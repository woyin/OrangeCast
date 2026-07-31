import { describe, expect, test } from "vitest";
import { sessionStorage } from "../app/lib/auth.server";
import type { AppEnv } from "../app/lib/env.server";
import { answerSourceQuestion, selectRelevantTranscriptChunks } from "../app/lib/services/qa.server";
import type { ChatProvider, KnowledgeCard, TranscriptSegment } from "../app/lib/providers/types.server";
import { action as sourceDetailAction, MAX_QUESTION_LENGTH } from "../app/routes/sources.$sourceType.$sourceId";

describe("selectRelevantTranscriptChunks", () => {
  test("returns chunks containing the requested word before unrelated chunks", () => {
    const segments: TranscriptSegment[] = [
      { startSeconds: 0, endSeconds: 10, text: "We discuss team rituals and onboarding." },
      { startSeconds: 10, endSeconds: 20, text: "The pricing page changed after customer research." },
      { startSeconds: 20, endSeconds: 30, text: "Next we cover infrastructure scaling." },
    ];

    const chunks = selectRelevantTranscriptChunks("pricing", segments);

    expect(chunks[0]?.text).toContain("pricing");
    expect(chunks.slice(1).map((chunk) => chunk.text)).toEqual([
      "We discuss team rituals and onboarding.",
      "Next we cover infrastructure scaling.",
    ]);
  });
});

const analysis: KnowledgeCard = {
  title: "Pricing Episode",
  summary: "A summary about pricing.",
  keyPoints: [],
  chapters: [],
  quotes: [],
  entities: [],
  actionItems: [],
  glossary: [],
  suggestedQuestions: [],
  tags: [],
};

describe("answerSourceQuestion", () => {
  test("passes relevant transcript chunks to the chat provider", async () => {
    const segments: TranscriptSegment[] = [
      { startSeconds: 0, endSeconds: 10, text: "Unrelated intro." },
      { startSeconds: 10, endSeconds: 20, text: "Pricing includes usage based tiers." },
    ];
    let transcriptSent = "";
    const provider: ChatProvider = {
      async answer(input) {
        transcriptSent = input.transcript;
        return {
          answer: `Answered: ${input.question}`,
          citations: [],
          provider: "test",
          model: "test-model",
        };
      },
    };

    const result = await answerSourceQuestion({
      provider,
      question: "What about pricing?",
      title: "Pricing Episode",
      transcriptText: "Unrelated intro. Pricing includes usage based tiers.",
      segments,
      analysis,
    });

    expect(transcriptSent.startsWith("[0:10] Pricing includes usage based tiers.")).toBe(true);
    expect(result.answer).toBe("Answered: What about pricing?");
  });

  test("bounds fallback transcript context when no transcript segments are available", async () => {
    let transcriptSent = "";
    const provider: ChatProvider = {
      async answer(input) {
        transcriptSent = input.transcript;
        return {
          answer: "bounded",
          citations: [],
          provider: "test",
          model: "test-model",
        };
      },
    };
    const longTranscript = "a".repeat(50);

    await answerSourceQuestion({
      provider,
      question: "What about pricing?",
      title: "Pricing Episode",
      transcriptText: longTranscript,
      segments: [],
      analysis,
      maxChars: 12,
    });

    expect(transcriptSent).toBe("a".repeat(12));
  });
});

function fakeEnvWithoutDbAccess(): AppEnv {
  return {
    DB: {
      prepare() {
        throw new Error("DB should not be queried for invalid questions");
      },
    } as unknown as D1Database,
    R2: {} as R2Bucket,
    PROCESSING_QUEUE: {} as Queue,
    APP_NAME: "CloudWisePod",
    SESSION_COOKIE_NAME: "cloudwise_session",
    SESSION_SECRET: "test-session-secret",
    UPLOAD_MAX_BYTES: "104857600",
    UPLOAD_MAX_SECONDS: "7200",
  };
}

async function authenticatedAskRequest(env: AppEnv, question: string) {
  const storage = sessionStorage(env);
  const session = await storage.getSession();
  session.set("userId", "user_1");
  const formData = new FormData();
  formData.set("intent", "ask");
  formData.set("question", question);

  return new Request("https://example.com/sources/upload/upload_1", {
    method: "POST",
    headers: { Cookie: await storage.commitSession(session) },
    body: formData,
  });
}

describe("source detail question action", () => {
  test("rejects questions longer than the maximum length before loading source data", async () => {
    const env = fakeEnvWithoutDbAccess();
    const response = await sourceDetailAction({
      request: await authenticatedAskRequest(env, "x".repeat(MAX_QUESTION_LENGTH + 1)),
      context: { env },
      params: { sourceType: "upload", sourceId: "upload_1" },
    } as never);
    const data = await response.json() as { error: string };

    expect(response.status).toBe(400);
    expect(data.error).toBe(`Question must be ${MAX_QUESTION_LENGTH} characters or fewer.`);
  });
});


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

