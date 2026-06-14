import { type Db, newId, nowIso } from "../db.server";
import type { ParsedEpisode } from "../services/rss-parser.server";

export interface EpisodeRecord {
  id: string;
  user_id: string;
  podcast_id: string;
  guid: string;
  title: string;
  description: string | null;
  audio_url: string;
  duration_seconds: number | null;
  published_at: string | null;
  processing_status: string;
  created_at: string;
}

const episodeColumns = `id, user_id, podcast_id, guid, title, description, audio_url, duration_seconds, published_at, processing_status, created_at`;

export async function upsertEpisodesForPodcast(
  db: Db,
  userId: string,
  podcastId: string,
  episodes: ParsedEpisode[],
): Promise<void> {
  const now = nowIso();

  for (const episode of episodes) {
    await db
      .prepare(
        `INSERT INTO episodes (id, user_id, podcast_id, guid, title, description, audio_url, duration_seconds, published_at, processing_status, created_at)
         VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 'unprocessed', ?)
         ON CONFLICT(user_id, podcast_id, guid) DO UPDATE SET
           title = excluded.title,
           description = excluded.description,
           audio_url = excluded.audio_url,
           duration_seconds = excluded.duration_seconds,
           published_at = excluded.published_at`,
      )
      .bind(
        newId("epi"),
        userId,
        podcastId,
        episode.guid,
        episode.title,
        episode.description,
        episode.audioUrl,
        episode.durationSeconds,
        episode.publishedAt,
        now,
      )
      .run();
  }
}

export async function listEpisodesForPodcast(
  db: Db,
  userId: string,
  podcastId: string,
): Promise<EpisodeRecord[]> {
  const result = await db
    .prepare(
      `SELECT ${episodeColumns}
       FROM episodes
       WHERE user_id = ? AND podcast_id = ?
       ORDER BY published_at DESC, created_at DESC`,
    )
    .bind(userId, podcastId)
    .all<EpisodeRecord>();

  return result.results ?? [];
}

export async function getSourceEpisodeForUser(
  db: Db,
  userId: string,
  episodeId: string,
): Promise<EpisodeRecord | null> {
  return await db
    .prepare(
      `SELECT ${episodeColumns}
       FROM episodes
       WHERE user_id = ? AND id = ?
       LIMIT 1`,
    )
    .bind(userId, episodeId)
    .first<EpisodeRecord>();
}

export async function updateEpisodeStatus(
  db: Db,
  userId: string,
  episodeId: string,
  status: string,
): Promise<void> {
  await db
    .prepare(
      `UPDATE episodes
       SET processing_status = ?
       WHERE user_id = ? AND id = ?`,
    )
    .bind(status, userId, episodeId)
    .run();
}
