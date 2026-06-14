import { describe, expect, test } from "vitest";
import { renderKnowledgeCardMarkdown } from "../app/lib/export/markdown.server";
import type { KnowledgeCard } from "../app/lib/providers/types.server";

const card: KnowledgeCard = {
  title: "Better Thinking",
  summary: "A concise summary about thinking more clearly.",
  keyPoints: ["Question assumptions.", "Use better mental models."],
  chapters: [],
  quotes: [],
  entities: [{ name: "Naval", type: "person" }],
  actionItems: [],
  glossary: [],
  suggestedQuestions: [],
  tags: ["podcast"],
};

describe("renderKnowledgeCardMarkdown", () => {
  test("renders frontmatter, tags, entity links, and core sections", () => {
    const markdown = renderKnowledgeCardMarkdown(
      card,
      {
        sourceTitle: "Better Thinking",
        sourceType: "episode",
        sourceId: "episode_1",
        createdAt: "2026-06-14T00:00:00.000Z",
      },
      { includeTranscriptAppendix: false },
    );

    expect(markdown).toContain("---\n");
    expect(markdown).toContain('title: "Better Thinking"');
    expect(markdown).toContain("#podcast");
    expect(markdown).toContain("[[Naval]]");
    expect(markdown).toContain("## Summary");
    expect(markdown).toContain("## Key Points");
    expect(markdown).not.toContain("## Transcript");
  });
});
