import type { Db } from "../db.server";
import type { AppEnv } from "../env.server";
import { markJobRunning } from "../repositories/jobs.server";
import type { ProcessingQueueMessage } from "./messages.server";

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

export async function handleProcessingQueueMessage(
  _env: AppEnv,
  db: Db,
  message: ProcessingQueueMessage,
): Promise<void> {
  // Task 6 only defines the typed handler skeleton. Full artifact processing is Task 7.
  if (!isProcessingQueueMessage(message)) {
    throw new Error("Invalid processing queue message");
  }

  const claimed = await markJobRunning(db, message.jobId);
  if (!claimed) return;
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
