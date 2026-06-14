import { json, type LoaderFunctionArgs } from "@remix-run/cloudflare";
import { Link, useLoaderData } from "@remix-run/react";
import { requireUserId } from "../lib/auth.server";
import { requireEnv } from "../lib/env.server";
import type { SourceProcessingStatus, SourceType } from "../lib/queue/messages.server";
import type { KnowledgeCard, TranscriptSegment } from "../lib/providers/types.server";
import { getAnalysisForSource } from "../lib/repositories/analyses.server";
import { getSourceEpisodeForUser, type EpisodeRecord } from "../lib/repositories/episodes.server";
import { getTranscriptForSource } from "../lib/repositories/transcripts.server";
import { getUploadForUser, type UploadRecord } from "../lib/repositories/uploads.server";

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

async function loadJsonArtifactIfPresent<T>(bucket: R2Bucket, key: string | null | undefined): Promise<T | null> {
  const text = await loadTextArtifactIfPresent(bucket, key);
  if (text === null) return null;
  return JSON.parse(text) as T;
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

function sourceTitle(source: SourceRecord): string {
  return source.type === "episode" ? source.record.title : source.record.original_filename;
}

function sourceStatus(source: SourceRecord): SourceProcessingStatus | string {
  return source.record.processing_status;
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
    loadJsonArtifactIfPresent<KnowledgeCard>(env.R2, analysis?.content_json_r2_key),
    loadJsonArtifactIfPresent<TranscriptSegment[]>(env.R2, transcript?.segments_r2_key),
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
            <a href={source.record.audio_url}>Episode audio</a>
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
