import type { Db } from "../db.server";
import type { AppEnv } from "../env.server";
import { claimEpisodeForProcessing, updateEpisodeStatus } from "../repositories/episodes.server";
import { createJob, markQueuedJobFailed } from "../repositories/jobs.server";
import { claimUploadForProcessing, updateUploadStatus } from "../repositories/uploads.server";
import type {
  ProcessingJobStatus,
  ProcessingJobType,
  ProcessingQueueMessage,
  SourceProcessingStatus,
  SourceType,
} from "../queue/messages.server";

export type EnqueueProcessingResult =
  | { enqueued: true; jobId: string }
  | { enqueued: false; reason: "already_processing" | "queue_send_failed" };

export function nextStatusForJob(
  jobType: ProcessingJobType,
  jobStatus: Exclude<ProcessingJobStatus, "queued">,
): SourceProcessingStatus {
  if (jobStatus === "failed") return "failed";

  if (jobType === "transcribe") {
    return jobStatus === "running" ? "transcribing" : "transcribed";
  }

  return jobStatus === "running" ? "analyzing" : "processed";
}

async function claimSourceForProcessing(
  db: Db,
  userId: string,
  sourceType: SourceType,
  sourceId: string,
): Promise<{ previousStatus: "unprocessed" | "failed" } | null> {
  if (sourceType === "episode") {
    return await claimEpisodeForProcessing(db, userId, sourceId);
  }

  return await claimUploadForProcessing(db, userId, sourceId);
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

export async function enqueueProcessingForSource(
  env: Pick<AppEnv, "PROCESSING_QUEUE">,
  db: Db,
  userId: string,
  sourceType: SourceType,
  sourceId: string,
): Promise<EnqueueProcessingResult> {
  const claim = await claimSourceForProcessing(db, userId, sourceType, sourceId);
  if (!claim) return { enqueued: false, reason: "already_processing" };

  const job = await createJob(db, { userId, sourceType, sourceId, jobType: "transcribe" });
  const message: ProcessingQueueMessage = {
    type: "processing.job",
    jobType: "transcribe",
    jobId: job.id,
    userId,
    sourceType,
    sourceId,
  };

  try {
    await env.PROCESSING_QUEUE.send(message);
  } catch (_error) {
    await markQueuedJobFailed(db, job.id, "Failed to enqueue processing job");
    await updateSourceStatus(db, userId, sourceType, sourceId, claim.previousStatus);
    return { enqueued: false, reason: "queue_send_failed" };
  }

  return { enqueued: true, jobId: job.id };
}
