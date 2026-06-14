import { describe, expect, test } from "vitest";
import { sessionStorage } from "../app/lib/auth.server";
import type { Db } from "../app/lib/db.server";
import type { AppEnv } from "../app/lib/env.server";
import { scoreSearchResult, searchUserContent, type SearchResult } from "../app/lib/search/index.server";
import { loader as searchLoader } from "../app/routes/search";

describe("scoreSearchResult", () => {
  test("scores a title match higher than a summary match", () => {
    const titleScore = scoreSearchResult("cloud architecture", {
      title: "Cloud Architecture Patterns",
      summary: "A discussion about podcasts.",
    });
    const summaryScore = scoreSearchResult("cloud architecture", {
      title: "Podcast Notes",
      summary: "A discussion about cloud architecture patterns.",
    });

    expect(titleScore).toBeGreaterThan(summaryScore);
    expect(summaryScore).toBeGreaterThan(0);
  });

  test("scores no match as 0", () => {
    expect(
      scoreSearchResult("cloud architecture", {
        title: "Podcast Notes",
        summary: "A discussion about personal productivity.",
      }),
    ).toBe(0);
  });
});

function createSearchDb() {
  const executed: Array<{ sql: string; values: unknown[] }> = [];
  const db = {
    prepare(sql: string) {
      return {
        bind(...values: unknown[]) {
          executed.push({ sql, values });
          return {
            async all<T>() {
              if (sql.includes("FROM episodes")) {
                return {
                  results: values[0] === "user_1"
                    ? [
                        {
                          id: "episode_1",
                          title: "Cloud Cost Patterns",
                          description: "An episode for the owner.",
                        },
                      ]
                    : [],
                } as { results: T[] };
              }

              if (sql.includes("FROM uploads")) {
                return {
                  results: values[0] === "user_1"
                    ? [
                        {
                          id: "upload_1",
                          original_filename: "cloud-costs.mp3",
                        },
                      ]
                    : [],
                } as { results: T[] };
              }

              if (sql.includes("FROM analyses")) {
                return {
                  results: values[0] === "user_1"
                    ? [
                        {
                          id: "analysis_1",
                          source_type: "upload",
                          source_id: "upload_1",
                          title: "Cost Optimization",
                          summary: "Cloud architecture cost notes.",
                        },
                      ]
                    : [],
                } as { results: T[] };
              }

              throw new Error(`Unexpected SQL: ${sql}`);
            },
          };
        },
      };
    },
  } as unknown as Db;

  return { db, executed };
}

describe("searchUserContent", () => {
  test("searches only content owned by the requested user", async () => {
    const { db, executed } = createSearchDb();

    const results = await searchUserContent(db, "user_1", "cloud");

    expect(results.map((result) => result.sourceId)).toEqual(["episode_1", "upload_1", "upload_1"]);
    expect(executed).toHaveLength(3);
    expect(executed.every((query) => query.values[0] === "user_1")).toBe(true);
  });

  test("returns no results for blank queries", async () => {
    const { db, executed } = createSearchDb();

    await expect(searchUserContent(db, "user_1", "   ")).resolves.toEqual([]);
    expect(executed).toHaveLength(0);
  });
});

function fakeSearchEnv(db: Db): AppEnv {
  return {
    DB: db as D1Database,
    R2: {} as R2Bucket,
    PROCESSING_QUEUE: {} as Queue,
    APP_NAME: "CloudWisePod",
    SESSION_COOKIE_NAME: "cloudwise_session",
    SESSION_SECRET: "test-session-secret",
    UPLOAD_MAX_BYTES: "104857600",
    UPLOAD_MAX_SECONDS: "7200",
  };
}

async function authenticatedRequest(url: string, env: AppEnv, userId = "user_1") {
  const storage = sessionStorage(env);
  const session = await storage.getSession();
  session.set("userId", userId);
  return new Request(url, {
    headers: { Cookie: await storage.commitSession(session) },
  });
}

describe("search route loader", () => {
  test("requires authentication", async () => {
    const { db } = createSearchDb();

    await expect(
      searchLoader({
        request: new Request("https://example.com/search?q=cloud"),
        context: { env: fakeSearchEnv(db) },
        params: {},
      } as never),
    ).rejects.toMatchObject({ status: 302 });
  });

  test("uses the q query parameter and returns the transcript indexing disclosure", async () => {
    const { db, executed } = createSearchDb();
    const env = fakeSearchEnv(db);
    const response = await searchLoader({
      request: await authenticatedRequest("https://example.com/search?q=cloud", env),
      context: { env },
      params: {},
    } as never);
    const data = await response.json() as { query: string; results: SearchResult[]; transcriptBodySearchDisclosure: string };

    expect(data.query).toBe("cloud");
    expect(data.results.length).toBeGreaterThan(0);
    expect(data.transcriptBodySearchDisclosure).toBe(
      "Transcript body search is enabled after transcript indexing is configured",
    );
    expect(executed.every((query) => query.values[0] === "user_1")).toBe(true);
  });
});
