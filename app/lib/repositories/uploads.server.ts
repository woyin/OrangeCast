import { type Db, nowIso } from "../db.server";

export interface UploadRecord {
  id: string;
  user_id: string;
  original_filename: string;
  content_type: string;
  size_bytes: number;
  duration_seconds: number | null;
  r2_object_key: string;
  processing_status: string;
  created_at: string;
}

const uploadColumns = `id, user_id, original_filename, content_type, size_bytes, duration_seconds, r2_object_key, processing_status, created_at`;

export async function createUpload(
  db: Db,
  input: {
    id: string;
    userId: string;
    originalFilename: string;
    contentType: string;
    sizeBytes: number;
    durationSeconds: number | null;
    r2ObjectKey: string;
  },
): Promise<UploadRecord> {
  const now = nowIso();

  await db
    .prepare(
      `INSERT INTO uploads (id, user_id, original_filename, content_type, size_bytes, duration_seconds, r2_object_key, processing_status, created_at)
       VALUES (?, ?, ?, ?, ?, ?, ?, 'unprocessed', ?)`,
    )
    .bind(
      input.id,
      input.userId,
      input.originalFilename,
      input.contentType,
      input.sizeBytes,
      input.durationSeconds,
      input.r2ObjectKey,
      now,
    )
    .run();

  const upload = await getUploadForUser(db, input.userId, input.id);
  if (!upload) throw new Error("Failed to create upload");
  return upload;
}

export async function listUploadsForUser(db: Db, userId: string): Promise<UploadRecord[]> {
  const result = await db
    .prepare(
      `SELECT ${uploadColumns}
       FROM uploads
       WHERE user_id = ?
       ORDER BY created_at DESC`,
    )
    .bind(userId)
    .all<UploadRecord>();

  return result.results ?? [];
}

export async function getUploadForUser(
  db: Db,
  userId: string,
  uploadId: string,
): Promise<UploadRecord | null> {
  return await db
    .prepare(
      `SELECT ${uploadColumns}
       FROM uploads
       WHERE user_id = ? AND id = ?
       LIMIT 1`,
    )
    .bind(userId, uploadId)
    .first<UploadRecord>();
}

export async function claimUploadForProcessing(
  db: Db,
  userId: string,
  uploadId: string,
): Promise<{ previousStatus: "unprocessed" | "failed" } | null> {
  const current = await db
    .prepare(
      `SELECT processing_status
       FROM uploads
       WHERE user_id = ? AND id = ?
       LIMIT 1`,
    )
    .bind(userId, uploadId)
    .first<{ processing_status: string }>();

  if (current?.processing_status !== "unprocessed" && current?.processing_status !== "failed") {
    return null;
  }

  const result = await db
    .prepare(
      `UPDATE uploads
       SET processing_status = 'queued'
       WHERE user_id = ? AND id = ? AND processing_status = ? AND processing_status IN ('unprocessed', 'failed')`,
    )
    .bind(userId, uploadId, current.processing_status)
    .run();

  if (result.meta.changes === 0) return null;
  return { previousStatus: current.processing_status };
}

export async function updateUploadStatus(
  db: Db,
  userId: string,
  uploadId: string,
  status: string,
): Promise<void> {
  await db
    .prepare(
      `UPDATE uploads
       SET processing_status = ?
       WHERE user_id = ? AND id = ?`,
    )
    .bind(status, userId, uploadId)
    .run();
}
