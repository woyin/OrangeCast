import { describe, expect, it } from "vitest";
import { validateUploadMetadata } from "../app/lib/services/upload-validation.server";

describe("validateUploadMetadata", () => {
  it("accepts mp3 under 100MB and 2 hours", () => {
    expect(
      validateUploadMetadata({
        contentType: "audio/mpeg",
        sizeBytes: 104_857_599,
        durationSeconds: 7_199,
      }),
    ).toEqual({ ok: true });
  });

  it("rejects unsupported video/mp4", () => {
    expect(
      validateUploadMetadata({
        contentType: "video/mp4",
        sizeBytes: 1_000,
        durationSeconds: 60,
      }),
    ).toEqual({
      ok: false,
      message: "Only mp3, m4a, and wav audio files are supported.",
    });
  });

  it("rejects files over 100MB", () => {
    expect(
      validateUploadMetadata({
        contentType: "audio/mpeg",
        sizeBytes: 104_857_601,
        durationSeconds: 60,
      }),
    ).toEqual({ ok: false, message: "Audio file must be 100MB or smaller." });
  });

  it("rejects audio over 2 hours", () => {
    expect(
      validateUploadMetadata({
        contentType: "audio/mpeg",
        sizeBytes: 1_000,
        durationSeconds: 7_201,
      }),
    ).toEqual({ ok: false, message: "Audio must be 2 hours or shorter." });
  });
});
