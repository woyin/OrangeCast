import { z } from "zod";
import type {
  AnalysisProvider,
  AnalysisResult,
  ChatProvider,
  KnowledgeCard,
  TranscriptionProvider,
  TranscriptionResult,
} from "./types.server";
import type { AppEnv } from "../env.server";

const provider = "openai";
const transcriptionModel = "gpt-4o-mini-transcribe";
const analysisModel = "gpt-4.1-mini";
const chatModel = "gpt-4.1-mini";
const responsesEndpoint = "https://api.openai.com/v1/responses";
const transcriptionsEndpoint = "https://api.openai.com/v1/audio/transcriptions";

const knowledgeCardSchema = z
  .object({
    title: z.string(),
    summary: z.string(),
    keyPoints: z.array(z.string()),
    chapters: z.array(
      z
        .object({
          title: z.string(),
          startSeconds: z.number(),
          endSeconds: z.number(),
          summary: z.string(),
        })
        .strict(),
    ),
    quotes: z.array(
      z
        .object({
          text: z.string(),
          startSeconds: z.number().nullable(),
        })
        .strict(),
    ),
    entities: z.array(z.object({ name: z.string(), type: z.string() }).strict()),
    actionItems: z.array(z.string()),
    glossary: z.array(z.object({ term: z.string(), definition: z.string() }).strict()),
    suggestedQuestions: z.array(z.string()),
    tags: z.array(z.string()),
  })
  .strict() satisfies z.ZodType<KnowledgeCard>;

function requireOpenAIKey(env: AppEnv): string {
  if (!env.OPENAI_API_KEY) {
    throw new Response("OpenAI API key is not configured", { status: 500 });
  }
  return env.OPENAI_API_KEY;
}

async function parseOpenAIResponse(response: Response): Promise<unknown> {
  const body = await response.text();
  try {
    return body ? JSON.parse(body) : {};
  } catch {
    throw new Error("OpenAI provider returned invalid JSON");
  }
}

async function assertOk(response: Response): Promise<unknown> {
  const json = await parseOpenAIResponse(response);
  if (!response.ok) {
    throw new Error("OpenAI provider request failed");
  }
  return json;
}

function extractResponseText(json: unknown): string {
  if (json && typeof json === "object" && "output_text" in json && typeof json.output_text === "string") {
    return json.output_text;
  }

  const output = json && typeof json === "object" && "output" in json ? json.output : undefined;
  if (!Array.isArray(output)) return "";

  return output
    .flatMap((item) => {
      if (!item || typeof item !== "object" || !("content" in item) || !Array.isArray(item.content)) return [];
      return (item.content as unknown[]).flatMap((content: unknown) => {
        if (content && typeof content === "object" && "text" in content && typeof content.text === "string") {
          return [content.text];
        }
        return [];
      });
    })
    .join("\n");
}

function parseKnowledgeCard(text: string): KnowledgeCard {
  try {
    const parsed = JSON.parse(text);
    return knowledgeCardSchema.parse(parsed);
  } catch {
    throw new Error("Analysis provider returned invalid knowledge card JSON");
  }
}

function jsonSchemaForKnowledgeCard() {
  return {
    type: "object",
    additionalProperties: false,
    required: [
      "title",
      "summary",
      "keyPoints",
      "chapters",
      "quotes",
      "entities",
      "actionItems",
      "glossary",
      "suggestedQuestions",
      "tags",
    ],
    properties: {
      title: { type: "string" },
      summary: { type: "string" },
      keyPoints: { type: "array", items: { type: "string" } },
      chapters: {
        type: "array",
        items: {
          type: "object",
          additionalProperties: false,
          required: ["title", "startSeconds", "endSeconds", "summary"],
          properties: {
            title: { type: "string" },
            startSeconds: { type: "number" },
            endSeconds: { type: "number" },
            summary: { type: "string" },
          },
        },
      },
      quotes: {
        type: "array",
        items: {
          type: "object",
          additionalProperties: false,
          required: ["text", "startSeconds"],
          properties: {
            text: { type: "string" },
            startSeconds: { type: ["number", "null"] },
          },
        },
      },
      entities: {
        type: "array",
        items: {
          type: "object",
          additionalProperties: false,
          required: ["name", "type"],
          properties: { name: { type: "string" }, type: { type: "string" } },
        },
      },
      actionItems: { type: "array", items: { type: "string" } },
      glossary: {
        type: "array",
        items: {
          type: "object",
          additionalProperties: false,
          required: ["term", "definition"],
          properties: { term: { type: "string" }, definition: { type: "string" } },
        },
      },
      suggestedQuestions: { type: "array", items: { type: "string" } },
      tags: { type: "array", items: { type: "string" } },
    },
  };
}

