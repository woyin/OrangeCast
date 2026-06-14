import type { Db } from "../db.server";
import type { AppEnv } from "../env.server";
import { renderKnowledgeCardMarkdown } from "../export/markdown.server";
import { createMockAnalysisProvider, createMockTranscriptionProvider } from "../providers/mock.server";
import type { TranscriptSegment } from "../providers/types.server";
import { getSourceEpisodeForUser, updateEpisodeStatus } from "../repositories/episodes.server";
import { createJob, markJobFailed, markJobRunning, markJobSucceeded } from "../repositories/jobs.server";
import { getUploadForUser, updateUploadStatus } from "../repositories/uploads.server";
import { upsertAnalysisMetadata } from "../repositories/analyses.server";
import { getTranscriptForSource, upsertTranscriptMetadata } from "../repositories/transcripts.server";
import {
  analysisJsonKey,
  analysisMarkdownKey,
  transcriptSegmentsKey,
  transcriptTextKey,
} from "../services/artifacts.server";
import type { ProcessingQueueMessage, SourceProcessingStatus, SourceType } from "./messages.server";

export function isProcessingQueueMessage(value: unknown): value is ProcessingQueueMessage {
  if (!value || typeof value !== "object") return false;
  const message = value as Partial<ProcessingQueueMessage>;
  return (
    message.type === "processing.job" &&
    (message.jobType === "transcribe" || message.jobType === "analyze") &&
    typeof message.jobId === "string" &&
    typeof message.userId === "string" &&
    (message.sourceType === "episode" || message.sourceType === "upload") &&
    typeof message.sourceId === "string"
  );
}

function safeErrorMessage(error: unknown): string {
  const message = error instanceof Error ? error.message : "Processing failed";
  return message.slice(0, 500);
}

async function updateSourceStatus(
  db: Db,
  userId: string,
  sourceType: SourceType,
  sourceId: string,
  status: SourceProcessingStatus,
): Promise<void> {
  if (sourceType === "episode") {
    await updateEpisodeStatus(db, userId, sourceId, status);
    return;
  }

  await updateUploadStatus(db, userId, sourceId, status);
}

async function getSourceForTranscription(
  env: AppEnv,
  db: Db,
  userId: string,
  sourceType: SourceType,
  sourceId: string,
): Promise<{ title: string; audioUrl: string }> {
  if (sourceType === "episode") {
    const episode = await getSourceEpisodeForUser(db, userId, sourceId);
    if (!episode) throw new Error("Source episode not found");
    return { title: episode.title, audioUrl: episode.audio_url };
  }

  const upload = await getUploadForUser(db, userId, sourceId);
  if (!upload) throw new Error("Source upload not found");
  return { title: upload.original_filename, audioUrl: `r2://${upload.r2_object_key}` };
}

async function getSourceTitle(
  db: Db,
  userId: string,
  sourceType: SourceType,
  sourceId: string,
): Promise<string> {
  if (sourceType === "episode") {
    const episode = await getSourceEpisodeForUser(db, userId, sourceId);
    if (!episode) throw new Error("Source episode not found");
    return episode.title;
  }

  const upload = await getUploadForUser(db, userId, sourceId);
  if (!upload) throw new Error("Source upload not found");
  return upload.original_filename;
}

async function putText(env: AppEnv, key: string, value: string, contentType: string): Promise<void> {
  await env.R2.put(key, value, { httpMetadata: { contentType } });
}

async function readText(env: AppEnv, key: string): Promise<string> {
  const object = await env.R2.get(key);
  if (!object) throw new Error("Artifact not found");
  return await object.text();
}

