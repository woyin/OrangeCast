import { describe, expect, it, vi } from "vitest";
import type { Db } from "../app/lib/db.server";
import type { ParsedEpisode } from "../app/lib/services/rss-parser.server";
import { mergeFeedEpisodes, refreshPodcastFeed } from "../app/lib/services/rss-refresh.server";

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

function parsedEpisode(overrides: Partial<ParsedEpisode> & { guid: string }): ParsedEpisode {
  return {
    guid: overrides.guid,
    title: overrides.title ?? `Episode ${overrides.guid}`,
    description: overrides.description ?? null,
    audioUrl: overrides.audioUrl ?? `https://example.com/${overrides.guid}.mp3`,
    durationSeconds: overrides.durationSeconds ?? null,
    publishedAt: overrides.publishedAt ?? null,
  };
}

const feedXml = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0">
  <channel>
    <title>Cloud Feed</title>
    <description>Fresh cloud thinking</description>
    <link>https://example.com</link>
    <item>
      <guid>existing-guid</guid>
      <title>Already Stored</title>
      <enclosure url="https://example.com/existing.mp3" type="audio/mpeg" />
    </item>
    <item>
      <guid>new-guid</guid>
      <title>New Episode</title>
      <description>New notes</description>
      <pubDate>Mon, 01 Jan 2024 12:00:00 GMT</pubDate>
      <enclosure url="https://example.com/new.mp3" type="audio/mpeg" />
    </item>
  </channel>
</rss>`;

function createRefreshDb() {
  const state = {
    podcast: {
      id: "pod_1",
      user_id: "user_1",
      feed_url: "https://example.com/feed.xml",
      title: "Old Title",
      description: null as string | null,
      image_url: null as string | null,
      site_url: null as string | null,
      last_fetched_at: null as string | null,
      created_at: "2026-06-14T00:00:00.000Z",
    },
    episodes: [
      {
        id: "epi_existing",
        user_id: "user_1",
        podcast_id: "pod_1",
        guid: "existing-guid",
        title: "Already Stored",
        description: null as string | null,
        audio_url: "https://example.com/existing.mp3",
        duration_seconds: null as number | null,
        published_at: null as string | null,
        processing_status: "unprocessed",
        created_at: "2026-06-14T00:00:00.000Z",
      },
    ],
    processingJobs: [] as unknown[],
  };

  const db = {
    prepare(sql: string) {
      return {
        bind(...values: unknown[]) {
          return {
            async first<T>() {
              if (sql.includes("FROM podcasts") && sql.includes("WHERE id = ?") && sql.includes("user_id = ?")) {
                if (values[0] === "pod_1" && values[1] === "user_1") return state.podcast as T;
                return null;
              }

              throw new Error(`Unexpected first SQL: ${sql}`);
            },
            async all<T>() {
              if (sql.includes("SELECT guid") && sql.includes("FROM episodes")) {
                return { results: state.episodes.map((episode) => ({ guid: episode.guid })) } as T;
              }

              throw new Error(`Unexpected all SQL: ${sql}`);
            },
            async run() {
              if (sql.includes("UPDATE podcasts") && sql.includes("last_fetched_at")) {
                state.podcast.title = values[0] as string;
                state.podcast.description = values[1] as string | null;
                state.podcast.image_url = values[2] as string | null;
                state.podcast.site_url = values[3] as string | null;
                state.podcast.last_fetched_at = values[4] as string;
                return d1Result(1);
              }

              if (sql.includes("INSERT INTO episodes")) {
                state.episodes.push({
                  id: values[0] as string,
                  user_id: values[1] as string,
                  podcast_id: values[2] as string,
                  guid: values[3] as string,
                  title: values[4] as string,
                  description: values[5] as string | null,
                  audio_url: values[6] as string,
                  duration_seconds: values[7] as number | null,
                  published_at: values[8] as string | null,
                  processing_status: "unprocessed",
                  created_at: values[9] as string,
                });
                return d1Result(1);
              }

              if (sql.includes("processing_jobs")) {
                state.processingJobs.push(values);
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

describe("mergeFeedEpisodes", () => {
  it("returns only parsed episodes whose GUIDs are not already stored", () => {
    const parsedEpisodes = [
      parsedEpisode({ guid: "existing-guid" }),
      parsedEpisode({ guid: "new-guid" }),
      parsedEpisode({ guid: "also-new-guid" }),
    ];

    const result = mergeFeedEpisodes(new Set(["existing-guid"]), parsedEpisodes);

    expect(result.map((episode) => episode.guid)).toEqual(["new-guid", "also-new-guid"]);
  });

  it("is pure and does not enqueue processing jobs", () => {
    const enqueueProcessing = vi.fn();
    const existingGuids = new Set(["existing-guid"]);
    const parsedEpisodes = [parsedEpisode({ guid: "existing-guid" }), parsedEpisode({ guid: "new-guid" })];

    const result = mergeFeedEpisodes(existingGuids, parsedEpisodes);

    expect(result).toHaveLength(1);
    expect(enqueueProcessing).not.toHaveBeenCalled();
    expect(existingGuids).toEqual(new Set(["existing-guid"]));
  });
});

describe("refreshPodcastFeed", () => {
  it("fetches, parses, inserts only new episodes, updates last_fetched_at, and never creates processing jobs", async () => {
    const { db, state } = createRefreshDb();
    const fetcher = vi.fn(async () => feedXml);

    const result = await refreshPodcastFeed(db, "user_1", "pod_1", fetcher);

    expect(fetcher).toHaveBeenCalledWith("https://example.com/feed.xml");
    expect(result).toMatchObject({ podcastId: "pod_1", insertedEpisodes: 1, skippedEpisodes: 1 });
    expect(state.podcast.title).toBe("Cloud Feed");
    expect(state.podcast.last_fetched_at).toEqual(expect.any(String));
    expect(state.episodes.map((episode) => episode.guid)).toEqual(["existing-guid", "new-guid"]);
    expect(state.episodes.at(-1)).toMatchObject({
      user_id: "user_1",
      podcast_id: "pod_1",
      guid: "new-guid",
      title: "New Episode",
      processing_status: "unprocessed",
    });
    expect(state.processingJobs).toEqual([]);
  });
});
