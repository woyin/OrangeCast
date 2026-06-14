import JSZip from "jszip";
import { describe, expect, test } from "vitest";
import { buildMarkdownZip, exportPathForSource } from "../app/lib/export/zip.server";

describe("exportPathForSource", () => {
  test("returns a safe markdown path scoped by podcast title", () => {
    expect(exportPathForSource({ podcastTitle: "Acme Show", title: "Hello/World" })).toBe(
      "Acme Show/Hello-World.md",
    );
  });

  test("replaces filename-unsafe characters with dashes", () => {
    expect(exportPathForSource({ podcastTitle: "A:B*C?", title: 'One"Two<Three>Four|Five' })).toBe(
      "A-B-C-/One-Two-Three-Four-Five.md",
    );
  });
});

describe("buildMarkdownZip", () => {
  test("adds one markdown file per selected source", async () => {
    const zipBytes = await buildMarkdownZip([
      { podcastTitle: "Acme Show", title: "Hello/World", markdown: "# Hello\n" },
      { podcastTitle: "Uploads", title: "Meeting:Notes", markdown: "# Meeting\n" },
    ]);

    const zip = await JSZip.loadAsync(zipBytes);

    await expect(zip.file("Acme Show/Hello-World.md")?.async("string")).resolves.toBe("# Hello\n");
    await expect(zip.file("Uploads/Meeting-Notes.md")?.async("string")).resolves.toBe("# Meeting\n");
  });
});
