import { json, redirect, type ActionFunctionArgs, type LoaderFunctionArgs } from "@remix-run/cloudflare";
import { Form, Link, useActionData } from "@remix-run/react";
import { requireUserId } from "../lib/auth.server";
import { requireEnv } from "../lib/env.server";
import { upsertEpisodesForPodcast } from "../lib/repositories/episodes.server";
import { upsertPodcastForUser } from "../lib/repositories/podcasts.server";
import { parsePodcastRss } from "../lib/services/rss-parser.server";

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

  let url: URL;
  try {
    url = new URL(feedUrl);
  } catch {
    return json({ error: "Enter a valid feed URL." }, { status: 400 });
  }

  if (url.protocol !== "https:" && url.protocol !== "http:") {
    return json({ error: "Feed URL must use HTTP or HTTPS." }, { status: 400 });
  }

  const response = await fetch(url.toString());
  if (!response.ok) {
    return json({ error: "Could not fetch that RSS feed." }, { status: 400 });
  }

  let parsed;
  try {
    parsed = parsePodcastRss(await response.text());
  } catch (error) {
    return json(
      { error: error instanceof Error ? error.message : "Could not parse that RSS feed." },
      { status: 400 },
    );
  }

  const podcast = await upsertPodcastForUser(env.DB, userId, url.toString(), parsed);
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
