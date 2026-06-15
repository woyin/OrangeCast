import { type Db, newId, nowIso } from "../db.server";
import type { ProcessingJobStatus, ProcessingJobType, SourceType } from "../queue/messages.server";

export interface ProcessingJobRecord {
  id: string;
  user_id: string;
  source_type: SourceType;
  source_id: string;
  job_type: ProcessingJobType;
  status: ProcessingJobStatus;
  attempt_count: number;
  error_message: string | null;
  provider: string | null;
  model: string | null;
  started_at: string | null;
  finished_at: string | null;
  created_at: string;
}

const jobColumns = `id, user_id, source_type, source_id, job_type, status, attempt_count, error_message, provider, model, started_at, finished_at, created_at`;

function changed(result: D1Result): boolean {
  return result.meta.changes > 0;
}

export async function createJob(
  db: Db,
  input: {
    userId: string;
    sourceType: SourceType;
    sourceId: string;
    jobType: ProcessingJobType;
  },
): Promise<ProcessingJobRecord> {
  const id = newId("job");
  const now = nowIso();

  await db
    .prepare(
      `INSERT INTO processing_jobs (id, user_id, source_type, source_id, job_type, status, attempt_count, created_at)
       VALUES (?, ?, ?, ?, ?, 'queued', 0, ?)`,
    )
    .bind(id, input.userId, input.sourceType, input.sourceId, input.jobType, now)
    .run();

  const job = await db
    .prepare(`SELECT ${jobColumns} FROM processing_jobs WHERE id = ? LIMIT 1`)
    .bind(id)
    .first<ProcessingJobRecord>();

  if (!job) throw new Error("Failed to create processing job");
  return job;
}

export async function getJobById(db: Db, jobId: string): Promise<ProcessingJobRecord | null> {
  return await db
    .prepare(`SELECT ${jobColumns} FROM processing_jobs WHERE id = ? LIMIT 1`)
    .bind(jobId)
    .first<ProcessingJobRecord>();
}

export async function markJobRunning(db: Db, jobId: string): Promise<boolean> {
  const result = await db
    .prepare(
      `UPDATE processing_jobs
       SET status = 'running', attempt_count = attempt_count + 1, error_message = NULL, started_at = ?
       WHERE id = ? AND status = 'queued'`,
    )
    .bind(nowIso(), jobId)
    .run();

  return changed(result);
}

export async function markJobSucceeded(
  db: Db,
  jobId: string,
  input: { provider?: string | null; model?: string | null } = {},
): Promise<boolean> {
  const result = await db
    .prepare(
      `UPDATE processing_jobs
       SET status = 'succeeded', provider = ?, model = ?, error_message = NULL, finished_at = ?
       WHERE id = ? AND status = 'running'`,
    )
    .bind(input.provider ?? null, input.model ?? null, nowIso(), jobId)
    .run();

  return changed(result);
}

export async function markJobFailed(db: Db, jobId: string, errorMessage: string): Promise<boolean> {
  const result = await db
    .prepare(
      `UPDATE processing_jobs
       SET status = 'failed', error_message = ?, finished_at = ?
       WHERE id = ? AND status = 'running'`,
    )
    .bind(errorMessage, nowIso(), jobId)
    .run();

  return changed(result);
}

export async function markQueuedJobFailed(
  db: Db,
  jobId: string,
  errorMessage: string,
): Promise<boolean> {
  const result = await db
    .prepare(
      `UPDATE processing_jobs
       SET status = 'failed', error_message = ?, finished_at = ?
       WHERE id = ? AND status = 'queued'`,
    )
    .bind(errorMessage, nowIso(), jobId)
    .run();

  return changed(result);
}

export async function findPendingJob(
  db: Db,
  input?: { jobType?: ProcessingJobType; sourceType?: SourceType; sourceId?: string },
): Promise<ProcessingJobRecord | null> {
  const clauses = ["status = 'queued'"];
  const values: string[] = [];

  if (input?.jobType) {
    clauses.push("job_type = ?");
    values.push(input.jobType);
  }
  if (input?.sourceType) {
    clauses.push("source_type = ?");
    values.push(input.sourceType);
  }
  if (input?.sourceId) {
    clauses.push("source_id = ?");
    values.push(input.sourceId);
  }

  return await db
    .prepare(
      `SELECT ${jobColumns}
       FROM processing_jobs
       WHERE ${clauses.join(" AND ")}
       ORDER BY created_at ASC
       LIMIT 1`,
    )
    .bind(...values)
    .first<ProcessingJobRecord>();
}
