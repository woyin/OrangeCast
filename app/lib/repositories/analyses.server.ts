import { type Db, newId, nowIso } from "../db.server";
import type { SourceType } from "../queue/messages.server";

export interface AnalysisRecord {
  id: string;
  user_id: string;
  source_type: SourceType;
  source_id: string;
  provider: string;
  model: string;
  title: string;
  summary: string;
  content_json_r2_key: string;
  markdown_r2_key: string;
  created_at: string;
}

const analysisColumns = `id, user_id, source_type, source_id, provider, model, title, summary, content_json_r2_key, markdown_r2_key, created_at`;

export async function upsertAnalysisMetadata(
  db: Db,
  input: {
    userId: string;
    sourceType: SourceType;
    sourceId: string;
    provider: string;
    model: string;
    title: string;
    summary: string;
    contentJsonR2Key: string;
    markdownR2Key: string;
  },
): Promise<AnalysisRecord> {
  const now = nowIso();

  await db
    .prepare(
      `INSERT INTO analyses (id, user_id, source_type, source_id, provider, model, title, summary, content_json_r2_key, markdown_r2_key, created_at)
       VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
       ON CONFLICT(user_id, source_type, source_id) DO UPDATE SET
         provider = excluded.provider,
         model = excluded.model,
         title = excluded.title,
         summary = excluded.summary,
         content_json_r2_key = excluded.content_json_r2_key,
         markdown_r2_key = excluded.markdown_r2_key`,
    )
    .bind(
      newId("ana"),
      input.userId,
      input.sourceType,
      input.sourceId,
      input.provider,
      input.model,
      input.title,
      input.summary,
      input.contentJsonR2Key,
      input.markdownR2Key,
      now,
    )
    .run();

  const analysis = await getAnalysisForSource(db, input.userId, input.sourceType, input.sourceId);
  if (!analysis) throw new Error("Failed to upsert analysis metadata");
  return analysis;
}

export async function getAnalysisForSource(
  db: Db,
  userId: string,
  sourceType: SourceType,
  sourceId: string,
): Promise<AnalysisRecord | null> {
  return await db
    .prepare(
      `SELECT ${analysisColumns}
       FROM analyses
       WHERE user_id = ? AND source_type = ? AND source_id = ?
       LIMIT 1`,
    )
    .bind(userId, sourceType, sourceId)
    .first<AnalysisRecord>();
}