async function runTranscribeJob(env: AppEnv, db: Db, message: ProcessingQueueMessage): Promise<void> {
  await updateSourceStatus(db, message.userId, message.sourceType, message.sourceId, "transcribing");

  const source = await getSourceForTranscription(env, db, message.userId, message.sourceType, message.sourceId);
  const provider = createMockTranscriptionProvider();
  const transcript = await provider.transcribe({ audioUrl: source.audioUrl, sourceTitle: source.title });

  const textKey = transcriptTextKey(message.userId, message.sourceType, message.sourceId);
  const segmentsKey = transcriptSegmentsKey(message.userId, message.sourceType, message.sourceId);

  await putText(env, textKey, transcript.text, "text/plain; charset=utf-8");
  await putText(env, segmentsKey, JSON.stringify(transcript.segments, null, 2), "application/json; charset=utf-8");

  await upsertTranscriptMetadata(db, {
    userId: message.userId,
    sourceType: message.sourceType,
    sourceId: message.sourceId,
    language: transcript.language,
    provider: transcript.provider,
    model: transcript.model,
    textR2Key: textKey,
    segmentsR2Key: segmentsKey,
    durationSeconds: transcript.durationSeconds,
  });

  const analyzeJob = await createJob(db, {
    userId: message.userId,
    sourceType: message.sourceType,
    sourceId: message.sourceId,
    jobType: "analyze",
  });

  const analyzeMessage: ProcessingQueueMessage = {
    type: "processing.job",
    jobType: "analyze",
    jobId: analyzeJob.id,
    userId: message.userId,
    sourceType: message.sourceType,
    sourceId: message.sourceId,
  };
  await env.PROCESSING_QUEUE.send(analyzeMessage);

  await updateSourceStatus(db, message.userId, message.sourceType, message.sourceId, "transcribed");
  await markJobSucceeded(db, message.jobId, { provider: transcript.provider, model: transcript.model });
}

async function runAnalyzeJob(env: AppEnv, db: Db, message: ProcessingQueueMessage): Promise<void> {
  await updateSourceStatus(db, message.userId, message.sourceType, message.sourceId, "analyzing");

  const transcript = await getTranscriptForSource(db, message.userId, message.sourceType, message.sourceId);
  if (!transcript) throw new Error("Transcript metadata not found");

  const [title, transcriptText, segmentsJson] = await Promise.all([
    getSourceTitle(db, message.userId, message.sourceType, message.sourceId),
    readText(env, transcript.text_r2_key),
    readText(env, transcript.segments_r2_key),
  ]);
  const segments = JSON.parse(segmentsJson) as TranscriptSegment[];

  const provider = createMockAnalysisProvider();
  const analysis = await provider.analyze({ title, transcript: transcriptText, segments });
  const jsonKey = analysisJsonKey(message.userId, message.sourceType, message.sourceId);
  const markdownKey = analysisMarkdownKey(message.userId, message.sourceType, message.sourceId);
  const createdAt = new Date().toISOString();
  const markdown = renderKnowledgeCardMarkdown(
    analysis.card,
    {
      sourceTitle: title,
      sourceType: message.sourceType,
      sourceId: message.sourceId,
      createdAt,
    },
    { includeTranscriptAppendix: false },
  );

  await putText(env, jsonKey, JSON.stringify(analysis.card, null, 2), "application/json; charset=utf-8");
  await putText(env, markdownKey, markdown, "text/markdown; charset=utf-8");

  await upsertAnalysisMetadata(db, {
    userId: message.userId,
    sourceType: message.sourceType,
    sourceId: message.sourceId,
    provider: analysis.provider,
    model: analysis.model,
    title: analysis.card.title,
    summary: analysis.card.summary,
    contentJsonR2Key: jsonKey,
    markdownR2Key: markdownKey,
  });

  await updateSourceStatus(db, message.userId, message.sourceType, message.sourceId, "processed");
  await markJobSucceeded(db, message.jobId, { provider: analysis.provider, model: analysis.model });
}

export async function handleProcessingQueueMessage(
  env: AppEnv,
  db: Db,
  message: ProcessingQueueMessage,
): Promise<void> {
  if (!isProcessingQueueMessage(message)) {
    throw new Error("Invalid processing queue message");
  }

  const claimed = await markJobRunning(db, message.jobId);
  if (!claimed) return;

  try {
    if (message.jobType === "transcribe") {
      await runTranscribeJob(env, db, message);
      return;
    }

    await runAnalyzeJob(env, db, message);
  } catch (error) {
    const errorMessage = safeErrorMessage(error);
    await markJobFailed(db, message.jobId, errorMessage);
    await updateSourceStatus(db, message.userId, message.sourceType, message.sourceId, "failed");
  }
}

export async function handleProcessingQueueBatch(
  env: AppEnv,
  batch: MessageBatch<ProcessingQueueMessage>,
): Promise<void> {
  for (const message of batch.messages) {
    await handleProcessingQueueMessage(env, env.DB, message.body);
    message.ack();
  }
}
