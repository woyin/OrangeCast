export interface TranscriptSegment {
  startSeconds: number;
  endSeconds: number;
  text: string;
}

export interface TranscriptionResult {
  text: string;
  segments: TranscriptSegment[];
  language: string | null;
  durationSeconds: number | null;
  provider: string;
  model: string;
}

export interface KnowledgeCard {
  title: string;
  summary: string;
  keyPoints: string[];
  chapters: { title: string; startSeconds: number; endSeconds: number; summary: string }[];
  quotes: { text: string; startSeconds: number | null }[];
  entities: { name: string; type: string }[];
  actionItems: string[];
  glossary: { term: string; definition: string }[];
  suggestedQuestions: string[];
  tags: string[];
}

export interface AnalysisResult {
  card: KnowledgeCard;
  provider: string;
  model: string;
}

export interface TranscriptionProvider {
  transcribe(input: { audioUrl: string; sourceTitle: string }): Promise<TranscriptionResult>;
}

export interface AnalysisProvider {
  analyze(input: {
    title: string;
    transcript: string;
    segments: TranscriptSegment[];
  }): Promise<AnalysisResult>;
}

export interface ChatProvider {
  answer(input: {
    question: string;
    title: string;
    transcript: string;
    analysis: KnowledgeCard;
  }): Promise<{
    answer: string;
    citations: { startSeconds: number | null; text: string }[];
    provider: string;
    model: string;
  }>;
}
