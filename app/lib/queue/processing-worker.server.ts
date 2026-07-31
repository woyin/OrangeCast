import { getUserSettings } from "../settings.server";
import type { Db } from "../db.server";
import type { AppEnv } from "../env.server";
import { renderKnowledgeCardMarkdown } from "../export/markdown.server";
import { getProviders } from "../providers/index.server";
import type { TranscriptionInput, TranscriptSegment } from "../providers/types.server";
import { getSourceEpisodeForUser, updateEpisodeStatus } from "../repositories/episodes.server";
import { getPodcastForUser } from "../repositories/podcasts.server";
import { createJob, getJobById, markJobFailed, markJobRunning, markJobSucceeded } from "../repositories/jobs.server";
import { getUploadForUser, updateUploadStatus } from "../repositories/uploads.server";
import { upsertAnalysisMetadata } from "../repositories/analyses.server";
import { getTranscriptForSource, upsertTranscriptMetadata } from "../repositories/transcripts.server";
import { createUsageRecord, type UsageOperation } from "../repositories/usage-records.server";
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

function jobMatchesMessage(job: Awaited<ReturnType<typeof getJobById>>, message: ProcessingQueueMessage): boolean {
  return (
    !!job &&
    job.user_id === message.userId &&
    job.source_type === message.sourceType &&
    job.source_id === message.sourceId &&
    job.job_type === message.jobType
  );
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


async function r2ObjectToBlob(object: R2ObjectBody, contentType: string): Promise<Blob> {
  if (typeof object.blob === "function") return await object.blob();
  const text = await object.text();
  return new Blob([text], { type: contentType });
}

async function getSourceForTranscription(
  env: AppEnv,
  db: Db,
  userId: string,
  sourceType: SourceType,
  sourceId: string,
): Promise<{ title: string; transcriptionInput: TranscriptionInput }> {
  if (sourceType === "episode") {
    const episode = await getSourceEpisodeForUser(db, userId, sourceId);
    if (!episode) throw new Error("Source episode not found");
    return { title: episode.title, transcriptionInput: { sourceTitle: episode.title, audioUrl: episode.audio_url } };
  }

  const upload = await getUploadForUser(db, userId, sourceId);
  if (!upload) throw new Error("Source upload not found");
  const object = await env.R2.get(upload.r2_object_key);
  if (!object) throw new Error("Uploaded audio artifact not found");
  const audio = await r2ObjectToBlob(object, upload.content_type);
  return {
    title: upload.original_filename,
    transcriptionInput: {
      sourceTitle: upload.original_filename,
      audio,
      fileName: upload.original_filename,
      contentType: upload.content_type,
    },
  };
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

interface SourceExportMetadata {
  sourceTitle: string;
  podcastTitle: string | null;
  publishedAt: string | null;
  durationSeconds: number | null;
}

async function getSourceExportMetadata(
  db: Db,
  userId: string,
  sourceType: SourceType,
  sourceId: string,
): Promise<SourceExportMetadata> {
  if (sourceType === "episode") {
    const episode = await getSourceEpisodeForUser(db, userId, sourceId);
    if (!episode) throw new Error("Source episode not found");
    const podcast = await getPodcastForUser(db, userId, episode.podcast_id);
    return {
      sourceTitle: episode.title,
      podcastTitle: podcast?.title ?? null,
      publishedAt: episode.published_at,
      durationSeconds: episode.duration_seconds,
    };
  }

  const upload = await getUploadForUser(db, userId, sourceId);
  if (!upload) throw new Error("Source upload not found");
  return {
    sourceTitle: upload.original_filename,
    podcastTitle: null,
    publishedAt: null,
    durationSeconds: upload.duration_seconds,
  };
}

async function putText(env: AppEnv, key: string, value: string, contentType: string): Promise<void> {
  await env.R2.put(key, value, { httpMetadata: { contentType } });
}

async function readText(env: AppEnv, key: string): Promise<string> {
  const object = await env.R2.get(key);
  if (!object) throw new Error("Artifact not found");
  return await object.text();
}

function estimateTextUnits(value: string): number {
  return Math.ceil(value.length / 4);
}

function estimatedCost(operation: UsageOperation, inputUnits: number, outputUnits: number): number {
  if (operation === "transcription") {
    return inputUnits * 0.0001;
  }

  return inputUnits * 0.00000015 + outputUnits * 0.0000006;
}

async function recordUsage(
  db: Db,
  message: ProcessingQueueMessage,
  input: { operation: UsageOperation; provider: string; model: string; inputUnits: number; outputUnits: number },
): Promise<void> {
  try {
    await createUsageRecord(db, {
      userId: message.userId,
      sourceType: message.sourceType,
      sourceId: message.sourceId,
      provider: input.provider,
      model: input.model,
      operation: input.operation,
      inputUnits: input.inputUnits,
      outputUnits: input.outputUnits,
      estimatedCost: estimatedCost(input.operation, input.inputUnits, input.outputUnits),
    });
  } catch {
    // Usage records are best-effort telemetry and must not change processing state.
  }
}

async function runTranscribeJob(env: AppEnv, db: Db, message: ProcessingQueueMessage): Promise<void> {
  await updateSourceStatus(db, message.userId, message.sourceType, message.sourceId, "transcribing");

  const source = await getSourceForTranscription(env, db, message.userId, message.sourceType, message.sourceId);
  const userSettings = await getUserSettings(db, message.userId);
  const provider = getProviders(env, { transcriptionModel: userSettings.transcription_model, analysisModel: userSettings.analysis_model, chatModel: userSettings.chat_model }).transcription;
  const transcript = await provider.transcribe(source.transcriptionInput);

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

  await recordUsage(db, message, {
    operation: "transcription",
    provider: transcript.provider,
    model: transcript.model,
    inputUnits: transcript.durationSeconds ?? 0,
    outputUnits: estimateTextUnits(transcript.text),
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

  const succeeded = await markJobSucceeded(db, message.jobId, { provider: transcript.provider, model: transcript.model });
  if (succeeded) {
    await updateSourceStatus(db, message.userId, message.sourceType, message.sourceId, "transcribed");
  }
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

  const userSettings = await getUserSettings(db, message.userId);
  const provider = getProviders(env, { transcriptionModel: userSettings.transcription_model, analysisModel: userSettings.analysis_model, chatModel: userSettings.chat_model }).analysis;
  const analysis = await provider.analyze({ title, transcript: transcriptText, segments });
  const jsonKey = analysisJsonKey(message.userId, message.sourceType, message.sourceId);
  const markdownKey = analysisMarkdownKey(message.userId, message.sourceType, message.sourceId);
  const analysisMetadata = await upsertAnalysisMetadata(db, {
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
  const exportMetadata = await getSourceExportMetadata(db, message.userId, message.sourceType, message.sourceId);
  const markdown = renderKnowledgeCardMarkdown(
    analysis.card,
    {
      sourceTitle: exportMetadata.sourceTitle,
      sourceType: message.sourceType,
      sourceId: message.sourceId,
      podcastTitle: exportMetadata.podcastTitle,
      publishedAt: exportMetadata.publishedAt,
      processedAt: analysisMetadata.created_at,
      durationSeconds: exportMetadata.durationSeconds,
      createdAt: analysisMetadata.created_at,
    },
    { includeTranscriptAppendix: false },
  );

  await putText(env, jsonKey, JSON.stringify(analysis.card, null, 2), "application/json; charset=utf-8");
  await putText(env, markdownKey, markdown, "text/markdown; charset=utf-8");

  await recordUsage(db, message, {
    operation: "analysis",
    provider: analysis.provider,
    model: analysis.model,
    inputUnits: estimateTextUnits(`${title}
${transcriptText}`),
    outputUnits: estimateTextUnits(JSON.stringify(analysis.card)),
  });

  const succeeded = await markJobSucceeded(db, message.jobId, { provider: analysis.provider, model: analysis.model });
  if (succeeded) {
    await updateSourceStatus(db, message.userId, message.sourceType, message.sourceId, "processed");
  }
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

  const job = await getJobById(db, message.jobId);
  if (!jobMatchesMessage(job, message)) {
    await markJobFailed(db, message.jobId, "Queue message does not match claimed job");
    return;
  }

  try {
    if (message.jobType === "transcribe") {
      await runTranscribeJob(env, db, message);
      return;
    }

    await runAnalyzeJob(env, db, message);
  } catch (error) {
    const errorMessage = safeErrorMessage(error);
    const failed = await markJobFailed(db, message.jobId, errorMessage);
    if (failed) {
      await updateSourceStatus(db, message.userId, message.sourceType, message.sourceId, "failed");
    }
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
