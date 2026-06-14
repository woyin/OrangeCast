import { json, redirect, type ActionFunctionArgs, type LoaderFunctionArgs } from "@remix-run/cloudflare";
import { Form, Link, useActionData } from "@remix-run/react";
import { requireUserId } from "../lib/auth.server";
import { newId } from "../lib/db.server";
import { requireEnv } from "../lib/env.server";
import { createUpload } from "../lib/repositories/uploads.server";
import {
  validateUploadFileSignature,
  validateUploadMetadata,
} from "../lib/services/upload-validation.server";

function sanitizeFilename(filename: string): string {
  const safe = filename
    .replace(/[\\/\u0000-\u001F\u007F]+/g, "_")
    .replace(/[^a-zA-Z0-9._ -]+/g, "_")
    .replace(/^\.+/, "")
    .trim();

  return safe.length > 0 ? safe : "audio";
}

function parseDurationSeconds(value: FormDataEntryValue | null): number | null | { error: string } {
  const raw = String(value || "").trim();
  if (!raw) return null;

  const minutes = Number(raw);
  if (!Number.isFinite(minutes) || minutes < 0) {
    return { error: "Duration must be a positive number of minutes." };
  }

  return Math.round(minutes * 60);
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
  const file = form.get("audioFile");

  if (!(file instanceof File) || file.size === 0) {
    return json({ error: "Choose an audio file to upload." }, { status: 400 });
  }

  const durationSeconds = parseDurationSeconds(form.get("durationMinutes"));
  if (typeof durationSeconds === "object" && durationSeconds !== null) {
    return json({ error: durationSeconds.error }, { status: 400 });
  }

  const validation = validateUploadMetadata({
    contentType: file.type,
    sizeBytes: file.size,
    durationSeconds,
  });
  if (!validation.ok) {
    return json({ error: validation.message }, { status: 400 });
  }

  const headerBytes = new Uint8Array(await file.slice(0, 16).arrayBuffer());
  const signatureValidation = validateUploadFileSignature(file.name || "audio", headerBytes);
  if (!signatureValidation.ok) {
    return json({ error: signatureValidation.message }, { status: 400 });
  }

  const uploadId = newId("upl");
  const safeFilename = sanitizeFilename(file.name || "audio");
  const r2ObjectKey = `users/${userId}/uploads/${uploadId}/${safeFilename}`;
  let r2PutSucceeded = false;

  try {
    await env.R2.put(r2ObjectKey, file.stream(), {
      httpMetadata: { contentType: file.type || "application/octet-stream" },
    });
    r2PutSucceeded = true;

    await createUpload(env.DB, {
      id: uploadId,
      userId,
      originalFilename: file.name || safeFilename,
      contentType: file.type,
      sizeBytes: file.size,
      durationSeconds,
      r2ObjectKey,
    });
  } catch (error) {
    if (r2PutSucceeded) {
      try {
        await env.R2.delete(r2ObjectKey);
      } catch (deleteError) {
        console.error("Failed to delete orphaned upload from R2", deleteError);
      }
    }

    console.error("Failed to store upload", error);
    return json({ error: "Upload could not be saved. Please try again." }, { status: 500 });
  }

  return redirect("/uploads");
}

export default function NewUpload() {
  const actionData = useActionData<typeof action>();

  return (
    <main>
      <h1>Upload audio</h1>
      {actionData?.error ? <p role="alert">{actionData.error}</p> : null}
      <Form method="post" encType="multipart/form-data">
        <label>
          Audio file
          <input name="audioFile" type="file" accept="audio/mpeg,audio/mp3,audio/mp4,audio/x-m4a,audio/wav,audio/wave" />
        </label>
        <label>
          Duration in minutes (optional)
          <input name="durationMinutes" type="number" min="0" step="0.1" />
        </label>
        <button type="submit">Upload</button>
      </Form>
      <p>
        <Link to="/uploads">Back to uploads</Link>
      </p>
    </main>
  );
}
