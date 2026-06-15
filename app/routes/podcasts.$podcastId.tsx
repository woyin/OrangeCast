import { json, redirect, type ActionFunctionArgs, type LoaderFunctionArgs } from "@remix-run/cloudflare";
import { Form, Link, useLoaderData } from "@remix-run/react";
import { requireUserId } from "../lib/auth.server";
import { requireEnv } from "../lib/env.server";
import { getSourceEpisodeForUser, listEpisodesForPodcast } from "../lib/repositories/episodes.server";
import { getPodcastForUser } from "../lib/repositories/podcasts.server";
import { enqueueProcessingForSource } from "../lib/services/processing.server";

function canProcess(status: string): boolean {
  return status === "unprocessed" || status === "failed";
}

function episodeSourceHref(episodeId: string): string {
  return `/sources/episode/${encodeURIComponent(episodeId)}`;
}

export async function loader({ request, context, params }: LoaderFunctionArgs) {
  const env = requireEnv(context);
  const userId = await requireUserId(request, env);
  const podcastId = params.podcastId;
  if (!podcastId) throw new Response("Not found", { status: 404 });

  const podcast = await getPodcastForUser(env.DB, userId, podcastId);
  if (!podcast) throw new Response("Not found", { status: 404 });

  const episodes = await listEpisodesForPodcast(env.DB, userId, podcast.id);
  return json({ podcast, episodes });
}

export async function action({ request, context, params }: ActionFunctionArgs) {
  const env = requireEnv(context);
  const userId = await requireUserId(request, env);
  const podcastId = params.podcastId;
  if (!podcastId) throw new Response("Not found", { status: 404 });

  const formData = await request.formData();
  const intent = formData.get("intent");
  const sourceType = formData.get("sourceType");
  const sourceId = formData.get("sourceId");

  if (intent !== "process" || sourceType !== "episode" || typeof sourceId !== "string") {
    throw new Response("Bad request", { status: 400 });
  }

  const episode = await getSourceEpisodeForUser(env.DB, userId, sourceId);
  if (!episode || episode.podcast_id !== podcastId) {
    throw new Response("Not found", { status: 404 });
  }

  if (canProcess(episode.processing_status)) {
    await enqueueProcessingForSource(env, env.DB, userId, "episode", episode.id);
  }

  return redirect(`/podcasts/${podcastId}`);
}

export default function PodcastDetail() {
  const { podcast, episodes } = useLoaderData<typeof loader>();

  return (
    <main>
      <p>
        <Link to="/podcasts">Back to podcasts</Link>
      </p>
      <h1>{podcast.title}</h1>
      {podcast.description ? <p>{podcast.description}</p> : null}
      {podcast.site_url ? (
        <p>
          <a href={podcast.site_url}>Podcast website</a>
        </p>
      ) : null}

      <h2>Episodes</h2>
      {episodes.length === 0 ? (
        <p>No playable episodes found.</p>
      ) : (
        <ul>
          {episodes.map((episode) => {
            const processable = canProcess(episode.processing_status);
            return (
              <li key={episode.id}>
                <h3>
                  <Link to={episodeSourceHref(episode.id)}>{episode.title}</Link>
                </h3>
                {episode.published_at ? (
                  <p>{new Date(episode.published_at).toLocaleDateString()}</p>
                ) : null}
                <p>Status: {episode.processing_status}</p>
                <Form method="post">
                  <input type="hidden" name="intent" value="process" />
                  <input type="hidden" name="sourceType" value="episode" />
                  <input type="hidden" name="sourceId" value={episode.id} />
                  <button type="submit" disabled={!processable}>
                    Process
                  </button>
                </Form>
              </li>
            );
          })}
        </ul>
      )}
    </main>
  );
}
