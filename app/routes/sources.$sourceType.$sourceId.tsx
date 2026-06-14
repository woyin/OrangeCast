import { json, type ActionFunctionArgs, type LoaderFunctionArgs } from "@remix-run/cloudflare";
import { Form, Link, useActionData, useLoaderData } from "@remix-run/react";
import { requireUserId } from "../lib/auth.server";
import { requireEnv } from "../lib/env.server";
import type { SourceProcessingStatus, SourceType } from "../lib/queue/messages.server";
import type { KnowledgeCard, TranscriptSegment } from "../lib/providers/types.server";
import { getAnalysisForSource } from "../lib/repositories/analyses.server";
import { getSourceEpisodeForUser, type EpisodeRecord } from "../lib/repositories/episodes.server";
import { getTranscriptForSource } from "../lib/repositories/transcripts.server";
import { createMockChatProvider } from "../lib/providers/mock.server";
import { answerSourceQuestion } from "../lib/services/qa.server";
import { renderKnowledgeCardMarkdown } from "../lib/export/markdown.server";
import { getUploadForUser, type UploadRecord } from "../lib/repositories/uploads.server";

export const MAX_QUESTION_LENGTH = 1000;

type AskActionData = {
  intent: "ask";
  question: string;
  answer: Awaited<ReturnType<typeof answerSourceQuestion>> | null;
  error: string | null;
};

function parseSourceType(value: string | undefined): SourceType | null {
  if (value === "episode" || value === "upload") return value;
  return null;
}


async function loadTextArtifactIfPresent(bucket: R2Bucket, key: string | null | undefined): Promise<string | null> {
  if (!key) return null;
  const object = await bucket.get(key);
  if (!object) return null;
  return await object.text();
}

async function loadKnowledgeCardArtifactIfPresent(bucket: R2Bucket, key: string | null | undefined): Promise<KnowledgeCard | null> {
  const text = await loadTextArtifactIfPresent(bucket, key);
  if (text === null) return null;
  return safeParseKnowledgeCard(text);
}

async function loadTranscriptSegmentsArtifactIfPresent(
  bucket: R2Bucket,
  key: string | null | undefined,
): Promise<TranscriptSegment[] | null> {
  const text = await loadTextArtifactIfPresent(bucket, key);
  if (text === null) return null;
  return safeParseTranscriptSegments(text);
}

function getStatusMessage(status: string): string {
  switch (status) {
    case "unprocessed":
      return "This source has not been processed yet.";
    case "queued":
      return "Processing is queued and will start soon.";
    case "transcribing":
      return "Transcription is in progress.";
    case "transcribed":
      return "Transcript metadata is ready and analysis is waiting to run.";
    case "analyzing":
      return "Analysis is in progress.";
    case "processed":
      return "Knowledge card is ready.";
    case "failed":
      return "Processing failed. You can try processing this source again from its list page.";
    default:
      return "Processing status is unknown.";
  }
}

type SourceRecord =
  | { type: "episode"; record: EpisodeRecord }
  | { type: "upload"; record: UploadRecord };

function safeJsonParse(text: string): unknown | null {
  try {
    return JSON.parse(text) as unknown;
  } catch {
    return null;
  }
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return !!value && typeof value === "object" && !Array.isArray(value);
}

function stringArray(value: unknown): string[] {
  if (!Array.isArray(value)) return [];
  return value.filter((item): item is string => typeof item === "string" && item.length > 0);
}

function finiteNumber(value: unknown): value is number {
  return typeof value === "number" && Number.isFinite(value);
}

export function safeParseTranscriptSegments(text: string): TranscriptSegment[] | null {
  const value = safeJsonParse(text);
  if (!Array.isArray(value)) return null;

  return value.flatMap((item): TranscriptSegment[] => {
    if (!isRecord(item)) return [];
    if (!finiteNumber(item.startSeconds) || !finiteNumber(item.endSeconds)) return [];
    if (typeof item.text !== "string" || item.text.length === 0) return [];

    return [
      {
        startSeconds: item.startSeconds,
        endSeconds: item.endSeconds,
        text: item.text,
      },
    ];
  });
}

