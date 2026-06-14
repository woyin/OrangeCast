import { json, redirect, type ActionFunctionArgs, type LoaderFunctionArgs } from "@remix-run/cloudflare";
import { Form, Link, useActionData, useLoaderData } from "@remix-run/react";
import { buildMarkdownZip } from "../lib/export/zip.server";
import { requireUserId } from "../lib/auth.server";
import { requireEnv } from "../lib/env.server";
import {
  createExportRecord,
  getExportForUser,
  listProcessedExportSources,
  listRecentExportsForUser,
  type ProcessedExportSource,
} from "../lib/repositories/exports.server";

function sourceKey(source: Pick<ProcessedExportSource, "sourceType" | "sourceId">): string {
  return `${source.sourceType}:${source.sourceId}`;
}

async function loadTextArtifact(bucket: R2Bucket, key: string): Promise<string> {
  const object = await bucket.get(key);
  if (!object) throw new Response("Export source artifact is not available", { status: 404 });
  return await object.text();
}

function zipDownloadResponse(bytes: ArrayBuffer, exportId: string): Response {
  return new Response(bytes, {
    headers: {
      "Content-Type": "application/zip",
      "Content-Disposition": `attachment; filename="cloudwisepod-export-${exportId}.zip"`,
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
    return zipDownloadResponse(await object.arrayBuffer(), exportRecord.id);
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

  const availableSources = await listProcessedExportSources(env.DB, userId);
  const selectedSources = availableSources.filter((source) => selectedKeys.has(sourceKey(source)));

  if (selectedSources.length === 0) {
    return json({ error: "No selected processed sources are available to export." }, { status: 400 });
  }

  const markdownSources = await Promise.all(
    selectedSources.map(async (source) => ({
      podcastTitle: source.podcastTitle,
      title: source.title,
      markdown: await loadTextArtifact(env.R2, source.markdownR2Key),
    })),
  );

  const exportRecord = await createExportRecord(env.DB, { userId });
  const zipBytes = await buildMarkdownZip(markdownSources);
  await env.R2.put(exportRecord.r2_object_key, zipBytes, {
    httpMetadata: { contentType: "application/zip" },
  });

  return redirect(`/exports?created=${encodeURIComponent(exportRecord.id)}`);
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
