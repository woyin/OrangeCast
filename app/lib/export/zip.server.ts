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

function uniqueZipPath(path: string, counts: Map<string, number>): string {
  const count = counts.get(path) ?? 0;
  counts.set(path, count + 1);
  if (count === 0) return path;

  const extensionIndex = path.toLowerCase().endsWith(".md") ? path.length - ".md".length : path.length;
  return `${path.slice(0, extensionIndex)}-${count + 1}${path.slice(extensionIndex)}`;
}

export async function buildMarkdownZip(sources: MarkdownExportSource[]): Promise<Uint8Array> {
  const zip = new JSZip();
  const pathCounts = new Map<string, number>();

  for (const source of sources) {
    zip.file(uniqueZipPath(exportPathForSource(source), pathCounts), source.markdown);
  }

  return await zip.generateAsync({ type: "uint8array", compression: "DEFLATE" });
}