export class OpenAITranscriptionProvider implements TranscriptionProvider {
  private readonly apiKey: string;

  constructor(env: AppEnv) {
    this.apiKey = requireOpenAIKey(env);
  }

  async transcribe(input: { audioUrl: string; sourceTitle: string }): Promise<TranscriptionResult> {
    const audioResponse = await fetch(input.audioUrl);
    if (!audioResponse.ok) throw new Error("Audio source could not be fetched for transcription");
    const audioBlob = await audioResponse.blob();
    const form = new FormData();
    form.set("model", transcriptionModel);
    form.set("file", audioBlob, `${input.sourceTitle || "audio"}.mp3`);
    form.set("response_format", "verbose_json");

    const response = await fetch(transcriptionsEndpoint, {
      method: "POST",
      headers: { Authorization: `Bearer ${this.apiKey}` },
      body: form,
    });
    const json = await assertOk(response) as {
      text?: unknown;
      language?: unknown;
      duration?: unknown;
      segments?: unknown;
    };

    const rawSegments = Array.isArray(json.segments) ? json.segments : [];
    const segments = rawSegments.flatMap((segment): TranscriptionResult["segments"] => {
      if (!segment || typeof segment !== "object") return [];
      const start = "start" in segment ? segment.start : undefined;
      const end = "end" in segment ? segment.end : undefined;
      const text = "text" in segment ? segment.text : undefined;
      if (typeof start !== "number" || typeof end !== "number" || typeof text !== "string") return [];
      return [{ startSeconds: start, endSeconds: end, text }];
    });

    return {
      text: typeof json.text === "string" ? json.text : "",
      segments,
      language: typeof json.language === "string" ? json.language : null,
      durationSeconds: typeof json.duration === "number" ? json.duration : null,
      provider,
      model: transcriptionModel,
    };
  }
}

export class OpenAIAnalysisProvider implements AnalysisProvider {
  private readonly apiKey: string;

  constructor(env: AppEnv) {
    this.apiKey = requireOpenAIKey(env);
  }

  async analyze(input: Parameters<AnalysisProvider["analyze"]>[0]): Promise<AnalysisResult> {
    const response = await fetch(responsesEndpoint, {
      method: "POST",
      headers: {
        Authorization: `Bearer ${this.apiKey}`,
        "Content-Type": "application/json",
      },
      body: JSON.stringify({
        model: analysisModel,
        input: [
          {
            role: "system",
            content: "Return only a KnowledgeCard JSON object for the podcast transcript. Do not include prose.",
          },
          {
            role: "user",
            content: JSON.stringify({ title: input.title, transcript: input.transcript, segments: input.segments }),
          },
        ],
        text: {
          format: {
            type: "json_schema",
            name: "knowledge_card",
            strict: true,
            schema: jsonSchemaForKnowledgeCard(),
          },
        },
      }),
    });
    const json = await assertOk(response);
    const text = extractResponseText(json);
    return { card: parseKnowledgeCard(text), provider, model: analysisModel };
  }
}

export class OpenAIChatProvider implements ChatProvider {
  private readonly apiKey: string;

  constructor(env: AppEnv) {
    this.apiKey = requireOpenAIKey(env);
  }

  async answer(input: Parameters<ChatProvider["answer"]>[0]): Promise<Awaited<ReturnType<ChatProvider["answer"]>>> {
    const response = await fetch(responsesEndpoint, {
      method: "POST",
      headers: {
        Authorization: `Bearer ${this.apiKey}`,
        "Content-Type": "application/json",
      },
      body: JSON.stringify({
        model: chatModel,
        input: [
          {
            role: "system",
            content: "Answer podcast questions using the provided analysis and transcript context. Include concise citations.",
          },
          {
            role: "user",
            content: JSON.stringify(input),
          },
        ],
        text: {
          format: {
            type: "json_schema",
            name: "podcast_answer",
            strict: true,
            schema: {
              type: "object",
              additionalProperties: false,
              required: ["answer", "citations"],
              properties: {
                answer: { type: "string" },
                citations: {
                  type: "array",
                  items: {
                    type: "object",
                    additionalProperties: false,
                    required: ["startSeconds", "text"],
                    properties: {
                      startSeconds: { type: ["number", "null"] },
                      text: { type: "string" },
                    },
                  },
                },
              },
            },
          },
        },
      }),
    });
    const json = await assertOk(response);
    const parsed = z
      .object({
        answer: z.string(),
        citations: z.array(z.object({ startSeconds: z.number().nullable(), text: z.string() }).strict()),
      })
      .strict()
      .parse(JSON.parse(extractResponseText(json)));

    return { ...parsed, provider, model: chatModel };
  }
}
