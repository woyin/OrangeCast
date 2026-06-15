import { json, type LoaderFunctionArgs } from "@remix-run/cloudflare";
import { Link, useLoaderData } from "@remix-run/react";
import { requireUserId } from "../lib/auth.server";
import { requireEnv } from "../lib/env.server";
import { listPodcastsForUser } from "../lib/repositories/podcasts.server";

export async function loader({ request, context }: LoaderFunctionArgs) {
  const env = requireEnv(context);
  const userId = await requireUserId(request, env);
  const podcasts = await listPodcastsForUser(env.DB, userId);
  return json({ podcasts });
}

export default function PodcastsIndex() {
  const { podcasts } = useLoaderData<typeof loader>();

  return (
    <main>
      <h1>Podcasts</h1>
      <p>
        <Link to="/podcasts/new">Add podcast</Link>
      </p>
      {podcasts.length === 0 ? (
        <p>No podcasts yet.</p>
      ) : (
        <ul>
          {podcasts.map((podcast) => (
            <li key={podcast.id}>
              <Link to={`/podcasts/${podcast.id}`}>{podcast.title}</Link>
              {podcast.site_url ? (
                <span>
                  {" "}
                  · <a href={podcast.site_url}>Website</a>
                </span>
              ) : null}
            </li>
          ))}
        </ul>
      )}
    </main>
  );
}
