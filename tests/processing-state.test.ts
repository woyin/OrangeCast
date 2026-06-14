import { describe, expect, test, vi } from "vitest";
import { markJobFailed, markJobRunning, markJobSucceeded } from "../app/lib/repositories/jobs.server";
import { handleProcessingQueueMessage } from "../app/lib/queue/processing-worker.server";
import { enqueueProcessingForSource, nextStatusForJob } from "../app/lib/services/processing.server";
import type { Db } from "../app/lib/db.server";
import type { AppEnv } from "../app/lib/env.server";
import type { SourceProcessingStatus } from "../app/lib/queue/messages.server";

function d1Result(changes: number): D1Result {
  return {
    success: true,
    results: [],
    meta: {
      duration: 0,
      size_after: 0,
      rows_read: 0,
      rows_written: changes,
      last_row_id: 0,
      changed_db: changes > 0,
      changes,
    },
  };
}

function createJobTransitionDb(initialStatus: string) {
  const job = {
    id: "job_1",
    status: initialStatus,
    attempt_count: 0,
    error_message: null as string | null,
    provider: null as string | null,
    model: null as string | null,
  };

  const db = {
    prepare(sql: string) {
      return {
        bind(...values: unknown[]) {
          return {
            async run() {
              if (sql.includes("SET status = 'running'")) {
                if (job.status !== "queued") return d1Result(0);
                job.status = "running";
                job.attempt_count += 1;
                job.error_message = null;
                return d1Result(1);
              }

              if (sql.includes("SET status = 'succeeded'")) {
                if (job.status !== "running") return d1Result(0);
                job.status = "succeeded";
                job.provider = values[0] as string | null;
                job.model = values[1] as string | null;
                job.error_message = null;
                return d1Result(1);
              }

              if (sql.includes("SET status = 'failed'")) {
                if (job.status !== "running") return d1Result(0);
                job.status = "failed";
                job.error_message = values[0] as string;
                return d1Result(1);
              }

              throw new Error(`Unexpected SQL: ${sql}`);
            },
          };
        },
      };
    },
  } as unknown as Db;

  return { db, job };
}

function createProcessingDb(initialStatus: SourceProcessingStatus) {
  const state = {
    sourceStatus: initialStatus,
    jobs: [] as Array<{ id: string; status: string; error_message: string | null }>,
  };

  const db = {
    prepare(sql: string) {
      return {
        bind(...values: unknown[]) {
          return {
            async first<T>() {
              if (sql.includes("FROM uploads") && sql.includes("processing_status")) {
                return { processing_status: state.sourceStatus } as T;
              }

              if (sql.includes("FROM processing_jobs")) {
                return {
                  id: state.jobs.at(-1)?.id ?? "job_1",
                  user_id: "user_1",
                  source_type: "upload",
                  source_id: "upload_1",
                  job_type: "transcribe",
                  status: state.jobs.at(-1)?.status ?? "queued",
                  attempt_count: 0,
                  error_message: state.jobs.at(-1)?.error_message ?? null,
                  provider: null,
                  model: null,
                  started_at: null,
                  finished_at: null,
                  created_at: "2026-06-14T00:00:00.000Z",
                } as T;
              }

              throw new Error(`Unexpected first SQL: ${sql}`);
            },
            async run() {
              if (sql.includes("UPDATE uploads") && sql.includes("processing_status = 'queued'")) {
                const expectedCurrentStatus = values[2] as SourceProcessingStatus;
                if (
                  values[0] === "user_1" &&
                  values[1] === "upload_1" &&
                  state.sourceStatus === expectedCurrentStatus &&
                  (state.sourceStatus === "unprocessed" || state.sourceStatus === "failed")
                ) {
                  state.sourceStatus = "queued";
                  return d1Result(1);
                }
                return d1Result(0);
              }

              if (sql.includes("INSERT INTO processing_jobs")) {
                state.jobs.push({ id: values[0] as string, status: "queued", error_message: null });
                return d1Result(1);
              }

              if (sql.includes("UPDATE uploads") && sql.includes("SET processing_status = ?")) {
                state.sourceStatus = values[0] as SourceProcessingStatus;
                return d1Result(1);
              }

              if (sql.includes("UPDATE processing_jobs") && sql.includes("SET status = 'failed'")) {
                const job = state.jobs.find((candidate) => candidate.id === values[2]);
                const expectedStatus = sql.includes("status = 'queued'") ? "queued" : "running";
                if (!job || job.status !== expectedStatus) return d1Result(0);
                job.status = "failed";
                job.error_message = values[0] as string;
                return d1Result(1);
              }

              throw new Error(`Unexpected run SQL: ${sql}`);
            },
          };
        },
      };
    },
  } as unknown as Db;

  return { db, state };
}


