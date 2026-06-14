import JSZip from "jszip";
import { describe, expect, test } from "vitest";
import { sessionStorage } from "../app/lib/auth.server";
import { buildMarkdownZip, exportPathForSource, safeDownloadFilename } from "../app/lib/export/zip.server";
import type { AppEnv } from "../app/lib/env.server";
import { action as exportsAction, MAX_EXPORT_SOURCE_COUNT } from "../app/routes/exports";

describe("exportPathForSource", () => {
  test("returns a safe markdown path scoped by podcast title", () => {
    expect(exportPathForSource({ podcastTitle: "Acme Show", title: "Hello/World" })).toBe(
      "Acme Show/Hello-World.md",
    );
  });

  test("replaces filename-unsafe characters with dashes", () => {
    expect(exportPathForSource({ podcastTitle: "A:B*C?", title: 'One"Two<Three>Four|Five' })).toBe(
      "A-B-C-/One-Two-Three-Four-Five.md",
    );
  });

  test("prevents dot segment and Windows traversal path parts", () => {
    expect(exportPathForSource({ podcastTitle: "..", title: "." })).toBe("Untitled/Untitled.md");
    expect(exportPathForSource({ podcastTitle: "..\\windows", title: "..\\secret" })).toBe(
      "..-windows/..-secret.md",
    );
  });

  test("replaces backslashes and control characters", () => {
    expect(exportPathForSource({ podcastTitle: "Acme\\Show", title: "Hello\r\nWorld" })).toBe(
      "Acme-Show/Hello--World.md",
    );
  });

  test("falls back for empty and whitespace-only names", () => {
    expect(exportPathForSource({ podcastTitle: "", title: "   " })).toBe("Untitled/Untitled.md");
  });
});

describe("safeDownloadFilename", () => {
  test("removes path separators and control characters from attachment filenames", () => {
    expect(safeDownloadFilename("..\\evil\r\nname", "md", "download")).toBe("..-evil--name.md");
  });

  test("falls back for empty and dot-only attachment filenames", () => {
    expect(safeDownloadFilename("..", "zip", "download")).toBe("download.zip");
    expect(safeDownloadFilename("   ", "md", "download")).toBe("download.md");
  });
});

describe("buildMarkdownZip", () => {
  test("adds one markdown file per selected source", async () => {
    const zipBytes = await buildMarkdownZip([
      { podcastTitle: "Acme Show", title: "Hello/World", markdown: "# Hello\n" },
      { podcastTitle: "Uploads", title: "Meeting:Notes", markdown: "# Meeting\n" },
    ]);

    const zip = await JSZip.loadAsync(zipBytes);

    await expect(zip.file("Acme Show/Hello-World.md")?.async("string")).resolves.toBe("# Hello\n");
    await expect(zip.file("Uploads/Meeting-Notes.md")?.async("string")).resolves.toBe("# Meeting\n");
  });

  test("keeps duplicate sanitized paths as separate markdown files", async () => {
    const zipBytes = await buildMarkdownZip([
      { podcastTitle: "Acme", title: "Hello/World", markdown: "first" },
      { podcastTitle: "Acme", title: "Hello:World", markdown: "second" },
    ]);

    const zip = await JSZip.loadAsync(zipBytes);

    await expect(zip.file("Acme/Hello-World.md")?.async("string")).resolves.toBe("first");
    await expect(zip.file("Acme/Hello-World-2.md")?.async("string")).resolves.toBe("second");
  });
});

