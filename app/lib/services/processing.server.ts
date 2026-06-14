import type { Db } from "../db.server";
import type { AppEnv } from "../env.server";
import { updateEpisodeStatus } from "../repositories/episodes.server";
import { createJob } from "../repositories/jobs.server";
import { updateUploadStatus } from "../repositories/uploads.server";
import type {
  ProcessingJobStatus,
  ProcessingJobType,
  ProcessingQueueMessage,
  SourceProcessingStatus,
  SourceType,
} from "../queue/messages.server";

export function nextStatusForJob(
  jobType: ProcessingJobType,
  jobStatus: Exclude<ProcessingJobStatus, "pending">,
): SourceProcessingStatus {
  if (jobStatus === "failed") return "failed";

  if (jobType === "transcribe") {
    return jobStatus === "running" ? "transcribing" : "transcribed";
  }

  return jobStatus === "running" ? "analyzing" : "processed";
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
): Promise<{ jobId: string }> {
  const job = await createJob(db, { userId, sourceType, sourceId, jobType: "transcribe" });
  await updateSourceStatus(db, userId, sourceType, sourceId, "queued");

  const message: ProcessingQueueMessage = {
    type: "processing.job",
    jobType: "transcribe",
    jobId: job.id,
    userId,
    sourceType,
    sourceId,
  };
  await env.PROCESSING_QUEUE.send(message);

  return { jobId: job.id };
}
