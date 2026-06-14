import { json, redirect, type ActionFunctionArgs, type LoaderFunctionArgs } from "@remix-run/cloudflare";
import { Form, Link, useActionData } from "@remix-run/react";
import { requireUserId } from "../lib/auth.server";
import { requireEnv } from "../lib/env.server";
import { upsertEpisodesForPodcast } from "../lib/repositories/episodes.server";
import { upsertPodcastForUser } from "../lib/repositories/podcasts.server";
import { parsePodcastRss } from "../lib/services/rss-parser.server";

const FEED_FETCH_TIMEOUT_MS = 10_000;
const MAX_FEED_BYTES = 2 * 1024 * 1024;
const RSS_FETCH_ERROR = "Could not fetch that RSS feed.";
const MAX_REDIRECTS = 3;

function parseIpv4(hostname: string): number[] | null {
  const parts = hostname.split(".");
  if (parts.length !== 4) return null;

  const octets = parts.map((part) => {
    if (!/^\d{1,3}$/.test(part)) return Number.NaN;
    const value = Number(part);
    return value >= 0 && value <= 255 ? value : Number.NaN;
  });

  return octets.every((octet) => Number.isInteger(octet)) ? octets : null;
}

function isBlockedIpLiteral(hostname: string): boolean {
  const lowerHostname = hostname.toLowerCase();
  const normalized = lowerHostname.startsWith("[") && lowerHostname.endsWith("]")
    ? lowerHostname.slice(1, -1)
    : lowerHostname;
  if (["localhost", "127.0.0.1", "::1", "0.0.0.0"].includes(normalized)) return true;
  if (normalized.startsWith("fc") || normalized.startsWith("fd")) return true;
  if (/^fe[89ab]/.test(normalized)) return true;

  const ipv4 = parseIpv4(normalized);
  if (!ipv4) return false;

  const [first, second] = ipv4;
  return (
    first === 10 ||
    first === 127 ||
    (first === 172 && second >= 16 && second <= 31) ||
    (first === 192 && second === 168) ||
    (first === 169 && second === 254) ||
    (first === 0 && second === 0 && ipv4[2] === 0 && ipv4[3] === 0)
  );
}

function validateFeedUrl(feedUrl: string): { ok: true; url: URL } | { ok: false; error: string } {
  let url: URL;
  try {
    url = new URL(feedUrl);
  } catch {
    return { ok: false, error: "Enter a valid feed URL." };
  }

  if (url.protocol !== "https:") {
    return { ok: false, error: "Feed URL must use HTTPS." };
  }

  if (url.username || url.password) {
    return { ok: false, error: "Feed URL must not include credentials." };
  }

  if (isBlockedIpLiteral(url.hostname)) {
    return { ok: false, error: "Feed URL host is not allowed." };
  }

  return { ok: true, url };
}

async function readResponseTextWithLimit(response: Response): Promise<string> {
  const contentLength = response.headers.get("content-length");
  if (contentLength) {
    const byteLength = Number(contentLength);
    if (Number.isFinite(byteLength) && byteLength > MAX_FEED_BYTES) {
      throw new Error("RSS feed is too large.");
    }
  }

  if (!response.body) {
    const text = await response.text();
    if (new TextEncoder().encode(text).byteLength > MAX_FEED_BYTES) {
      throw new Error("RSS feed is too large.");
    }
    return text;
  }

  const reader = response.body.getReader();
  const chunks: Uint8Array[] = [];
  let totalBytes = 0;

  while (true) {
    const { done, value } = await reader.read();
    if (done) break;
    if (!value) continue;

    totalBytes += value.byteLength;
    if (totalBytes > MAX_FEED_BYTES) {
      await reader.cancel();
      throw new Error("RSS feed is too large.");
    }
    chunks.push(value);
  }

  const bytes = new Uint8Array(totalBytes);
  let offset = 0;
  for (const chunk of chunks) {
    bytes.set(chunk, offset);
    offset += chunk.byteLength;
  }

  return new TextDecoder().decode(bytes);
}

function isRedirectStatus(status: number): boolean {
  return status >= 300 && status < 400;
}

function validateRedirectLocation(location: string | null, baseUrl: URL): URL {
  if (!location) throw new Error(RSS_FETCH_ERROR);

  const redirectUrl = new URL(location, baseUrl);
  const validated = validateFeedUrl(redirectUrl.toString());
  if (!validated.ok) throw new Error(RSS_FETCH_ERROR);
  return validated.url;
}

async function fetchFeedXml(url: URL): Promise<string> {
  const controller = new AbortController();
  const timeoutId = setTimeout(() => controller.abort(), FEED_FETCH_TIMEOUT_MS);

  try {
    let currentUrl = url;

    for (let redirectCount = 0; redirectCount <= MAX_REDIRECTS; redirectCount += 1) {
      const response = await fetch(currentUrl.toString(), {
        redirect: "manual",
        signal: controller.signal,
      });

      if (isRedirectStatus(response.status)) {
        if (redirectCount === MAX_REDIRECTS) throw new Error(RSS_FETCH_ERROR);
        currentUrl = validateRedirectLocation(response.headers.get("location"), currentUrl);
        continue;
      }

      if (!response.ok) {
        throw new Error(RSS_FETCH_ERROR);
      }
      return await readResponseTextWithLimit(response);
    }

    throw new Error(RSS_FETCH_ERROR);
  } catch (error) {
    if (error instanceof Error && error.message === "RSS feed is too large.") {
      throw error;
    }
    throw new Error(RSS_FETCH_ERROR);
  } finally {
    clearTimeout(timeoutId);
  }
}

export async function loader({ request, context }: LoaderFunctionArgs) {
  const env = requireEnv(context);
  await requireUserId(request, env);
  return null;
}

export async function action({ request, context }: ActionFunctionArgs) {
  const env = requireEnv(context);
  const userId = await requireUserId(request, env);
  const form = await request.formData();
  const feedUrl = String(form.get("feedUrl") || "").trim();

  if (!feedUrl) {
    return json({ error: "Feed URL is required." }, { status: 400 });
  }

  const validatedUrl = validateFeedUrl(feedUrl);
  if (!validatedUrl.ok) {
    return json({ error: validatedUrl.error }, { status: 400 });
  }

  let xml: string;
  try {
    xml = await fetchFeedXml(validatedUrl.url);
  } catch (error) {
    return json(
      { error: error instanceof Error ? error.message : RSS_FETCH_ERROR },
      { status: 400 },
    );
  }

  let parsed;
  try {
    parsed = parsePodcastRss(xml);
  } catch (error) {
    return json(
      { error: error instanceof Error ? error.message : "Could not parse that RSS feed." },
      { status: 400 },
    );
  }

  const podcast = await upsertPodcastForUser(env.DB, userId, validatedUrl.url.toString(), parsed);
  await upsertEpisodesForPodcast(env.DB, userId, podcast.id, parsed.episodes);

  return redirect(`/podcasts/${podcast.id}`);
}

export default function NewPodcast() {
  const actionData = useActionData<typeof action>();

  return (
    <main>
      <h1>Add podcast</h1>
      {actionData?.error ? <p role="alert">{actionData.error}</p> : null}
      <Form method="post">
        <label>
          RSS feed URL
          <input name="feedUrl" type="url" placeholder="https://example.com/feed.xml" />
        </label>
        <button type="submit">Add podcast</button>
      </Form>
      <p>
        <Link to="/podcasts">Back to podcasts</Link>
      </p>
    </main>
  );
}
