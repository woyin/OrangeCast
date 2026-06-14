import type { ChatProvider, KnowledgeCard, TranscriptSegment } from "../providers/types.server";

export const QA_CONTEXT_MAX_CHARS = 12000;

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
  maxChars = QA_CONTEXT_MAX_CHARS,
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

export interface QuestionAnswerUsageEstimate {
  inputUnits: number;
  outputUnits: number;
  estimatedCost: number;
}

export type AnswerSourceQuestionResult = Awaited<ReturnType<ChatProvider["answer"]>> & {
  usage: QuestionAnswerUsageEstimate;
};

export function estimateQuestionAnswerUsage(input: {
  question: string;
  transcriptContext: string;
  answer: string;
}): QuestionAnswerUsageEstimate {
  const inputUnits = Math.ceil((input.question.length + input.transcriptContext.length) / 4);
  const outputUnits = Math.ceil(input.answer.length / 4);
  return {
    inputUnits,
    outputUnits,
    estimatedCost: inputUnits * 0.00000015 + outputUnits * 0.0000006,
  };
}

export async function answerSourceQuestion(input: {
  provider: ChatProvider;
  question: string;
  title: string;
  transcriptText: string;
  segments: TranscriptSegment[];
  analysis: KnowledgeCard;
  maxChars?: number;
}): Promise<AnswerSourceQuestionResult> {
  const maxChars = input.maxChars ?? QA_CONTEXT_MAX_CHARS;
  const selectedChunks = selectRelevantTranscriptChunks(input.question, input.segments, maxChars);
  const transcript = (selectedChunks.length > 0
    ? selectedChunks.map(formatChunk).join("\n\n")
    : input.transcriptText).slice(0, maxChars);

  const answer = await input.provider.answer({
    question: input.question,
    title: input.title,
    transcript,
    analysis: input.analysis,
  });
  return {
    ...answer,
    usage: estimateQuestionAnswerUsage({
      question: input.question,
      transcriptContext: transcript,
      answer: answer.answer,
    }),
  };
}