function createWorkerTerminalDb(options: { markSucceededChanges: number; markFailedChanges?: number }) {
  const state = {
    sourceStatus: "queued" as SourceProcessingStatus,
    jobs: [{ id: "job_1", status: "queued", error_message: null as string | null }],
    transcripts: [] as Array<{ text_r2_key: string; segments_r2_key: string }>,
  };

  const db = {
    prepare(sql: string) {
      return {
        bind(...values: unknown[]) {
          return {
            async first<T>() {
              if (sql.includes("FROM uploads")) {
                return {
                  id: "upload_1",
                  user_id: "user_1",
                  original_filename: "Thinking.mp3",
                  content_type: "audio/mpeg",
                  size_bytes: 123,
                  duration_seconds: null,
                  r2_object_key: "users/user_1/uploads/upload_1/audio.mp3",
                  processing_status: state.sourceStatus,
                  created_at: "2026-06-14T00:00:00.000Z",
                } as T;
              }

              if (sql.includes("FROM transcripts")) {
                const transcript = state.transcripts.at(-1);
                if (!transcript) return null as T;
                return {
                  id: "trn_1",
                  user_id: "user_1",
                  source_type: "upload",
                  source_id: "upload_1",
                  language: "en",
                  provider: "mock",
                  model: "mock-transcriber-v1",
                  text_r2_key: transcript.text_r2_key,
                  segments_r2_key: transcript.segments_r2_key,
                  duration_seconds: 120,
                  created_at: "2026-06-14T00:00:00.000Z",
                } as T;
              }

              if (sql.includes("FROM processing_jobs")) {
                const job = state.jobs.at(-1) ?? state.jobs[0];
                return {
                  id: job?.id ?? "job_2",
                  user_id: "user_1",
                  source_type: "upload",
                  source_id: "upload_1",
                  job_type: "analyze",
                  status: job?.status ?? "queued",
                  attempt_count: 0,
                  error_message: job?.error_message ?? null,
                  provider: null,
                  model: null,
                  started_at: null,
                  finished_at: null,
                  created_at: "2026-06-14T00:00:00.000Z",
                } as T;
              }

              throw new Error(`Unexpected first SQL: ${sql}`);
            },
            async run() {
              if (sql.includes("UPDATE processing_jobs") && sql.includes("SET status = 'running'")) {
                const job = state.jobs.find((candidate) => candidate.id === values[1]);
                if (!job || job.status !== "queued") return d1Result(0);
                job.status = "running";
                return d1Result(1);
              }

              if (sql.includes("UPDATE processing_jobs") && sql.includes("SET status = 'succeeded'")) {
                return d1Result(options.markSucceededChanges);
              }

              if (sql.includes("UPDATE processing_jobs") && sql.includes("SET status = 'failed'")) {
                return d1Result(options.markFailedChanges ?? 1);
              }

              if (sql.includes("UPDATE uploads") && sql.includes("SET processing_status = ?")) {
                state.sourceStatus = values[0] as SourceProcessingStatus;
                return d1Result(1);
              }

              if (sql.includes("INSERT INTO transcripts")) {
                state.transcripts.push({ text_r2_key: values[7] as string, segments_r2_key: values[8] as string });
                return d1Result(1);
              }

              if (sql.includes("INSERT INTO processing_jobs")) {
                state.jobs.push({ id: values[0] as string, status: "queued", error_message: null });
                return d1Result(1);
              }

              throw new Error(`Unexpected run SQL: ${sql}`);
            },
          };
        },
      };
    },
  } as unknown as Db;

  return { db, state };
}

function createWorkerEnv(r2Overrides: Partial<R2Bucket> = {}): AppEnv {
  const r2Objects = new Map<string, string>();
  return {
    DB: {} as D1Database,
    R2: {
      async put(key: string, value: string) {
        r2Objects.set(key, value);
        return {} as R2Object;
      },
      ...r2Overrides,
    } as unknown as R2Bucket,
    PROCESSING_QUEUE: { send: vi.fn() } as unknown as Queue,
    APP_NAME: "CloudWisePod",
    SESSION_COOKIE_NAME: "session",
    SESSION_SECRET: "secret",
    UPLOAD_MAX_BYTES: "1000",
    UPLOAD_MAX_SECONDS: "1000",
  };
}

