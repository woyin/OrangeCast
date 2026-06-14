import { describe, expect, test } from "vitest";
import { answerSourceQuestion, selectRelevantTranscriptChunks } from "../app/lib/services/qa.server";
import type { ChatProvider, KnowledgeCard, TranscriptSegment } from "../app/lib/providers/types.server";

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

describe("answerSourceQuestion", () => {
  test("passes relevant transcript chunks to the chat provider", async () => {
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
});