function d1Result(changes = 1): D1Result {
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

function createExportActionDb(options: { failInsert?: boolean; onInsert?: () => void } = {}) {
  const state = {
    inserted: [] as unknown[][],
  };

  const db = {
    prepare(sql: string) {
      return {
        bind(...values: unknown[]) {
          return {
            async all<T>() {
              if (sql.includes("a.source_type = 'episode'")) return { results: [] as T[] };
              if (sql.includes("a.source_type = 'upload'")) {
                return {
                  results: [
                    {
                      sourceType: "upload",
                      sourceId: "upload_1",
                      title: "Export Me",
                      podcastTitle: "Uploads",
                      markdownR2Key: "users/user_1/analyses/upload/upload_1/note.md",
                      contentJsonR2Key: "users/user_1/analyses/upload/upload_1/content.json",
                      transcriptTextR2Key: null,
                      analysisCreatedAt: "2026-06-15T00:00:00.000Z",
                    },
                  ] as T[],
                };
              }
              throw new Error(`Unexpected all SQL: ${sql}`);
            },
            async run() {
              if (sql.includes("INSERT INTO exports")) {
                options.onInsert?.();
                if (options.failInsert) throw new Error("insert failed");
                state.inserted.push(values);
                return d1Result();
              }
              throw new Error(`Unexpected run SQL: ${sql}`);
            },
            async first<T>() {
              if (sql.includes("FROM exports")) {
                const row = state.inserted.at(-1);
                if (!row) return null;
                return {
                  id: row[0],
                  user_id: row[1],
                  export_type: row[2],
                  r2_object_key: row[3],
                  status: row[4],
                  expires_at: row[5],
                  created_at: row[6],
                } as T;
              }
              throw new Error(`Unexpected first SQL: ${sql}`);
            },
          };
        },
      };
    },
  } as unknown as D1Database;

  return { db, state };
}

function createExportActionR2() {
  const state = {
    puts: [] as Array<{ key: string; value: unknown }>,
    deletes: [] as string[],
  };

  const r2 = {
    async get(key: string) {
      return {
        key,
        size: 11,
        async text() {
          return "# Export Me";
        },
      };
    },
    async put(key: string, value: unknown) {
      state.puts.push({ key, value });
      return null;
    },
    async delete(key: string) {
      state.deletes.push(key);
    },
  } as unknown as R2Bucket;

  return { r2, state };
}

async function authenticatedExportRequest(env: AppEnv, sourceIds = ["upload:upload_1"]) {
  const storage = sessionStorage(env);
  const session = await storage.getSession();
  session.set("userId", "user_1");
  const formData = new FormData();
  formData.set("intent", "create-zip");
  for (const sourceId of sourceIds) formData.append("source", sourceId);

  return new Request("https://example.com/exports", {
    method: "POST",
    headers: { Cookie: await storage.commitSession(session) },
    body: formData,
  });
}

function exportTestEnv(db: D1Database, r2: R2Bucket): AppEnv {
  return {
    DB: db,
    R2: r2,
    PROCESSING_QUEUE: {} as Queue,
    APP_NAME: "CloudWisePod",
    SESSION_COOKIE_NAME: "cloudwise_session",
    SESSION_SECRET: "test-session-secret",
    UPLOAD_MAX_BYTES: "104857600",
    UPLOAD_MAX_SECONDS: "7200",
  };
}

describe("exports action consistency", () => {

  test("rejects exports with more than the selected source count limit before querying sources", async () => {
    const db = {
      prepare() {
        throw new Error("DB should not be queried when selection exceeds the limit");
      },
    } as unknown as D1Database;
    const r2 = createExportActionR2();
    const env = exportTestEnv(db, r2.r2);
    const tooManySources = Array.from({ length: MAX_EXPORT_SOURCE_COUNT + 1 }, (_, index) => `upload:upload_${index}`);

    const response = await exportsAction({
      request: await authenticatedExportRequest(env, tooManySources),
      context: { env },
      params: {},
    } as never);
    const data = await response.json() as { error: string };

    expect(response.status).toBe(400);
    expect(data.error).toBe(`Select ${MAX_EXPORT_SOURCE_COUNT} or fewer sources per export.`);
    expect(r2.state.puts).toHaveLength(0);
  });
  test("creates the completed export record only after the ZIP is uploaded", async () => {
    const r2 = createExportActionR2();
    const db = createExportActionDb({
      onInsert() {
        expect(r2.state.puts).toHaveLength(1);
      },
    });
    const env = exportTestEnv(db.db, r2.r2);

    const response = await exportsAction({
      request: await authenticatedExportRequest(env),
      context: { env },
      params: {},
    } as never);

    expect(response.status).toBe(302);
    expect(r2.state.puts).toHaveLength(1);
    expect(db.state.inserted).toHaveLength(1);
    expect(db.state.inserted[0][3]).toBe(r2.state.puts[0].key);
  });

  test("deletes the uploaded ZIP if creating the completed export record fails", async () => {
    const r2 = createExportActionR2();
    const db = createExportActionDb({ failInsert: true });
    const env = exportTestEnv(db.db, r2.r2);

    await expect(
      exportsAction({
        request: await authenticatedExportRequest(env),
        context: { env },
        params: {},
      } as never),
    ).rejects.toThrow("insert failed");

    expect(r2.state.puts).toHaveLength(1);
    expect(r2.state.deletes).toEqual([r2.state.puts[0].key]);
    expect(db.state.inserted).toHaveLength(0);
  });
});
