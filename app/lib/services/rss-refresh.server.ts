import { type Db, newId, nowIso } from "../db.server";
import type { PodcastRecord } from "../repositories/podcasts.server";
import { parsePodcastRss, type ParsedEpisode } from "./rss-parser.server";

const FEED_FETCH_TIMEOUT_MS = 10_000;
const MAX_FEED_BYTES = 2 * 1024 * 1024;
const MAX_REDIRECTS = 3;
const RSS_FETCH_ERROR = "Could not fetch that RSS feed.";
export const RSS_REFRESH_BATCH_SIZE = 25;

export type FeedFetcher = (feedUrl: string) => Promise<string>;

export interface RefreshPodcastFeedResult {
  podcastId: string;
  insertedEpisodes: number;
  skippedEpisodes: number;
}

export interface RefreshAllPodcastFeedsResult {
  attempted: number;
  refreshed: number;
  failed: number;
}

interface GuidRow {
  guid: string;
}

const podcastColumns = `id, user_id, feed_url, title, description, image_url, site_url, last_fetched_at, created_at`;

function parseIpv4(hostname: string): number[] | null {
  const parts = hostname.split(".");
  if (parts.length !== 4) return null;

  const octets = parts.map((part) => {
    if (!/^\d{1,3}$/.test(part)) return Number.NaN;
    const value = Number(part);
    return value >= 0 && value <= 255 ? value : Number.NaN;
  });

  return octets.every((octet) => Number.isInteger(octet)) ? octets : null;
}

function isBlockedIpLiteral(hostname: string): boolean {
  const lowerHostname = hostname.toLowerCase();
  const normalized = lowerHostname.startsWith("[") && lowerHostname.endsWith("]")
    ? lowerHostname.slice(1, -1)
    : lowerHostname;
  if (["localhost", "127.0.0.1", "::1", "0.0.0.0"].includes(normalized)) return true;
  if (normalized.includes("::ffff:")) return true;
  if (normalized.startsWith("fc") || normalized.startsWith("fd")) return true;
  if (/^fe[89ab]/.test(normalized)) return true;

  const ipv4 = parseIpv4(normalized);
  if (!ipv4) return false;

  const [first, second] = ipv4;
  return (
    first === 10 ||
    first === 127 ||
    (first === 172 && second >= 16 && second <= 31) ||
    (first === 192 && second === 168) ||
    (first === 169 && second === 254) ||
    (first === 0 && second === 0 && ipv4[2] === 0 && ipv4[3] === 0)
  );
}

function validateFeedUrl(feedUrl: string): URL {
  let url: URL;
  try {
    url = new URL(feedUrl);
  } catch {
    throw new Error(RSS_FETCH_ERROR);
  }

  if (url.protocol !== "https:" || url.username || url.password || isBlockedIpLiteral(url.hostname)) {
    throw new Error(RSS_FETCH_ERROR);
  }

  return url;
}

async function readResponseTextWithLimit(response: Response): Promise<string> {
  const contentLength = response.headers.get("content-length");
  if (contentLength) {
    const byteLength = Number(contentLength);
    if (Number.isFinite(byteLength) && byteLength > MAX_FEED_BYTES) {
      throw new Error("RSS feed is too large.");
    }
  }

  if (!response.body) {
    const text = await response.text();
    if (new TextEncoder().encode(text).byteLength > MAX_FEED_BYTES) {
      throw new Error("RSS feed is too large.");
    }
    return text;
  }

  const reader = response.body.getReader();
  const chunks: Uint8Array[] = [];
  let totalBytes = 0;

  while (true) {
    const { done, value } = await reader.read();
    if (done) break;
    if (!value) continue;

    totalBytes += value.byteLength;
    if (totalBytes > MAX_FEED_BYTES) {
      await reader.cancel();
      throw new Error("RSS feed is too large.");
    }
    chunks.push(value);
  }

  const bytes = new Uint8Array(totalBytes);
  let offset = 0;
  for (const chunk of chunks) {
    bytes.set(chunk, offset);
    offset += chunk.byteLength;
  }

  return new TextDecoder().decode(bytes);
}

function isRedirectStatus(status: number): boolean {
  return status >= 300 && status < 400;
}

function validateRedirectLocation(location: string | null, baseUrl: URL): URL {
  if (!location) throw new Error(RSS_FETCH_ERROR);
  return validateFeedUrl(new URL(location, baseUrl).toString());
}

