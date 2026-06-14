import { json, type LoaderFunctionArgs } from "@remix-run/cloudflare";
import { Link, useLoaderData } from "@remix-run/react";
import { requireUserId } from "../lib/auth.server";
import { requireEnv } from "../lib/env.server";
import { listUploadsForUser } from "../lib/repositories/uploads.server";

export async function loader({ request, context }: LoaderFunctionArgs) {
  const env = requireEnv(context);
  const userId = await requireUserId(request, env);
  const uploads = await listUploadsForUser(env.DB, userId);
  return json({ uploads });
}

function formatSize(sizeBytes: number): string {
  return `${(sizeBytes / (1024 * 1024)).toFixed(1)} MB`;
}

function formatDuration(durationSeconds: number | null): string {
  if (durationSeconds === null) return "Duration unknown";
  const minutes = Math.round(durationSeconds / 60);
  return `${minutes} min`;
}

export default function UploadsIndex() {
  const { uploads } = useLoaderData<typeof loader>();

  return (
    <main>
      <h1>Uploads</h1>
      <p>
        <Link to="/uploads/new">Upload audio</Link>
      </p>
      {uploads.length === 0 ? (
        <p>No uploads yet.</p>
      ) : (
        <ul>
          {uploads.map((upload) => (
            <li key={upload.id}>
              <strong>{upload.original_filename}</strong> · {formatSize(upload.size_bytes)} ·{" "}
              {formatDuration(upload.duration_seconds)} · {upload.processing_status}
            </li>
          ))}
        </ul>
      )}
      <p>
        <Link to="/dashboard">Back to dashboard</Link>
      </p>
    </main>
  );
}