export function safeParseKnowledgeCard(text: string): KnowledgeCard | null {
  const value = safeJsonParse(text);
  if (!isRecord(value)) return null;
  if (typeof value.title !== "string" || typeof value.summary !== "string") return null;

  const chapters = Array.isArray(value.chapters)
    ? value.chapters.flatMap((chapter): KnowledgeCard["chapters"] => {
        if (!isRecord(chapter)) return [];
        if (typeof chapter.title !== "string" || typeof chapter.summary !== "string") return [];
        if (!finiteNumber(chapter.startSeconds) || !finiteNumber(chapter.endSeconds)) return [];
        return [
          {
            title: chapter.title,
            startSeconds: chapter.startSeconds,
            endSeconds: chapter.endSeconds,
            summary: chapter.summary,
          },
        ];
      })
    : [];

  const quotes = Array.isArray(value.quotes)
    ? value.quotes.flatMap((quote): KnowledgeCard["quotes"] => {
        if (!isRecord(quote)) return [];
        if (typeof quote.text !== "string" || quote.text.length === 0) return [];
        if (quote.startSeconds !== null && quote.startSeconds !== undefined && !finiteNumber(quote.startSeconds)) return [];
        return [{ text: quote.text, startSeconds: finiteNumber(quote.startSeconds) ? quote.startSeconds : null }];
      })
    : [];

  const entities = Array.isArray(value.entities)
    ? value.entities.flatMap((entity): KnowledgeCard["entities"] => {
        if (!isRecord(entity)) return [];
        if (typeof entity.name !== "string" || typeof entity.type !== "string") return [];
        return [{ name: entity.name, type: entity.type }];
      })
    : [];

  const glossary = Array.isArray(value.glossary)
    ? value.glossary.flatMap((entry): KnowledgeCard["glossary"] => {
        if (!isRecord(entry)) return [];
        if (typeof entry.term !== "string" || typeof entry.definition !== "string") return [];
        return [{ term: entry.term, definition: entry.definition }];
      })
    : [];

  return {
    title: value.title,
    summary: value.summary,
    keyPoints: stringArray(value.keyPoints),
    chapters,
    quotes,
    entities,
    actionItems: stringArray(value.actionItems),
    glossary,
    suggestedQuestions: stringArray(value.suggestedQuestions),
    tags: stringArray(value.tags),
  };
}

export function getSafeHttpUrl(value: string): string | null {
  try {
    const url = new URL(value);
    if (url.protocol !== "http:" && url.protocol !== "https:") return null;
    return url.href;
  } catch {
    return null;
  }
}

function sourceTitle(source: SourceRecord): string {
  return source.type === "episode" ? source.record.title : source.record.original_filename;
}

function sourceStatus(source: SourceRecord): SourceProcessingStatus | string {
  return source.record.processing_status;
}

function markdownDownloadResponse(markdown: string, title: string): Response {
  const filename = `${title.replace(/[/:*?\"<>|]/g, "-").trim() || "knowledge-card"}.md`;
  return new Response(markdown, {
    headers: {
      "Content-Type": "text/markdown; charset=utf-8",
      "Content-Disposition": `attachment; filename="${filename.replace(/"/g, "-")}"`,
    },
  });
}

function formatDate(value: string | null): string | null {
  if (!value) return null;
  return new Date(value).toLocaleDateString();
}

function formatTime(seconds: number | null): string {
  if (seconds === null) return "Unknown time";
  const totalSeconds = Math.max(0, Math.round(seconds));
  const minutes = Math.floor(totalSeconds / 60);
  const remainingSeconds = totalSeconds % 60;
  return `${minutes}:${remainingSeconds.toString().padStart(2, "0")}`;
}

function formatDuration(seconds: number | null): string {
  if (seconds === null) return "Duration unknown";
  const minutes = Math.round(seconds / 60);
  return `${minutes} min`;
}

function formatSize(sizeBytes: number): string {
  return `${(sizeBytes / (1024 * 1024)).toFixed(1)} MB`;
}

async function getOwnedSource(
  db: D1Database,
  userId: string,
  sourceType: SourceType,
  sourceId: string,
): Promise<SourceRecord | null> {
  if (sourceType === "episode") {
    const episode = await getSourceEpisodeForUser(db, userId, sourceId);
    return episode ? { type: "episode", record: episode } : null;
  }

  const upload = await getUploadForUser(db, userId, sourceId);
  return upload ? { type: "upload", record: upload } : null;
}

