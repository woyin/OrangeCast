import { afterEach, describe, expect, test, vi } from "vitest";
import type { AppEnv } from "../app/lib/env.server";
import { OpenAIAnalysisProvider } from "../app/lib/providers/openai.server";
import type { KnowledgeCard } from "../app/lib/providers/types.server";

function openAiEnv(): AppEnv {
  return {
    DB: {} as D1Database,
    R2: {} as R2Bucket,
    PROCESSING_QUEUE: {} as Queue,
    APP_NAME: "CloudWisePod",
    SESSION_COOKIE_NAME: "cloudwise_session",
    SESSION_SECRET: "test-session-secret",
    UPLOAD_MAX_BYTES: "104857600",
    UPLOAD_MAX_SECONDS: "7200",
    AI_PROVIDER: "openai",
    OPENAI_API_KEY: "test-openai-key",
  };
}

function responsesApiJson(content: string): Response {
  return new Response(
    JSON.stringify({
      output: [
        {
          content: [
            {
              type: "output_text",
              text: content,
            },
          ],
        },
      ],
    }),
    { status: 200, headers: { "content-type": "application/json" } },
  );
}

const validCard: KnowledgeCard = {
  title: "Cloud Cost Review",
  summary: "A concise summary of cloud cost review practices.",
  keyPoints: ["Review idle resources", "Tag workloads"],
  chapters: [
    {
      title: "Overview",
      startSeconds: 0,
      endSeconds: 60,
      summary: "The conversation introduces cost review practices.",
    },
  ],
  quotes: [
    {
      text: "We should review idle resources every week.",
      startSeconds: 12,
    },
  ],
  entities: [{ name: "CloudWise", type: "product" }],
  actionItems: ["Audit idle resources"],
  glossary: [{ term: "FinOps", definition: "Cloud financial operations practice." }],
  suggestedQuestions: ["Which resources should we audit first?"],
  tags: ["cloud", "cost"],
};

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("OpenAI analysis provider contract", () => {
  test("rejects invalid JSON with a user-safe error", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => responsesApiJson("not-json")));
    const provider = new OpenAIAnalysisProvider(openAiEnv());

    await expect(
      provider.analyze({
        title: "Cloud Cost Review",
        transcript: "We should review idle resources every week.",
        segments: [{ startSeconds: 0, endSeconds: 60, text: "We should review idle resources every week." }],
      }),
    ).rejects.toThrow(new Error("Analysis provider returned invalid knowledge card JSON"));
  });

  test("accepts JSON matching KnowledgeCard", async () => {
    const fetchMock = vi.fn(async () => responsesApiJson(JSON.stringify(validCard)));
    vi.stubGlobal("fetch", fetchMock);
    const provider = new OpenAIAnalysisProvider(openAiEnv());

    const result = await provider.analyze({
      title: "Cloud Cost Review",
      transcript: "We should review idle resources every week.",
      segments: [{ startSeconds: 0, endSeconds: 60, text: "We should review idle resources every week." }],
    });

    expect(result.card).toEqual(validCard);
    expect(result.provider).toBe("openai");
    expect(result.model).toBe("gpt-4.1-mini");
    expect(fetchMock).toHaveBeenCalledWith(
      "https://api.openai.com/v1/responses",
      expect.objectContaining({ method: "POST" }),
    );
  });
});
