import { type Db, newId, nowIso } from "../db.server";
import type { SourceType } from "../queue/messages.server";

export interface TranscriptRecord {
  id: string;
  user_id: string;
  source_type: SourceType;
  source_id: string;
  language: string | null;
  provider: string;
  model: string;
  text_r2_key: string;
  segments_r2_key: string;
  duration_seconds: number | null;
  created_at: string;
}

const transcriptColumns = `id, user_id, source_type, source_id, language, provider, model, text_r2_key, segments_r2_key, duration_seconds, created_at`;

export async function upsertTranscriptMetadata(
  db: Db,
  input: {
    userId: string;
    sourceType: SourceType;
    sourceId: string;
    language: string | null;
    provider: string;
    model: string;
    textR2Key: string;
    segmentsR2Key: string;
    durationSeconds: number | null;
  },
): Promise<TranscriptRecord> {
  const now = nowIso();

  await db
    .prepare(
      `INSERT INTO transcripts (id, user_id, source_type, source_id, language, provider, model, text_r2_key, segments_r2_key, duration_seconds, created_at)
       VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
       ON CONFLICT(user_id, source_type, source_id) DO UPDATE SET
         language = excluded.language,
         provider = excluded.provider,
         model = excluded.model,
         text_r2_key = excluded.text_r2_key,
         segments_r2_key = excluded.segments_r2_key,
         duration_seconds = excluded.duration_seconds`,
    )
    .bind(
      newId("trn"),
      input.userId,
      input.sourceType,
      input.sourceId,
      input.language,
      input.provider,
      input.model,
      input.textR2Key,
      input.segmentsR2Key,
      input.durationSeconds,
      now,
    )
    .run();

  const transcript = await getTranscriptForSource(db, input.userId, input.sourceType, input.sourceId);
  if (!transcript) throw new Error("Failed to upsert transcript metadata");
  return transcript;
}

export async function getTranscriptForSource(
  db: Db,
  userId: string,
  sourceType: SourceType,
  sourceId: string,
): Promise<TranscriptRecord | null> {
  return await db
    .prepare(
      `SELECT ${transcriptColumns}
       FROM transcripts
       WHERE user_id = ? AND source_type = ? AND source_id = ?
       LIMIT 1`,
    )
    .bind(userId, sourceType, sourceId)
    .first<TranscriptRecord>();
}
