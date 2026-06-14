const SUPPORTED = new Set(["audio/mpeg", "audio/mp3", "audio/mp4", "audio/x-m4a", "audio/wav", "audio/wave"]);
const MAX_BYTES = 104_857_600;
const MAX_SECONDS = 7_200;

export function validateUploadMetadata(input: { contentType: string; sizeBytes: number; durationSeconds: number | null }) {
  if (!SUPPORTED.has(input.contentType)) return { ok: false as const, message: "Only mp3, m4a, and wav audio files are supported." };
  if (input.sizeBytes > MAX_BYTES) return { ok: false as const, message: "Audio file must be 100MB or smaller." };
  if (input.durationSeconds !== null && input.durationSeconds > MAX_SECONDS) return { ok: false as const, message: "Audio must be 2 hours or shorter." };
  return { ok: true as const };
}
