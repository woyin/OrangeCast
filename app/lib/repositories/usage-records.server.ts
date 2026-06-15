import { type Db, newId, nowIso } from "../db.server";
import type { SourceType } from "../queue/messages.server";

export type UsageOperation = "transcription" | "analysis" | "chat";

export interface UsageRecord {
  id: string;
  user_id: string;
  source_type: SourceType | null;
  source_id: string | null;
  provider: string;
  model: string;
  operation: UsageOperation;
  input_units: number;
  output_units: number;
  estimated_cost: number;
  created_at: string;
}

export async function createUsageRecord(
  db: Db,
  input: {
    userId: string;
    sourceType?: SourceType | null;
    sourceId?: string | null;
    provider: string;
    model: string;
    operation: UsageOperation;
    inputUnits: number;
    outputUnits: number;
    estimatedCost: number;
  },
): Promise<UsageRecord> {
  const id = newId("use");
  const now = nowIso();
  await db
    .prepare(
      `INSERT INTO usage_records (id, user_id, source_type, source_id, provider, model, operation, input_units, output_units, estimated_cost, created_at)
       VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
    )
    .bind(
      id,
      input.userId,
      input.sourceType ?? null,
      input.sourceId ?? null,
      input.provider,
      input.model,
      input.operation,
      Math.max(0, Math.ceil(input.inputUnits)),
      Math.max(0, Math.ceil(input.outputUnits)),
      Math.max(0, input.estimatedCost),
      now,
    )
    .run();

  const record = await db
    .prepare(
      `SELECT id, user_id, source_type, source_id, provider, model, operation, input_units, output_units, estimated_cost, created_at
       FROM usage_records
       WHERE id = ?
       LIMIT 1`,
    )
    .bind(id)
    .first<UsageRecord>();
  if (!record) throw new Error("Failed to create usage record");
  return record;
}
