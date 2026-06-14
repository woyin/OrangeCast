import { json, type LoaderFunctionArgs } from "@remix-run/cloudflare";
import { Link, useLoaderData } from "@remix-run/react";
import { requireUserId } from "../lib/auth.server";
import { requireEnv } from "../lib/env.server";
import { listEpisodesForPodcast } from "../lib/repositories/episodes.server";
import { getPodcastForUser } from "../lib/repositories/podcasts.server";

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

export default function PodcastDetail() {
  const { podcast, episodes } = useLoaderData<typeof loader>();

  return (
    <main>
      <p>
        <Link to="/podcasts">Back to podcasts</Link>
      </p>
      <h1>{podcast.title}</h1>
      {podcast.description ? <p>{podcast.description}</p> : null}
      {podcast.site_url ? <p><a href={podcast.site_url}>Podcast website</a></p> : null}

      <h2>Episodes</h2>
      {episodes.length === 0 ? (
        <p>No playable episodes found.</p>
      ) : (
        <ul>
          {episodes.map((episode) => (
            <li key={episode.id}>
              <h3>{episode.title}</h3>
              {episode.published_at ? (
                <p>{new Date(episode.published_at).toLocaleDateString()}</p>
              ) : null}
              <p>Status: {episode.processing_status}</p>
              <button type="button" disabled>
                Process
              </button>
            </li>
          ))}
        </ul>
      )}
    </main>
  );
}
