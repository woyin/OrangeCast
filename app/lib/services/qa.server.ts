import type { ChatProvider, KnowledgeCard, TranscriptSegment } from "../providers/types.server";

function tokenize(input: string): string[] {
  return Array.from(new Set(input.toLowerCase().match(/[a-z0-9]+/g) ?? []));
}

function segmentScore(questionTokens: string[], segment: TranscriptSegment): number {
  if (questionTokens.length === 0) return 0;
  const textTokens = new Set(tokenize(segment.text));
  return questionTokens.reduce((score, token) => score + (textTokens.has(token) ? 1 : 0), 0);
}

function formatTime(seconds: number): string {
  const totalSeconds = Math.max(0, Math.round(seconds));
  const minutes = Math.floor(totalSeconds / 60);
  const remainingSeconds = totalSeconds % 60;
  return `${minutes}:${remainingSeconds.toString().padStart(2, "0")}`;
}

function formatChunk(segment: TranscriptSegment): string {
  return `[${formatTime(segment.startSeconds)}] ${segment.text}`;
}

export function selectRelevantTranscriptChunks(
  question: string,
  segments: TranscriptSegment[],
  maxChars = 12000,
): TranscriptSegment[] {
  const questionTokens = tokenize(question);
  let usedChars = 0;

  return segments
    .map((segment, index) => ({ segment, index, score: segmentScore(questionTokens, segment) }))
    .sort((left, right) => right.score - left.score || left.index - right.index)
    .flatMap(({ segment }): TranscriptSegment[] => {
      const nextChars = segment.text.length;
      if (usedChars > 0 && usedChars + nextChars > maxChars) return [];
      usedChars += nextChars;
      return [segment];
    });
}

export async function answerSourceQuestion(input: {
  provider: ChatProvider;
  question: string;
  title: string;
  transcriptText: string;
  segments: TranscriptSegment[];
  analysis: KnowledgeCard;
}): Promise<Awaited<ReturnType<ChatProvider["answer"]>>> {
  const selectedChunks = selectRelevantTranscriptChunks(input.question, input.segments);
  const transcript = selectedChunks.length > 0
    ? selectedChunks.map(formatChunk).join("\n\n")
    : input.transcriptText;

  return await input.provider.answer({
    question: input.question,
    title: input.title,
    transcript,
    analysis: input.analysis,
  });
}
