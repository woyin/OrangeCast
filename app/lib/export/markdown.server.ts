import type { KnowledgeCard } from "../providers/types.server";
import type { SourceType } from "../queue/messages.server";

export interface KnowledgeCardMarkdownMetadata {
  sourceTitle: string;
  sourceType: SourceType;
  sourceId: string;
  podcastTitle: string | null;
  publishedAt: string | null;
  processedAt: string;
  durationSeconds: number | null;
  createdAt: string;
}

export interface KnowledgeCardMarkdownOptions {
  includeTranscriptAppendix?: boolean;
  transcriptText?: string;
}

function yamlString(value: string): string {
  return JSON.stringify(value);
}

function yamlList(values: string[]): string {
  if (values.length === 0) return "[]";
  return values.map((value) => `\n  - ${yamlString(value)}`).join("");
}

function tagToken(tag: string): string {
  return `#${tag.replace(/^#/, "").trim().replace(/\s+/g, "-")}`;
}

function formatTimestamp(seconds: number | null): string {
  if (seconds == null || !Number.isFinite(seconds)) return "";
  const total = Math.max(0, Math.floor(seconds));
  const minutes = Math.floor(total / 60);
  const remainingSeconds = total % 60;
  return ` [${minutes}:${remainingSeconds.toString().padStart(2, "0")}]`;
}

function pushListSection(lines: string[], heading: string, items: string[], fallback = "None.") {
  lines.push(`## ${heading}`, "");
  if (items.length === 0) {
    lines.push(fallback, "");
    return;
  }
  for (const item of items) lines.push(`- ${item}`);
  lines.push("");
}

export function renderKnowledgeCardMarkdown(
  card: KnowledgeCard,
  metadata: KnowledgeCardMarkdownMetadata,
  options: KnowledgeCardMarkdownOptions = {},
): string {
  const tags = Array.from(new Set(card.tags.map((tag) => tag.trim()).filter(Boolean)));
  const entities = Array.from(new Set(card.entities.map((entity) => entity.name.trim()).filter(Boolean)));
  const durationLabel =
    metadata.durationSeconds != null
      ? `${Math.round(metadata.durationSeconds / 60)} min`
      : null;

  const lines: string[] = [
    "---",
    `title: ${yamlString(card.title)}`,
    `source_title: ${yamlString(metadata.sourceTitle)}`,
    `source_type: ${yamlString(metadata.sourceType)}`,
    `source_id: ${yamlString(metadata.sourceId)}`,
    `podcast: ${yamlString(metadata.podcastTitle ?? "")}`,
    `published_at: ${yamlString(metadata.publishedAt ?? "")}`,
    `processed_at: ${yamlString(metadata.processedAt)}`,
    ...(durationLabel ? [`duration: ${yamlString(durationLabel)}`] : []),
    `tags:${yamlList(tags)}`,
    `entities:${yamlList(entities)}`,
    "---",
    "",
    `# ${card.title}`,
    "",
  ];

  if (tags.length > 0) lines.push(tags.map(tagToken).join(" "), "");
  if (entities.length > 0) lines.push(entities.map((entity) => `[[${entity}]]`).join(" "), "");

  lines.push("## Summary", "", card.summary || "No summary available.", "");

  pushListSection(lines, "Key Points", card.keyPoints);

  if (card.chapters.length > 0) {
    lines.push("## Chapters", "");
    for (const chapter of card.chapters) {
      lines.push(`- **${chapter.title}**${formatTimestamp(chapter.startSeconds)} — ${chapter.summary}`);
    }
    lines.push("");
  }

  if (card.quotes.length > 0) {
    lines.push("## Quotes", "");
    for (const quote of card.quotes) lines.push(`- "${quote.text}"${formatTimestamp(quote.startSeconds)}`);
    lines.push("");
  }

  pushListSection(lines, "Action Items", card.actionItems);

  if (card.glossary.length > 0) {
    lines.push("## Glossary", "");
    for (const entry of card.glossary) lines.push(`- **${entry.term}:** ${entry.definition}`);
    lines.push("");
  }

  pushListSection(lines, "Suggested Questions", card.suggestedQuestions);

  if (options.includeTranscriptAppendix) {
    lines.push("## Transcript", "", options.transcriptText?.trim() || "Transcript unavailable.", "");
  }

  return `${lines.join("\n").trimEnd()}\n`;
}
