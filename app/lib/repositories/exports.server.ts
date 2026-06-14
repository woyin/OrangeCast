import { type Db, newId, nowIso } from "../db.server";
import type { SourceType } from "../queue/messages.server";

export type ExportStatus = "completed" | "failed";
export type ExportType = "markdown_zip";

export interface ExportRecord {
  id: string;
  user_id: string;
  export_type: ExportType;
  r2_object_key: string;
  status: ExportStatus;
  expires_at: string | null;
  created_at: string;
}

export interface ProcessedExportSource {
  sourceType: SourceType;
  sourceId: string;
  title: string;
  podcastTitle: string;
  markdownR2Key: string;
  contentJsonR2Key: string;
  transcriptTextR2Key: string | null;
  analysisCreatedAt: string;
}

const exportColumns = `id, user_id, export_type, r2_object_key, status, expires_at, created_at`;

export function exportZipKey(userId: string, exportId: string): string {
  return `users/${userId}/exports/${exportId}.zip`;
}

export async function createExportRecord(
  db: Db,
  input: {
    userId: string;
    exportType?: ExportType;
    r2ObjectKey?: string;
    status?: ExportStatus;
    expiresAt?: string | null;
  },
): Promise<ExportRecord> {
  const id = newId("exp");
  const r2ObjectKey = input.r2ObjectKey ?? exportZipKey(input.userId, id);
  const now = nowIso();

  await db
    .prepare(
      `INSERT INTO exports (id, user_id, export_type, r2_object_key, status, expires_at, created_at)
       VALUES (?, ?, ?, ?, ?, ?, ?)`,
    )
    .bind(id, input.userId, input.exportType ?? "markdown_zip", r2ObjectKey, input.status ?? "completed", input.expiresAt ?? null, now)
    .run();

  const record = await getExportForUser(db, input.userId, id);
  if (!record) throw new Error("Failed to create export record");
  return record;
}

export async function getExportForUser(db: Db, userId: string, exportId: string): Promise<ExportRecord | null> {
  return await db
    .prepare(
      `SELECT ${exportColumns}
       FROM exports
       WHERE user_id = ? AND id = ?
       LIMIT 1`,
    )
    .bind(userId, exportId)
    .first<ExportRecord>();
}

export async function listRecentExportsForUser(db: Db, userId: string, limit = 20): Promise<ExportRecord[]> {
  const result = await db
    .prepare(
      `SELECT ${exportColumns}
       FROM exports
       WHERE user_id = ?
       ORDER BY created_at DESC
       LIMIT ?`,
    )
    .bind(userId, limit)
    .all<ExportRecord>();

  return result.results ?? [];
}

export async function listProcessedExportSources(db: Db, userId: string): Promise<ProcessedExportSource[]> {
  const [episodes, uploads] = await Promise.all([
    db
      .prepare(
        `SELECT
           'episode' AS sourceType,
           e.id AS sourceId,
           a.title AS title,
           p.title AS podcastTitle,
           a.markdown_r2_key AS markdownR2Key,
           a.content_json_r2_key AS contentJsonR2Key,
           t.text_r2_key AS transcriptTextR2Key,
           a.created_at AS analysisCreatedAt
         FROM analyses a
         INNER JOIN episodes e ON e.user_id = a.user_id AND e.id = a.source_id AND a.source_type = 'episode'
         INNER JOIN podcasts p ON p.user_id = e.user_id AND p.id = e.podcast_id
         LEFT JOIN transcripts t ON t.user_id = a.user_id AND t.source_type = a.source_type AND t.source_id = a.source_id
         WHERE a.user_id = ? AND e.processing_status = 'processed'
         ORDER BY a.created_at DESC`,
      )
      .bind(userId)
      .all<ProcessedExportSource>(),
    db
      .prepare(
        `SELECT
           'upload' AS sourceType,
           u.id AS sourceId,
           a.title AS title,
           'Uploads' AS podcastTitle,
           a.markdown_r2_key AS markdownR2Key,
           a.content_json_r2_key AS contentJsonR2Key,
           t.text_r2_key AS transcriptTextR2Key,
           a.created_at AS analysisCreatedAt
         FROM analyses a
         INNER JOIN uploads u ON u.user_id = a.user_id AND u.id = a.source_id AND a.source_type = 'upload'
         LEFT JOIN transcripts t ON t.user_id = a.user_id AND t.source_type = a.source_type AND t.source_id = a.source_id
         WHERE a.user_id = ? AND u.processing_status = 'processed'
         ORDER BY a.created_at DESC`,
      )
      .bind(userId)
      .all<ProcessedExportSource>(),
  ]);

  return [...(episodes.results ?? []), ...(uploads.results ?? [])].sort((a, b) =>
    b.analysisCreatedAt.localeCompare(a.analysisCreatedAt),
  );
}
