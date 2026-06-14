import type { AppEnv } from "../env.server";
import { createMockAnalysisProvider, createMockChatProvider, createMockTranscriptionProvider } from "./mock.server";
import { OpenAIAnalysisProvider, OpenAIChatProvider, OpenAITranscriptionProvider } from "./openai.server";
import type { AnalysisProvider, ChatProvider, TranscriptionProvider } from "./types.server";

export interface Providers {
  transcription: TranscriptionProvider;
  analysis: AnalysisProvider;
  chat: ChatProvider;
}

function setupError(message: string): Error & { status: number } {
  return Object.assign(new Error(message), { status: 500 });
}

function allowsMockDefault(env: AppEnv): boolean {
  const environment = (env.ENVIRONMENT ?? env.NODE_ENV ?? "development").toLowerCase();
  return env.ALLOW_MOCK_PROVIDER === "true" || environment === "development" || environment === "local" || environment === "test";
}

export function getProviders(env: AppEnv): Providers {
  const selected = env.AI_PROVIDER ?? (allowsMockDefault(env) ? "mock" : undefined);

  if (!selected) {
    throw setupError("AI provider is not configured");
  }

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

  throw setupError("Unsupported AI provider configuration");
}
