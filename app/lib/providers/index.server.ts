import type { AppEnv } from "../env.server";
import { createMockAnalysisProvider, createMockChatProvider, createMockTranscriptionProvider } from "./mock.server";
import { OpenAIAnalysisProvider, OpenAIChatProvider, OpenAITranscriptionProvider } from "./openai.server";
import type { AnalysisProvider, ChatProvider, TranscriptionProvider } from "./types.server";

export interface Providers {
  transcription: TranscriptionProvider;
  analysis: AnalysisProvider;
  chat: ChatProvider;
}

/**
 * Per-user model overrides (from D1 settings table).
 * All fields are optional; any that are null/empty fall back to env vars → defaults.
 */
export interface ModelOverrides {
  transcriptionModel?: string | null;
  analysisModel?: string | null;
  chatModel?: string | null;
}

function setupError(message: string): Error & { status: number } {
  return Object.assign(new Error(message), { status: 500 });
}

function allowsMockDefault(env: AppEnv): boolean {
  const environment = (env.ENVIRONMENT ?? env.NODE_ENV ?? "development").toLowerCase();
  return env.ALLOW_MOCK_PROVIDER === "true" || environment === "development" || environment === "local" || environment === "test";
}

/**
 * Create provider instances.
 *
 * @param env Cloudflare env bindings
 * @param overrides Optional per-user model names (from D1 settings).
 *                  Takes priority over env vars and hardcoded defaults.
 */
export function getProviders(env: AppEnv, overrides?: ModelOverrides): Providers {
  const selected = env.AI_PROVIDER ?? (allowsMockDefault(env) ? "mock" : undefined);

  if (!selected) {
    throw setupError("AI provider is not configured");
  }

  if (selected === "openai") {
    return {
      transcription: new OpenAITranscriptionProvider(env, overrides?.transcriptionModel),
      analysis: new OpenAIAnalysisProvider(env, overrides?.analysisModel),
      chat: new OpenAIChatProvider(env, overrides?.chatModel),
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
