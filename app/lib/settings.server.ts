import type { AppEnv } from "./env.server";

export interface UserSettings {
  transcription_model: string | null;
  analysis_model: string | null;
  chat_model: string | null;
  updated_at: string | null;
}

const DEFAULT_SETTINGS: UserSettings = {
  transcription_model: null,
  analysis_model: null,
  chat_model: null,
  updated_at: null,
};

/**
 * Retrieve a user's model settings from D1.
 * Returns nulls for any field the user hasn't customised yet.
 */
export async function getUserSettings(db: AppEnv["DB"], userId: string): Promise<UserSettings> {
  const row = await db
    .prepare("SELECT transcription_model, analysis_model, chat_model, updated_at FROM settings WHERE user_id = ?")
    .bind(userId)
    .first<Pick<UserSettings, "transcription_model" | "analysis_model" | "chat_model" | "updated_at">>();

  return row ?? { ...DEFAULT_SETTINGS };
}

/**
 * Persist a user's model settings (upsert).
 * Empty strings are stored as NULL so the fallback chain kicks in.
 */
export async function saveUserSettings(
  db: AppEnv["DB"],
  userId: string,
  settings: {
    transcription_model?: string;
    analysis_model?: string;
    chat_model?: string;
  },
): Promise<UserSettings> {
  const toNull = (v: string | undefined) => (v?.trim() ? v.trim() : null);
  const now = new Date().toISOString();

  await db
    .prepare(
      `INSERT INTO settings (user_id, transcription_model, analysis_model, chat_model, updated_at)
       VALUES (?, ?, ?, ?, ?)
       ON CONFLICT(user_id) DO UPDATE SET
         transcription_model = excluded.transcription_model,
         analysis_model = excluded.analysis_model,
         chat_model = excluded.chat_model,
         updated_at = excluded.updated_at`,
    )
    .bind(
      userId,
      toNull(settings.transcription_model),
      toNull(settings.analysis_model),
      toNull(settings.chat_model),
      now,
    )
    .run();

  return {
    transcription_model: toNull(settings.transcription_model),
    analysis_model: toNull(settings.analysis_model),
    chat_model: toNull(settings.chat_model),
    updated_at: now,
  };
}
