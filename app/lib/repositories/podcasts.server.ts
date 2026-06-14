import { type Db, newId, nowIso } from "../db.server";
import type { ParsedPodcast } from "../services/rss-parser.server";

export interface PodcastRecord {
  id: string;
  user_id: string;
  feed_url: string;
  title: string;
  description: string | null;
  image_url: string | null;
  site_url: string | null;
  last_fetched_at: string | null;
  created_at: string;
}

const podcastColumns = `id, user_id, feed_url, title, description, image_url, site_url, last_fetched_at, created_at`;

export async function upsertPodcastForUser(
  db: Db,
  userId: string,
  feedUrl: string,
  podcast: ParsedPodcast,
): Promise<PodcastRecord> {
  const now = nowIso();
  const id = newId("pod");

  await db
    .prepare(
      `INSERT INTO podcasts (id, user_id, feed_url, title, description, image_url, site_url, last_fetched_at, created_at)
       VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
       ON CONFLICT(user_id, feed_url) DO UPDATE SET
         title = excluded.title,
         description = excluded.description,
         image_url = excluded.image_url,
         site_url = excluded.site_url,
         last_fetched_at = excluded.last_fetched_at`,
    )
    .bind(
      id,
      userId,
      feedUrl,
      podcast.title,
      podcast.description,
      podcast.imageUrl,
      podcast.siteUrl,
      now,
      now,
    )
    .run();

  const record = await db
    .prepare(
      `SELECT ${podcastColumns}
       FROM podcasts
       WHERE user_id = ? AND feed_url = ?
       LIMIT 1`,
    )
    .bind(userId, feedUrl)
    .first<PodcastRecord>();

  if (!record) throw new Error("Failed to upsert podcast");
  return record;
}

export async function listPodcastsForUser(db: Db, userId: string): Promise<PodcastRecord[]> {
  const result = await db
    .prepare(
      `SELECT ${podcastColumns}
       FROM podcasts
       WHERE user_id = ?
       ORDER BY created_at DESC`,
    )
    .bind(userId)
    .all<PodcastRecord>();

  return result.results ?? [];
}

export async function getPodcastForUser(
  db: Db,
  userId: string,
  podcastId: string,
): Promise<PodcastRecord | null> {
  return await db
    .prepare(
      `SELECT ${podcastColumns}
       FROM podcasts
       WHERE user_id = ? AND id = ?
       LIMIT 1`,
    )
    .bind(userId, podcastId)
    .first<PodcastRecord>();
}
