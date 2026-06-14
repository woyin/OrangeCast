const SUPPORTED = new Set(["audio/mpeg", "audio/mp3", "audio/mp4", "audio/x-m4a", "audio/wav", "audio/wave"]);
const MAX_BYTES = 104_857_600;
const MAX_SECONDS = 7_200;
const SUPPORTED_EXTENSION_MESSAGE = "Only mp3, m4a, and wav audio files are supported.";
const HEADER_MISMATCH_MESSAGE = "Audio file contents do not match a supported mp3, m4a, or wav file.";

export function validateUploadMetadata(input: { contentType: string; sizeBytes: number; durationSeconds: number | null }) {
  if (!SUPPORTED.has(input.contentType)) return { ok: false as const, message: SUPPORTED_EXTENSION_MESSAGE };
  if (input.sizeBytes > MAX_BYTES) return { ok: false as const, message: "Audio file must be 100MB or smaller." };
  if (input.durationSeconds !== null && input.durationSeconds > MAX_SECONDS) return { ok: false as const, message: "Audio must be 2 hours or shorter." };
  return { ok: true as const };
}

function extensionFor(filename: string): string | null {
  const lastDot = filename.lastIndexOf(".");
  if (lastDot < 0) return null;
  return filename.slice(lastDot).toLowerCase();
}

function startsWithAscii(bytes: Uint8Array, text: string, offset = 0): boolean {
  if (bytes.length < offset + text.length) return false;

  for (let index = 0; index < text.length; index += 1) {
    if (bytes[offset + index] !== text.charCodeAt(index)) return false;
  }

  return true;
}

function isMp3Header(bytes: Uint8Array): boolean {
  return (
    startsWithAscii(bytes, "ID3") ||
    (bytes.length >= 2 && bytes[0] === 0xff && (bytes[1] & 0xe0) === 0xe0)
  );
}

function isWavHeader(bytes: Uint8Array): boolean {
  return startsWithAscii(bytes, "RIFF") && startsWithAscii(bytes, "WAVE", 8);
}

function isM4aHeader(bytes: Uint8Array): boolean {
  return startsWithAscii(bytes, "ftyp", 4);
}

export function validateUploadFileSignature(filename: string, headerBytes: Uint8Array) {
  const extension = extensionFor(filename);
  if (![".mp3", ".m4a", ".wav"].includes(extension ?? "")) {
    return { ok: false as const, message: SUPPORTED_EXTENSION_MESSAGE };
  }

  if (extension === ".mp3" && isMp3Header(headerBytes)) return { ok: true as const };
  if (extension === ".wav" && isWavHeader(headerBytes)) return { ok: true as const };
  if (extension === ".m4a" && isM4aHeader(headerBytes)) return { ok: true as const };

  return { ok: false as const, message: HEADER_MISMATCH_MESSAGE };
}