export async function action({ request, context, params }: ActionFunctionArgs) {
  const env = requireEnv(context);
  const userId = await requireUserId(request, env);
  const sourceType = parseSourceType(params.sourceType);
  const sourceId = params.sourceId;

  if (!sourceType || !sourceId) {
    throw new Response("Not found", { status: 404 });
  }

  const formData = await request.formData();
  const intent = formData.get("intent");

  if (intent === "download-markdown") {
    const source = await getOwnedSource(env.DB, userId, sourceType, sourceId);
    if (!source) throw new Response("Not found", { status: 404 });

    const analysis = await getAnalysisForSource(env.DB, userId, sourceType, sourceId);
    if (!analysis) throw new Response("Markdown is not available for this source", { status: 404 });

    const includeTranscript = formData.get("includeTranscript") === "on";
    const title = analysis.title || sourceTitle(source);

    if (!includeTranscript) {
      const existingMarkdown = await loadTextArtifactIfPresent(env.R2, analysis.markdown_r2_key);
      if (!existingMarkdown) throw new Response("Markdown artifact is not available", { status: 404 });
      return markdownDownloadResponse(existingMarkdown, title);
    }

    const [analysisCard, transcript] = await Promise.all([
      loadKnowledgeCardArtifactIfPresent(env.R2, analysis.content_json_r2_key),
      getTranscriptForSource(env.DB, userId, sourceType, sourceId),
    ]);
    if (!analysisCard) throw new Response("Analysis artifact is not available", { status: 404 });

    const transcriptText = await loadTextArtifactIfPresent(env.R2, transcript?.text_r2_key);
    const markdown = renderKnowledgeCardMarkdown(
      analysisCard,
      {
        sourceTitle: sourceTitle(source),
        sourceType,
        sourceId,
        createdAt: analysis.created_at,
      },
      { includeTranscriptAppendix: true, transcriptText: transcriptText ?? undefined },
    );

    return markdownDownloadResponse(markdown, title);
  }

  if (intent !== "ask") {
    throw new Response("Unsupported action", { status: 400 });
  }

  const question = String(formData.get("question") ?? "").trim();
  if (question.length === 0) {
    return json({ intent: "ask" as const, question, answer: null, error: "Enter a question to ask this source." }, { status: 400 });
  }

  if (question.length > MAX_QUESTION_LENGTH) {
    return json(
      {
        intent: "ask" as const,
        question,
        answer: null,
        error: `Question must be ${MAX_QUESTION_LENGTH} characters or fewer.`,
      },
      { status: 400 },
    );
  }

  const source = await getOwnedSource(env.DB, userId, sourceType, sourceId);
  if (!source) throw new Response("Not found", { status: 404 });

  const [transcript, analysis] = await Promise.all([
    getTranscriptForSource(env.DB, userId, sourceType, sourceId),
    getAnalysisForSource(env.DB, userId, sourceType, sourceId),
  ]);

  const [transcriptText, transcriptSegments, analysisCard] = await Promise.all([
    loadTextArtifactIfPresent(env.R2, transcript?.text_r2_key),
    loadTranscriptSegmentsArtifactIfPresent(env.R2, transcript?.segments_r2_key),
    loadKnowledgeCardArtifactIfPresent(env.R2, analysis?.content_json_r2_key),
  ]);

  if (!transcriptText || !transcriptSegments || !analysisCard) {
    return json(
      {
        intent: "ask" as const,
        question,
        answer: null,
        error: "This source needs transcript and analysis artifacts before Q&A is available.",
      },
      { status: 400 },
    );
  }

  const answer = await answerSourceQuestion({
    provider: createMockChatProvider(),
    question,
    title: analysisCard.title ?? analysis?.title ?? sourceTitle(source),
    transcriptText,
    segments: transcriptSegments,
    analysis: analysisCard,
  });

  return json({ intent: "ask" as const, question, answer, error: null });
}

export async function loader({ request, context, params }: LoaderFunctionArgs) {
  const env = requireEnv(context);
  const userId = await requireUserId(request, env);
  const sourceType = parseSourceType(params.sourceType);
  const sourceId = params.sourceId;

  if (!sourceType || !sourceId) {
    throw new Response("Not found", { status: 404 });
  }

  const source = await getOwnedSource(env.DB, userId, sourceType, sourceId);
  if (!source) throw new Response("Not found", { status: 404 });

  const [transcript, analysis] = await Promise.all([
    getTranscriptForSource(env.DB, userId, sourceType, sourceId),
    getAnalysisForSource(env.DB, userId, sourceType, sourceId),
  ]);

  const [analysisCard, transcriptSegments] = await Promise.all([
    loadKnowledgeCardArtifactIfPresent(env.R2, analysis?.content_json_r2_key),
    loadTranscriptSegmentsArtifactIfPresent(env.R2, transcript?.segments_r2_key),
  ]);

  return json({
    source,
    statusMessage: getStatusMessage(sourceStatus(source)),
    transcript,
    analysis,
    analysisCard,
    transcriptSegments,
  });
}

