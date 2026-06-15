import JSZip from "jszip";

export interface MarkdownExportSource {
  podcastTitle: string;
  title: string;
  markdown: string;
}

export interface ExportPathInput {
  podcastTitle: string;
  title: string;
}

const unsafeFilenameChars = /[\\/:*?"<>|\x00-\x1F\x7F]/g;
const dotOnly = /^\.+$/;

function sanitizeName(value: string, fallback: string): string {
  const sanitized = value.replace(unsafeFilenameChars, "-").trim();
  if (sanitized.length === 0 || dotOnly.test(sanitized)) return fallback;
  return sanitized;
}

function sanitizePathPart(value: string): string {
  return sanitizeName(value, "Untitled");
}

export function safeDownloadFilename(name: string, extension: string, fallbackName: string): string {
  const safeName = sanitizeName(name, fallbackName);
  const safeExtension = sanitizeName(extension.replace(/^\.+/, ""), "bin");
  return `${safeName}.${safeExtension}`;
}

export function exportPathForSource(input: ExportPathInput): string {
  return `${sanitizePathPart(input.podcastTitle)}/${sanitizePathPart(input.title)}.md`;
}

function uniqueZipPath(path: string, emittedPaths: Set<string>): string {
  const extensionIndex = path.toLowerCase().endsWith(".md") ? path.length - ".md".length : path.length;
  const basename = path.slice(0, extensionIndex);
  const extension = path.slice(extensionIndex);
  let candidate = path;
  let suffix = 2;

  while (emittedPaths.has(candidate)) {
    candidate = `${basename}-${suffix}${extension}`;
    suffix += 1;
  }

  emittedPaths.add(candidate);
  return candidate;
}

export async function buildMarkdownZip(sources: MarkdownExportSource[]): Promise<Uint8Array> {
  const zip = new JSZip();
  const emittedPaths = new Set<string>();

  for (const source of sources) {
    zip.file(uniqueZipPath(exportPathForSource(source), emittedPaths), source.markdown);
  }

  return await zip.generateAsync({ type: "uint8array", compression: "DEFLATE" });
}
