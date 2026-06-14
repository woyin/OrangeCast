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

const unsafeFilenameChars = /[/:*?"<>|]/g;

function sanitizePathPart(value: string): string {
  const sanitized = value.replace(unsafeFilenameChars, "-").trim();
  return sanitized.length > 0 ? sanitized : "Untitled";
}

export function exportPathForSource(input: ExportPathInput): string {
  return `${sanitizePathPart(input.podcastTitle)}/${sanitizePathPart(input.title)}.md`;
}

export async function buildMarkdownZip(sources: MarkdownExportSource[]): Promise<Uint8Array> {
  const zip = new JSZip();

  for (const source of sources) {
    zip.file(exportPathForSource(source), source.markdown);
  }

  return await zip.generateAsync({ type: "uint8array", compression: "DEFLATE" });
}
