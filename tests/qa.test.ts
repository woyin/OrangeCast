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
