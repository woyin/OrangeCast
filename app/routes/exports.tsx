import { json, redirect, type ActionFunctionArgs, type LoaderFunctionArgs } from "@remix-run/cloudflare";
import { Form, Link, useActionData, useLoaderData } from "@remix-run/react";
import { buildMarkdownZip, safeDownloadFilename } from "../lib/export/zip.server";
import { requireUserId } from "../lib/auth.server";
import { newId } from "../lib/db.server";
import { requireEnv } from "../lib/env.server";
import {
  createExportRecord,
  exportZipKey,
  getExportForUser,
  listProcessedExportSources,
  listRecentExportsForUser,
  type ProcessedExportSource,
} from "../lib/repositories/exports.server";

export const MAX_EXPORT_SOURCE_COUNT = 50;
export const MAX_MARKDOWN_ARTIFACT_BYTES = 1_000_000;
export const MAX_EXPORT_MARKDOWN_BYTES = 20_000_000;

function sourceKey(source: Pick<ProcessedExportSource, "sourceType" | "sourceId">): string {
  return `${source.sourceType}:${source.sourceId}`;
}

async function loadTextArtifact(bucket: R2Bucket, key: string): Promise<{ text: string; byteLength: number }> {
  const object = await bucket.get(key);
  if (!object) throw new Response("Export source artifact is not available", { status: 404 });
  if (object.size > MAX_MARKDOWN_ARTIFACT_BYTES) {
    throw new Response("One selected source is too large to export.", { status: 400 });
  }

  const text = await object.text();
  const byteLength = new TextEncoder().encode(text).byteLength;
  if (byteLength > MAX_MARKDOWN_ARTIFACT_BYTES) {
    throw new Response("One selected source is too large to export.", { status: 400 });
  }

  return { text, byteLength };
}

function zipDownloadResponse(body: BodyInit | null, exportId: string): Response {
  return new Response(body, {
    headers: {
      "Content-Type": "application/zip",
      "Content-Disposition": `attachment; filename="${safeDownloadFilename(`cloudwisepod-export-${exportId}`, "zip", "cloudwisepod-export")}"`,
    },
  });
}

export async function loader({ request, context }: LoaderFunctionArgs) {
  const env = requireEnv(context);
  const userId = await requireUserId(request, env);
  const url = new URL(request.url);
  const downloadExportId = url.searchParams.get("download");

  if (downloadExportId) {
    const exportRecord = await getExportForUser(env.DB, userId, downloadExportId);
    if (!exportRecord) throw new Response("Not found", { status: 404 });

    const object = await env.R2.get(exportRecord.r2_object_key);
    if (!object) throw new Response("Export file is not available", { status: 404 });
    return zipDownloadResponse(object.body, exportRecord.id);
  }

  const [sources, exports] = await Promise.all([
    listProcessedExportSources(env.DB, userId),
    listRecentExportsForUser(env.DB, userId),
  ]);

  return json({ sources, exports });
}

export async function action({ request, context }: ActionFunctionArgs) {
  const env = requireEnv(context);
  const userId = await requireUserId(request, env);
  const formData = await request.formData();

  if (formData.get("intent") !== "create-zip") {
    throw new Response("Bad request", { status: 400 });
  }

  const selectedKeys = new Set(formData.getAll("source").filter((value): value is string => typeof value === "string"));
  if (selectedKeys.size === 0) {
    return json({ error: "Select at least one processed source to export." }, { status: 400 });
  }
  if (selectedKeys.size > MAX_EXPORT_SOURCE_COUNT) {
    return json({ error: `Select ${MAX_EXPORT_SOURCE_COUNT} or fewer sources per export.` }, { status: 400 });
  }

  const availableSources = await listProcessedExportSources(env.DB, userId);
  const selectedSources = availableSources.filter((source) => selectedKeys.has(sourceKey(source)));

  if (selectedSources.length === 0) {
    return json({ error: "No selected processed sources are available to export." }, { status: 400 });
  }
  if (selectedSources.length > MAX_EXPORT_SOURCE_COUNT) {
    return json({ error: `Select ${MAX_EXPORT_SOURCE_COUNT} or fewer sources per export.` }, { status: 400 });
  }

  const markdownSources = [];
  let totalMarkdownBytes = 0;
  for (const source of selectedSources) {
    const artifact = await loadTextArtifact(env.R2, source.markdownR2Key);
    totalMarkdownBytes += artifact.byteLength;
    if (totalMarkdownBytes > MAX_EXPORT_MARKDOWN_BYTES) {
      return json({ error: "Selected sources are too large to export together." }, { status: 400 });
    }
    markdownSources.push({
      podcastTitle: source.podcastTitle,
      title: source.title,
      markdown: artifact.text,
    });
  }

  const exportId = newId("exp");
  const r2ObjectKey = exportZipKey(userId, exportId);
  const zipBytes = await buildMarkdownZip(markdownSources);
  await env.R2.put(r2ObjectKey, zipBytes, {
    httpMetadata: { contentType: "application/zip" },
  });

  try {
    const exportRecord = await createExportRecord(env.DB, { id: exportId, userId, r2ObjectKey });
    return redirect(`/exports?created=${encodeURIComponent(exportRecord.id)}`);
  } catch (error) {
    await env.R2.delete(r2ObjectKey);
    throw error;
  }
}

function formatDate(value: string): string {
  return new Date(value).toLocaleString();
}

type ExportsLoaderData = {
  sources: ProcessedExportSource[];
  exports: Array<{ id: string; status: string; created_at: string }>;
};

type ExportsActionData = { error: string } | undefined;

export default function ExportsRoute() {
  const { sources, exports } = useLoaderData() as ExportsLoaderData;
  const actionData = useActionData() as ExportsActionData;

  return (
    <main>
      <p>
        <Link to="/dashboard">Back to dashboard</Link>
      </p>
      <h1>Exports</h1>

      <section>
        <h2>Create Markdown ZIP</h2>
        {actionData?.error ? <p role="alert">{actionData.error}</p> : null}
        {sources.length === 0 ? (
          <p>No processed sources are ready to export yet.</p>
        ) : (
          <Form method="post">
            <input type="hidden" name="intent" value="create-zip" />
            <fieldset>
              <legend>Processed sources</legend>
              {sources.map((source) => (
                <label key={sourceKey(source)} style={{ display: "block" }}>
                  <input type="checkbox" name="source" value={sourceKey(source)} /> {source.podcastTitle} / {source.title}
                </label>
              ))}
            </fieldset>
            <button type="submit">Create ZIP export</button>
          </Form>
        )}
      </section>

      <section>
        <h2>Recent exports</h2>
        {exports.length === 0 ? (
          <p>No exports yet.</p>
        ) : (
          <ul>
            {exports.map((exportRecord) => (
              <li key={exportRecord.id}>
                {formatDate(exportRecord.created_at)} · {exportRecord.status} ·{" "}
                <a href={`/exports?download=${encodeURIComponent(exportRecord.id)}`}>Download ZIP</a>
              </li>
            ))}
          </ul>
        )}
      </section>
    </main>
  );
}