function qaActionEnv(overrides: Partial<AppEnv> = {}) {
  const usageRecords: Array<{ operation: string; provider: string; model: string; inputUnits: number; outputUnits: number; estimatedCost: number }> = [];
  const artifacts = new Map<string, string>([
    ["transcripts/text.txt", "Pricing includes usage based tiers."],
    ["transcripts/segments.json", JSON.stringify([{ startSeconds: 0, endSeconds: 5, text: "Pricing includes usage based tiers." }])],
    ["analyses/card.json", JSON.stringify(analysis)],
  ]);

  const db = {
    prepare(sql: string) {
      return {
        bind(...values: unknown[]) {
          return {
            async first<T>() {
              if (sql.includes("FROM uploads")) {
                return {
                  id: "upload_1",
                  user_id: "user_1",
                  original_filename: "Pricing.mp3",
                  content_type: "audio/mpeg",
                  size_bytes: 123,
                  duration_seconds: 5,
                  r2_object_key: "uploads/audio.mp3",
                  processing_status: "processed",
                  created_at: "2026-06-14T00:00:00.000Z",
                } as T;
              }
              if (sql.includes("FROM transcripts")) {
                return {
                  id: "trn_1",
                  user_id: "user_1",
                  source_type: "upload",
                  source_id: "upload_1",
                  language: "en",
                  provider: "mock",
                  model: "mock-transcriber-v1",
                  text_r2_key: "transcripts/text.txt",
                  segments_r2_key: "transcripts/segments.json",
                  duration_seconds: 5,
                  created_at: "2026-06-14T00:00:00.000Z",
                } as T;
              }
              if (sql.includes("FROM analyses")) {
                return {
                  id: "ana_1",
                  user_id: "user_1",
                  source_type: "upload",
                  source_id: "upload_1",
                  provider: "mock",
                  model: "mock-analyzer-v1",
                  title: "Pricing Episode",
                  summary: "A summary about pricing.",
                  content_json_r2_key: "analyses/card.json",
                  markdown_r2_key: "analyses/card.md",
                  created_at: "2026-06-14T00:00:00.000Z",
                } as T;
              }
              if (sql.includes("FROM usage_records")) {
                const record = usageRecords.at(-1);
                return {
                  id: values[0] as string,
                  user_id: "user_1",
                  source_type: "upload",
                  source_id: "upload_1",
                  provider: record?.provider ?? "mock",
                  model: record?.model ?? "mock-chat-v1",
                  operation: record?.operation ?? "chat",
                  input_units: record?.inputUnits ?? 0,
                  output_units: record?.outputUnits ?? 0,
                  estimated_cost: record?.estimatedCost ?? 0,
                  created_at: "2026-06-14T00:00:00.000Z",
                } as T;
              }
              if (sql.includes("FROM settings")) {
                return null as T;
              }

              throw new Error(`Unexpected first SQL: ${sql}`);
            },
            async run() {
              if (sql.includes("INSERT INTO usage_records")) {
                usageRecords.push({
                  operation: values[6] as string,
                  provider: values[4] as string,
                  model: values[5] as string,
                  inputUnits: values[7] as number,
                  outputUnits: values[8] as number,
                  estimatedCost: values[9] as number,
                });
                return d1Result(1);
              }
              throw new Error(`Unexpected run SQL: ${sql}`);
            },
          };
        },
      };
    },
  } as unknown as D1Database;

  const env: AppEnv = {
    DB: db,
    R2: {
      async get(key: string) {
        const value = artifacts.get(key);
        return value === undefined ? null : ({ text: async () => value } as R2ObjectBody);
      },
    } as unknown as R2Bucket,
    PROCESSING_QUEUE: {} as Queue,
    APP_NAME: "CloudWisePod",
    SESSION_COOKIE_NAME: "cloudwise_session",
    SESSION_SECRET: "test-session-secret",
    UPLOAD_MAX_BYTES: "104857600",
    UPLOAD_MAX_SECONDS: "7200",
    ENVIRONMENT: "test",
    ...overrides,
  };

  return { env, usageRecords };
}

describe("source detail Q&A provider and usage integration", () => {
  test("successful answers persist chat usage", async () => {
    const { env, usageRecords } = qaActionEnv({ AI_PROVIDER: "mock" });

    const response = await sourceDetailAction({
      request: await authenticatedAskRequest(env, "What about pricing?"),
      context: { env },
      params: { sourceType: "upload", sourceId: "upload_1" },
    } as never);
    const data = await response.json() as { answer: { provider: string; model: string } };

    expect(response.status).toBe(200);
    expect(data.answer.provider).toBe("mock");
    expect(usageRecords).toEqual([
      expect.objectContaining({ operation: "chat", provider: "mock", model: "mock-chat-v1" }),
    ]);
    expect(usageRecords[0]?.inputUnits).toBeGreaterThan(0);
    expect(usageRecords[0]?.outputUnits).toBeGreaterThan(0);
  });

  test("uses configured provider selection instead of hardcoded mock", async () => {
    const { env } = qaActionEnv({ AI_PROVIDER: "unsupported-provider" });

    await expect(sourceDetailAction({
      request: await authenticatedAskRequest(env, "What about pricing?"),
      context: { env },
      params: { sourceType: "upload", sourceId: "upload_1" },
    } as never)).rejects.toMatchObject({ status: 500 });
  });
});