function ListSection({ title, items }: { title: string; items: string[] }) {
  return (
    <section>
      <h2>{title}</h2>
      {items.length === 0 ? (
        <p>None.</p>
      ) : (
        <ul>
          {items.map((item) => (
            <li key={item}>{item}</li>
          ))}
        </ul>
      )}
    </section>
  );
}

function SourceInfo({ source }: { source: SourceRecord }) {
  if (source.type === "episode") {
    const publishedAt = formatDate(source.record.published_at);
    const audioUrl = getSafeHttpUrl(source.record.audio_url);
    return (
      <dl>
        <div>
          <dt>Source type</dt>
          <dd>Episode</dd>
        </div>
        <div>
          <dt>Podcast ID</dt>
          <dd>{source.record.podcast_id}</dd>
        </div>
        <div>
          <dt>Duration</dt>
          <dd>{formatDuration(source.record.duration_seconds)}</dd>
        </div>
        {publishedAt ? (
          <div>
            <dt>Published</dt>
            <dd>{publishedAt}</dd>
          </div>
        ) : null}
        <div>
          <dt>Audio</dt>
          <dd>
            {audioUrl ? <a href={audioUrl}>Episode audio</a> : "Audio link unavailable"}
          </dd>
        </div>
      </dl>
    );
  }

  return (
    <dl>
      <div>
        <dt>Source type</dt>
        <dd>Upload</dd>
      </div>
      <div>
        <dt>Content type</dt>
        <dd>{source.record.content_type}</dd>
      </div>
      <div>
        <dt>Size</dt>
        <dd>{formatSize(source.record.size_bytes)}</dd>
      </div>
      <div>
        <dt>Duration</dt>
        <dd>{formatDuration(source.record.duration_seconds)}</dd>
      </div>
      <div>
        <dt>Uploaded</dt>
        <dd>{formatDate(source.record.created_at)}</dd>
      </div>
    </dl>
  );
}

function KnowledgeCardSections({ card }: { card: KnowledgeCard }) {
  return (
    <>
      <section>
        <h2>Summary</h2>
        <p>{card.summary}</p>
      </section>

      <ListSection title="Key points" items={card.keyPoints} />

      <section>
        <h2>Chapters</h2>
        {card.chapters.length === 0 ? (
          <p>None.</p>
        ) : (
          <ol>
            {card.chapters.map((chapter) => (
              <li key={`${chapter.startSeconds}-${chapter.title}`}>
                <strong>{chapter.title}</strong> ({formatTime(chapter.startSeconds)}–{formatTime(chapter.endSeconds)})
                <p>{chapter.summary}</p>
              </li>
            ))}
          </ol>
        )}
      </section>

      <section>
        <h2>Quotes</h2>
        {card.quotes.length === 0 ? (
          <p>None.</p>
        ) : (
          <ul>
            {card.quotes.map((quote) => (
              <li key={`${quote.startSeconds ?? "unknown"}-${quote.text}`}>
                <blockquote>{quote.text}</blockquote>
                <p>{quote.startSeconds === null ? "Timestamp unknown" : formatTime(quote.startSeconds)}</p>
              </li>
            ))}
          </ul>
        )}
      </section>

      <section>
        <h2>Entities</h2>
        {card.entities.length === 0 ? (
          <p>None.</p>
        ) : (
          <ul>
            {card.entities.map((entity) => (
              <li key={`${entity.type}-${entity.name}`}>
                {entity.name} <span>({entity.type})</span>
              </li>
            ))}
          </ul>
        )}
      </section>

      <ListSection title="Action items" items={card.actionItems} />

      <section>
        <h2>Glossary</h2>
        {card.glossary.length === 0 ? (
          <p>None.</p>
        ) : (
          <dl>
            {card.glossary.map((entry) => (
              <div key={entry.term}>
                <dt>{entry.term}</dt>
                <dd>{entry.definition}</dd>
              </div>
            ))}
          </dl>
        )}
      </section>

      <ListSection title="Suggested questions" items={card.suggestedQuestions} />
    </>
  );
}

