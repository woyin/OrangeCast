import { describe, expect, test } from "vitest";
import {
  getSafeHttpUrl,
  safeParseKnowledgeCard,
  safeParseTranscriptSegments,
} from "../app/routes/sources.$sourceType.$sourceId";

describe("source detail artifact hardening", () => {
  test("returns null for malformed knowledge card JSON instead of throwing", () => {
    expect(safeParseKnowledgeCard("not-json")).toBeNull();
  });

  test("normalizes missing knowledge card arrays to empty arrays", () => {
    expect(safeParseKnowledgeCard(JSON.stringify({ title: "Card", summary: "Summary" }))).toEqual({
      title: "Card",
      summary: "Summary",
      keyPoints: [],
      chapters: [],
      quotes: [],
      entities: [],
      actionItems: [],
      glossary: [],
      suggestedQuestions: [],
      tags: [],
    });
  });

  test("filters invalid transcript segment entries", () => {
    expect(
      safeParseTranscriptSegments(
        JSON.stringify([
          { startSeconds: 0, endSeconds: 4.4, text: "Valid" },
          { startSeconds: "bad", endSeconds: 10, text: "Invalid" },
          { startSeconds: 10, endSeconds: 12, text: "" },
        ]),
      ),
    ).toEqual([{ startSeconds: 0, endSeconds: 4.4, text: "Valid" }]);
  });

  test("returns null for malformed or non-array transcript segments", () => {
    expect(safeParseTranscriptSegments("not-json")).toBeNull();
    expect(safeParseTranscriptSegments(JSON.stringify({ startSeconds: 0 }))).toBeNull();
  });
});

describe("source detail audio URL hardening", () => {
  test("allows only http and https audio links", () => {
    expect(getSafeHttpUrl("https://example.com/audio.mp3")).toBe("https://example.com/audio.mp3");
    expect(getSafeHttpUrl("http://example.com/audio.mp3")).toBe("http://example.com/audio.mp3");
    expect(getSafeHttpUrl("javascript:alert(1)")).toBeNull();
    expect(getSafeHttpUrl("ftp://example.com/audio.mp3")).toBeNull();
    expect(getSafeHttpUrl("not a url")).toBeNull();
  });
});
