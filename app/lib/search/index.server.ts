import type { Db } from "../db.server";
import type { SourceType } from "../queue/messages.server";

export interface SearchFields {
  title?: string | null;
  summary?: string | null;
}

export interface SearchResult {
  id: string;
  sourceType: SourceType;
  sourceId: string;
  title: string;
  summary: string | null;
  detailHref: string;
  score: number;
}

interface EpisodeSearchRow {
  id: string;
  title: string;
  description: string | null;
}

interface UploadSearchRow {
  id: string;
  original_filename: string;
}

interface AnalysisSearchRow {
  id: string;
  source_type: SourceType;
  source_id: string;
  title: string;
  summary: string;
}

export const transcriptBodySearchDisclosure =
  "Transcript body search is enabled after transcript indexing is configured";

function normalizedTerms(query: string): string[] {
  return Array.from(new Set(query.toLowerCase().trim().split(/\s+/).filter(Boolean)));
}

function fieldScore(queryTerms: string[], value: string | null | undefined, weight: number): number {
  if (!value) return 0;
  const normalized = value.toLowerCase();
  return queryTerms.reduce((score, term) => (normalized.includes(term) ? score + weight : score), 0);
}

function escapeLike(value: string): string {
  return value.replace(/[\\%_]/g, (character) => `\\${character}`);
}

function likePattern(query: string): string {
  return `%${escapeLike(query.trim())}%`;
}

function detailHref(sourceType: SourceType, sourceId: string): string {
  return `/sources/${sourceType}/${encodeURIComponent(sourceId)}`;
}

export function scoreSearchResult(query: string, fields: SearchFields): number {
  const terms = normalizedTerms(query);
  if (terms.length === 0) return 0;

  return fieldScore(terms, fields.title, 10) + fieldScore(terms, fields.summary, 3);
}

export async function searchUserContent(db: Db, userId: string, query: string): Promise<SearchResult[]> {
  const trimmedQuery = query.trim();
  if (!trimmedQuery) return [];

  const pattern = likePattern(trimmedQuery);
  const [episodesResult, uploadsResult, analysesResult] = await Promise.all([
    db
      .prepare(
        `SELECT id, title, description
         FROM episodes
         WHERE user_id = ? AND title LIKE ? ESCAPE '\\'
         LIMIT 50`,
      )
      .bind(userId, pattern)
      .all<EpisodeSearchRow>(),
    db
      .prepare(
        `SELECT id, original_filename
         FROM uploads
         WHERE user_id = ? AND original_filename LIKE ? ESCAPE '\\'
         LIMIT 50`,
      )
      .bind(userId, pattern)
      .all<UploadSearchRow>(),
    db
      .prepare(
        `SELECT id, source_type, source_id, title, summary
         FROM analyses
         WHERE user_id = ? AND (title LIKE ? ESCAPE '\\' OR summary LIKE ? ESCAPE '\\')
         LIMIT 50`,
      )
      .bind(userId, pattern, pattern)
      .all<AnalysisSearchRow>(),
  ]);

  const results: SearchResult[] = [
    ...((episodesResult.results ?? []).map((episode) => {
      const score = scoreSearchResult(trimmedQuery, { title: episode.title, summary: episode.description });
      return {
        id: `episode:${episode.id}`,
        sourceType: "episode" as const,
        sourceId: episode.id,
        title: episode.title,
        summary: episode.description,
        detailHref: detailHref("episode", episode.id),
        score,
      };
    })),
    ...((uploadsResult.results ?? []).map((upload) => {
      const score = scoreSearchResult(trimmedQuery, { title: upload.original_filename });
      return {
        id: `upload:${upload.id}`,
        sourceType: "upload" as const,
        sourceId: upload.id,
        title: upload.original_filename,
        summary: null,
        detailHref: detailHref("upload", upload.id),
        score,
      };
    })),
    ...((analysesResult.results ?? []).map((analysis) => {
      const score = scoreSearchResult(trimmedQuery, { title: analysis.title, summary: analysis.summary });
      return {
        id: `analysis:${analysis.id}`,
        sourceType: analysis.source_type,
        sourceId: analysis.source_id,
        title: analysis.title,
        summary: analysis.summary,
        detailHref: detailHref(analysis.source_type, analysis.source_id),
        score,
      };
    })),
  ];

  return results
    .filter((result) => result.score > 0)
    .sort((left, right) => right.score - left.score || left.title.localeCompare(right.title))
    .slice(0, 50);
}
