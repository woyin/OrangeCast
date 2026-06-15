import { json, redirect, type ActionFunctionArgs, type LoaderFunctionArgs } from "@remix-run/cloudflare";
import { Form, Link, useLoaderData } from "@remix-run/react";
import { requireUserId } from "../lib/auth.server";
import { requireEnv } from "../lib/env.server";
import { getUploadForUser, listUploadsForUser } from "../lib/repositories/uploads.server";
import { enqueueProcessingForSource } from "../lib/services/processing.server";

function canProcess(status: string): boolean {
  return status === "unprocessed" || status === "failed";
}

function uploadSourceHref(uploadId: string): string {
  return `/sources/upload/${encodeURIComponent(uploadId)}`;
}

export async function loader({ request, context }: LoaderFunctionArgs) {
  const env = requireEnv(context);
  const userId = await requireUserId(request, env);
  const uploads = await listUploadsForUser(env.DB, userId);
  return json({ uploads });
}

export async function action({ request, context }: ActionFunctionArgs) {
  const env = requireEnv(context);
  const userId = await requireUserId(request, env);
  const formData = await request.formData();
  const intent = formData.get("intent");
  const sourceType = formData.get("sourceType");
  const sourceId = formData.get("sourceId");

  if (intent !== "process" || sourceType !== "upload" || typeof sourceId !== "string") {
    throw new Response("Bad request", { status: 400 });
  }

  const upload = await getUploadForUser(env.DB, userId, sourceId);
  if (!upload) throw new Response("Not found", { status: 404 });

  if (canProcess(upload.processing_status)) {
    await enqueueProcessingForSource(env, env.DB, userId, "upload", upload.id);
  }

  return redirect("/uploads");
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
          {uploads.map((upload) => {
            const processable = canProcess(upload.processing_status);
            return (
              <li key={upload.id}>
                <strong>
                  <Link to={uploadSourceHref(upload.id)}>{upload.original_filename}</Link>
                </strong>{" "}
                · {formatSize(upload.size_bytes)} · {formatDuration(upload.duration_seconds)} · {upload.processing_status}
                <Form method="post">
                  <input type="hidden" name="intent" value="process" />
                  <input type="hidden" name="sourceType" value="upload" />
                  <input type="hidden" name="sourceId" value={upload.id} />
                  <button type="submit" disabled={!processable}>
                    Process
                  </button>
                </Form>
              </li>
            );
          })}
        </ul>
      )}
      <p>
        <Link to="/dashboard">Back to dashboard</Link>
      </p>
    </main>
  );
}
