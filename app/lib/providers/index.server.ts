import type { AppEnv } from "../env.server";
import { createMockAnalysisProvider, createMockChatProvider, createMockTranscriptionProvider } from "./mock.server";
import { OpenAIAnalysisProvider, OpenAIChatProvider, OpenAITranscriptionProvider } from "./openai.server";
import type { AnalysisProvider, ChatProvider, TranscriptionProvider } from "./types.server";

export interface Providers {
  transcription: TranscriptionProvider;
  analysis: AnalysisProvider;
  chat: ChatProvider;
}

export function getProviders(env: AppEnv): Providers {
  const selected = env.AI_PROVIDER ?? "mock";

  if (selected === "openai") {
    return {
      transcription: new OpenAITranscriptionProvider(env),
      analysis: new OpenAIAnalysisProvider(env),
      chat: new OpenAIChatProvider(env),
    };
  }

  if (selected === "mock") {
    return {
      transcription: createMockTranscriptionProvider(),
      analysis: createMockAnalysisProvider(),
      chat: createMockChatProvider(),
    };
  }

  throw new Response("Unsupported AI provider configuration", { status: 500 });
}
