import type {
  AnalysisProvider,
  AnalysisResult,
  ChatProvider,
  KnowledgeCard,
  TranscriptionProvider,
  TranscriptionResult,
} from "./types.server";

const provider = "mock";
const transcriptionModel = "mock-transcriber-v1";
const analysisModel = "mock-analyzer-v1";
const chatModel = "mock-chat-v1";

function stableSeconds(input: string): number {
  const total = Array.from(input).reduce((sum, char) => sum + char.charCodeAt(0), 0);
  return 60 + (total % 300);
}

export class MockTranscriptionProvider implements TranscriptionProvider {
  async transcribe(input: Parameters<TranscriptionProvider["transcribe"]>[0]): Promise<TranscriptionResult> {
    const sourceLabel = input.audioUrl ?? input.fileName ?? "uploaded-audio";
    const durationSeconds = stableSeconds(`${input.sourceTitle}:${sourceLabel}`);
    const text = `Mock transcript for ${input.sourceTitle}. Audio source: ${sourceLabel}.`;

    return {
      text,
      segments: [
        {
          startSeconds: 0,
          endSeconds: Math.min(30, durationSeconds),
          text: `Mock transcript for ${input.sourceTitle}.`,
        },
        {
          startSeconds: Math.min(30, durationSeconds),
          endSeconds: durationSeconds,
          text: `Audio source: ${sourceLabel}.`,
        },
      ],
      language: "en",
      durationSeconds,
      provider,
      model: transcriptionModel,
    };
  }
}

export class MockAnalysisProvider implements AnalysisProvider {
  async analyze(input: AnalysisProvider extends { analyze(input: infer I): Promise<AnalysisResult> } ? I : never): Promise<AnalysisResult> {
    const firstSegment = input.segments[0];
    const lastSegment = input.segments[input.segments.length - 1];
    const endSeconds = lastSegment?.endSeconds ?? 60;
    const summary = `Mock analysis summary for ${input.title}.`;
    const card: KnowledgeCard = {
      title: input.title,
      summary,
      keyPoints: ["This is a deterministic mock analysis.", "Replace mock providers with AI providers in production."],
      chapters: [
        {
          title: "Overview",
          startSeconds: firstSegment?.startSeconds ?? 0,
          endSeconds,
          summary,
        },
      ],
      quotes: [
        {
          text: input.transcript.slice(0, 160),
          startSeconds: firstSegment?.startSeconds ?? null,
        },
      ],
      entities: [{ name: input.title, type: "source" }],
      actionItems: [],
      glossary: [],
      suggestedQuestions: [`What are the key takeaways from ${input.title}?`],
      tags: ["mock", "podcast"],
    };

    return { card, provider, model: analysisModel };
  }
}

export class MockChatProvider implements ChatProvider {
  async answer(input: Parameters<ChatProvider["answer"]>[0]): Promise<Awaited<ReturnType<ChatProvider["answer"]>>> {
    return {
      answer: `Mock answer for "${input.question}" based on ${input.title}: ${input.analysis.summary}`,
      citations: [
        {
          startSeconds: input.analysis.quotes[0]?.startSeconds ?? null,
          text: input.analysis.quotes[0]?.text ?? input.transcript.slice(0, 160),
        },
      ],
      provider,
      model: chatModel,
    };
  }
}

export function createMockTranscriptionProvider(): TranscriptionProvider {
  return new MockTranscriptionProvider();
}

export function createMockAnalysisProvider(): AnalysisProvider {
  return new MockAnalysisProvider();
}

export function createMockChatProvider(): ChatProvider {
  return new MockChatProvider();
}
