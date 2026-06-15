import { json, type LoaderFunctionArgs } from "@remix-run/cloudflare";
import { Form, Link, useLoaderData } from "@remix-run/react";
import { requireUserId } from "../lib/auth.server";
import { requireEnv } from "../lib/env.server";
import {
  searchUserContent,
  transcriptBodySearchDisclosure,
  type SearchResult,
} from "../lib/search/index.server";

function sourceTypeLabel(sourceType: SearchResult["sourceType"]): string {
  if (sourceType === "podcast") return "Podcast";
  return sourceType === "episode" ? "Episode" : "Upload";
}

function snippet(summary: string | null): string {
  if (!summary?.trim()) return "No summary snippet available.";
  const normalized = summary.replace(/\s+/g, " ").trim();
  return normalized.length > 220 ? `${normalized.slice(0, 217)}...` : normalized;
}

export async function loader({ request, context }: LoaderFunctionArgs) {
  const env = requireEnv(context);
  const userId = await requireUserId(request, env);
  const url = new URL(request.url);
  const query = (url.searchParams.get("q") ?? "").trim();
  const results = query ? await searchUserContent(env.DB, userId, query) : [];

  return json({
    query,
    results,
    transcriptBodySearchDisclosure,
  });
}

export default function SearchRoute() {
  const { query, results, transcriptBodySearchDisclosure } = useLoaderData<typeof loader>();
  const hasQuery = query.length > 0;

  return (
    <main>
      <p>
        <Link to="/dashboard">Back to dashboard</Link>
      </p>
      <h1>Search</h1>
      <Form method="get" action="/search">
        <label>
          Search your content
          <input name="q" type="search" defaultValue={query} />
        </label>
        <button type="submit">Search</button>
      </Form>

      <p>{transcriptBodySearchDisclosure}</p>

      {!hasQuery ? (
        <p>Enter a search query to find episodes, uploads, and analysis summaries.</p>
      ) : results.length === 0 ? (
        <p>No results found for “{query}”.</p>
      ) : (
        <section>
          <h2>Results for “{query}”</h2>
          <ul>
            {results.map((result) => (
              <li key={result.id}>
                <p>{sourceTypeLabel(result.sourceType)}</p>
                <h3>
                  <Link to={result.detailHref}>{result.title}</Link>
                </h3>
                <p>{snippet(result.summary)}</p>
                <p>
                  <Link to={result.detailHref}>View detail</Link>
                </p>
              </li>
            ))}
          </ul>
        </section>
      )}
    </main>
  );
}