describe("nextStatusForJob", () => {
  test("maps transcribe and analyze job states to source processing statuses", () => {
    expect(nextStatusForJob("transcribe", "running")).toBe("transcribing");
    expect(nextStatusForJob("transcribe", "succeeded")).toBe("transcribed");
    expect(nextStatusForJob("analyze", "running")).toBe("analyzing");
    expect(nextStatusForJob("analyze", "succeeded")).toBe("processed");
  });

  test("maps failed jobs to failed source processing status", () => {
    expect(nextStatusForJob("transcribe", "failed")).toBe("failed");
    expect(nextStatusForJob("analyze", "failed")).toBe("failed");
  });
});

describe("processing enqueue state machine", () => {
  test("does not create or send a job when source has already been claimed", async () => {
    const { db, state } = createProcessingDb("queued");
    const send = vi.fn();

    const result = await enqueueProcessingForSource(
      { PROCESSING_QUEUE: { send } as unknown as Queue },
      db,
      "user_1",
      "upload",
      "upload_1",
    );

    expect(result).toEqual({ enqueued: false, reason: "already_processing" });
    expect(state.jobs).toHaveLength(0);
    expect(send).not.toHaveBeenCalled();
    expect(state.sourceStatus).toBe("queued");
  });

  test("restores the claimed source status when queue send fails", async () => {
    const { db, state } = createProcessingDb("failed");
    const send = vi.fn().mockRejectedValue(new Error("queue unavailable"));

    const result = await enqueueProcessingForSource(
      { PROCESSING_QUEUE: { send } as unknown as Queue },
      db,
      "user_1",
      "upload",
      "upload_1",
    );

    expect(result).toEqual({ enqueued: false, reason: "queue_send_failed" });
    expect(state.jobs).toHaveLength(1);
    expect(state.jobs[0]?.status).toBe("failed");
    expect(state.jobs[0]?.error_message).toBe("Failed to enqueue processing job");
    expect(state.sourceStatus).toBe("failed");
  });
});

describe("processing job transitions", () => {
  test("markJobRunning only transitions queued jobs", async () => {
    const queued = createJobTransitionDb("queued");
    await expect(markJobRunning(queued.db, "job_1")).resolves.toBe(true);
    expect(queued.job.status).toBe("running");
    expect(queued.job.attempt_count).toBe(1);

    const stale = createJobTransitionDb("succeeded");
    await expect(markJobRunning(stale.db, "job_1")).resolves.toBe(false);
    expect(stale.job.status).toBe("succeeded");
    expect(stale.job.attempt_count).toBe(0);
  });

  test("terminal job updates only transition running jobs", async () => {
    const runningSucceeded = createJobTransitionDb("running");
    await expect(markJobSucceeded(runningSucceeded.db, "job_1", { provider: "mock", model: "mock-v1" })).resolves.toBe(true);
    expect(runningSucceeded.job.status).toBe("succeeded");
    expect(runningSucceeded.job.provider).toBe("mock");

    const staleSucceeded = createJobTransitionDb("queued");
    await expect(markJobSucceeded(staleSucceeded.db, "job_1")).resolves.toBe(false);
    expect(staleSucceeded.job.status).toBe("queued");

    const runningFailed = createJobTransitionDb("running");
    await expect(markJobFailed(runningFailed.db, "job_1", "boom")).resolves.toBe(true);
    expect(runningFailed.job.status).toBe("failed");
    expect(runningFailed.job.error_message).toBe("boom");

    const staleFailed = createJobTransitionDb("succeeded");
    await expect(markJobFailed(staleFailed.db, "job_1", "boom")).resolves.toBe(false);
    expect(staleFailed.job.status).toBe("succeeded");
  });
});


describe("processing worker guarded source transitions", () => {
  test("does not mark a source transcribed when the transcribe job success transition is stale", async () => {
    const { db, state } = createWorkerTerminalDb({ markSucceededChanges: 0 });
    const env = createWorkerEnv();

    await handleProcessingQueueMessage(env, db, {
      type: "processing.job",
      jobType: "transcribe",
      jobId: "job_1",
      userId: "user_1",
      sourceType: "upload",
      sourceId: "upload_1",
    });

    expect(state.sourceStatus).toBe("transcribing");
  });

  test("does not mark a source failed when the job failure transition is stale", async () => {
    const { db, state } = createWorkerTerminalDb({ markSucceededChanges: 0, markFailedChanges: 0 });
    const env = createWorkerEnv({
      async put() {
        throw new Error("R2 unavailable");
      },
    });

    await handleProcessingQueueMessage(env, db, {
      type: "processing.job",
      jobType: "transcribe",
      jobId: "job_1",
      userId: "user_1",
      sourceType: "upload",
      sourceId: "upload_1",
    });

    expect(state.sourceStatus).toBe("transcribing");
  });
});