export async function fetchFeedXml(feedUrl: string): Promise<string> {
  const controller = new AbortController();
  const timeoutId = setTimeout(() => controller.abort(), FEED_FETCH_TIMEOUT_MS);

  try {
    let currentUrl = validateFeedUrl(feedUrl);

    for (let redirectCount = 0; redirectCount <= MAX_REDIRECTS; redirectCount += 1) {
      const response = await fetch(currentUrl.toString(), {
        redirect: "manual",
        signal: controller.signal,
      });

      if (isRedirectStatus(response.status)) {
        if (redirectCount === MAX_REDIRECTS) throw new Error(RSS_FETCH_ERROR);
        currentUrl = validateRedirectLocation(response.headers.get("location"), currentUrl);
        continue;
      }

      if (!response.ok) throw new Error(RSS_FETCH_ERROR);
      return await readResponseTextWithLimit(response);
    }

    throw new Error(RSS_FETCH_ERROR);
  } catch (error) {
    if (error instanceof Error && error.message === "RSS feed is too large.") throw error;
    throw new Error(RSS_FETCH_ERROR);
  } finally {
    clearTimeout(timeoutId);
  }
}

export function mergeFeedEpisodes(
  existingGuids: ReadonlySet<string>,
  parsedEpisodes: ParsedEpisode[],
): ParsedEpisode[] {
  return parsedEpisodes.filter((episode) => !existingGuids.has(episode.guid));
}

async function getPodcastForRefresh(
  db: Db,
  userId: string,
  podcastId: string,
): Promise<PodcastRecord | null> {
  return await db
    .prepare(
      `SELECT ${podcastColumns}
       FROM podcasts
       WHERE id = ? AND user_id = ?
       LIMIT 1`,
    )
    .bind(podcastId, userId)
    .first<PodcastRecord>();
}

async function listExistingEpisodeGuids(db: Db, userId: string, podcastId: string): Promise<Set<string>> {
  const result = await db
    .prepare(
      `SELECT guid
       FROM episodes
       WHERE user_id = ? AND podcast_id = ?`,
    )
    .bind(userId, podcastId)
    .all<GuidRow>();

  return new Set((result.results ?? []).map((row) => row.guid));
}

async function updatePodcastMetadata(db: Db, podcastId: string, userId: string, parsed: ReturnType<typeof parsePodcastRss>): Promise<void> {
  await db
    .prepare(
      `UPDATE podcasts
       SET title = ?, description = ?, image_url = ?, site_url = ?, last_fetched_at = ?
       WHERE id = ? AND user_id = ?`,
    )
    .bind(parsed.title, parsed.description, parsed.imageUrl, parsed.siteUrl, nowIso(), podcastId, userId)
    .run();
}

async function insertNewEpisodes(
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
         ON CONFLICT(user_id, podcast_id, guid) DO NOTHING`,
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

export async function refreshPodcastFeed(
  db: Db,
  userId: string,
  podcastId: string,
  fetcher: FeedFetcher = fetchFeedXml,
): Promise<RefreshPodcastFeedResult> {
  const podcast = await getPodcastForRefresh(db, userId, podcastId);
  if (!podcast) throw new Error("Podcast not found");

  const xml = await fetcher(podcast.feed_url);
  const parsed = parsePodcastRss(xml);
  const existingGuids = await listExistingEpisodeGuids(db, userId, podcastId);
  const newEpisodes = mergeFeedEpisodes(existingGuids, parsed.episodes);

  await updatePodcastMetadata(db, podcastId, userId, parsed);
  await insertNewEpisodes(db, userId, podcastId, newEpisodes);

  return {
    podcastId,
    insertedEpisodes: newEpisodes.length,
    skippedEpisodes: parsed.episodes.length - newEpisodes.length,
  };
}

async function listPodcastsForRefreshBatch(db: Db, limit: number): Promise<PodcastRecord[]> {
  const result = await db
    .prepare(
      `SELECT ${podcastColumns}
       FROM podcasts
       ORDER BY last_fetched_at IS NOT NULL, last_fetched_at ASC, created_at ASC
       LIMIT ?`,
    )
    .bind(limit)
    .all<PodcastRecord>();

  return result.results ?? [];
}

export async function refreshAllPodcastFeeds(
  db: Db,
  options: { batchSize?: number; fetcher?: FeedFetcher } = {},
): Promise<RefreshAllPodcastFeedsResult> {
  const batchSize = Math.max(1, Math.min(options.batchSize ?? RSS_REFRESH_BATCH_SIZE, RSS_REFRESH_BATCH_SIZE));
  const podcasts = await listPodcastsForRefreshBatch(db, batchSize);
  let refreshed = 0;
  let failed = 0;

  for (const podcast of podcasts) {
    try {
      await refreshPodcastFeed(db, podcast.user_id, podcast.id, options.fetcher ?? fetchFeedXml);
      refreshed += 1;
    } catch (error) {
      failed += 1;
      console.error("Failed to refresh podcast RSS feed", {
        podcastId: podcast.id,
        userId: podcast.user_id,
        error: error instanceof Error ? error.message : String(error),
      });
    }
  }

  return { attempted: podcasts.length, refreshed, failed };
}