function TranscriptSegments({ segments }: { segments: TranscriptSegment[] | null }) {
  return (
    <section>
      <h2>Transcript segments</h2>
      {!segments || segments.length === 0 ? (
        <p>No transcript segments available.</p>
      ) : (
        <ol>
          {segments.map((segment) => (
            <li key={`${segment.startSeconds}-${segment.endSeconds}-${segment.text}`}>
              <strong>
                {formatTime(segment.startSeconds)}–{formatTime(segment.endSeconds)}
              </strong>
              <p>{segment.text}</p>
            </li>
          ))}
        </ol>
      )}
    </section>
  );
}

function SourceQuestionAnswer() {
  const data = useActionData() as AskActionData | undefined;

  return (
    <section>
      <h2>Ask this source</h2>
      <Form method="post">
        <input type="hidden" name="intent" value="ask" />
        <label htmlFor="source-question">Question</label>
        <textarea
          id="source-question"
          name="question"
          defaultValue={data?.intent === "ask" ? data.question : ""}
          rows={3}
          maxLength={MAX_QUESTION_LENGTH}
          required
        />
        <button type="submit">Ask</button>
      </Form>

      {data?.intent === "ask" && data.error ? (
        <p role="alert">{data.error}</p>
      ) : null}

      {data?.intent === "ask" && data.answer ? (
        <article>
          <h3>Answer</h3>
          <p>{data.answer.answer}</p>
          <p>Answered by {data.answer.provider} ({data.answer.model})</p>
          <h4>Citations</h4>
          {data.answer.citations.length === 0 ? (
            <p>No citations returned.</p>
          ) : (
            <ol>
              {data.answer.citations.map((citation, index) => (
                <li key={`${citation.startSeconds ?? "unknown"}-${index}-${citation.text}`}>
                  <blockquote>{citation.text}</blockquote>
                  <p>{citation.startSeconds === null ? "Timestamp unknown" : formatTime(citation.startSeconds)}</p>
                </li>
              ))}
            </ol>
          )}
        </article>
      ) : null}
    </section>
  );
}

export default function SourceDetail() {
  const { source, statusMessage, transcript, analysis, analysisCard, transcriptSegments } = useLoaderData<typeof loader>();
  const status = sourceStatus(source);
  const title = analysisCard?.title ?? analysis?.title ?? sourceTitle(source);

  return (
    <main>
      <p>
        {source.type === "episode" ? (
          <Link to={`/podcasts/${source.record.podcast_id}`}>Back to episodes</Link>
        ) : (
          <Link to="/uploads">Back to uploads</Link>
        )}
      </p>

      <header>
        <p>Status: {status}</p>
        <h1>{title}</h1>
        <p>{statusMessage}</p>
      </header>

      <section>
        <h2>Source info</h2>
        <SourceInfo source={source} />
      </section>

      <section>
        <h2>Transcript metadata</h2>
        {transcript ? (
          <dl>
            <div>
              <dt>Provider</dt>
              <dd>{transcript.provider}</dd>
            </div>
            <div>
              <dt>Model</dt>
              <dd>{transcript.model}</dd>
            </div>
            <div>
              <dt>Language</dt>
              <dd>{transcript.language ?? "Unknown"}</dd>
            </div>
            <div>
              <dt>Duration</dt>
              <dd>{formatDuration(transcript.duration_seconds)}</dd>
            </div>
          </dl>
        ) : (
          <p>No transcript metadata yet.</p>
        )}
      </section>

      {analysis ? (
        <section>
          <h2>Download Markdown</h2>
          <Form method="post">
            <input type="hidden" name="intent" value="download-markdown" />
            <label>
              <input type="checkbox" name="includeTranscript" /> Include transcript appendix
            </label>
            <button type="submit">Download Markdown</button>
          </Form>
        </section>
      ) : null}

      <SourceQuestionAnswer />

      <section>
        <h2>Analysis metadata</h2>
        {analysis ? (
          <dl>
            <div>
              <dt>Provider</dt>
              <dd>{analysis.provider}</dd>
            </div>
            <div>
              <dt>Model</dt>
              <dd>{analysis.model}</dd>
            </div>
            <div>
              <dt>Created</dt>
              <dd>{formatDate(analysis.created_at)}</dd>
            </div>
          </dl>
        ) : (
          <p>No analysis metadata yet.</p>
        )}
      </section>

      {status === "processed" ? (
        analysisCard ? (
          <>
            <KnowledgeCardSections card={analysisCard} />
            <TranscriptSegments segments={transcriptSegments} />
          </>
        ) : (
          <p>Analysis metadata exists, but the knowledge card artifact is not available.</p>
        )
      ) : status === "transcribed" ? (
        <TranscriptSegments segments={transcriptSegments} />
      ) : null}
    </main>
  );
}
