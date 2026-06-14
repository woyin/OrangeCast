import { describe, expect, it } from "vitest";
import {
  validateUploadFileSignature,
  validateUploadMetadata,
} from "../app/lib/services/upload-validation.server";

function bytes(values: number[]): Uint8Array {
  return new Uint8Array(values);
}

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

describe("validateUploadFileSignature", () => {
  it("accepts mp3 files with ID3 headers", () => {
    expect(validateUploadFileSignature("episode.mp3", bytes([0x49, 0x44, 0x33, 0x04]))).toEqual({
      ok: true,
    });
  });

  it("accepts mp3 files with MPEG frame headers", () => {
    expect(validateUploadFileSignature("episode.mp3", bytes([0xff, 0xfb, 0x90, 0x64]))).toEqual({
      ok: true,
    });
  });

  it("accepts wav files with RIFF WAVE headers", () => {
    expect(
      validateUploadFileSignature(
        "episode.wav",
        bytes([0x52, 0x49, 0x46, 0x46, 0x24, 0x00, 0x00, 0x00, 0x57, 0x41, 0x56, 0x45]),
      ),
    ).toEqual({ ok: true });
  });

  it("accepts m4a files with ftyp headers", () => {
    expect(
      validateUploadFileSignature(
        "episode.m4a",
        bytes([0x00, 0x00, 0x00, 0x20, 0x66, 0x74, 0x79, 0x70, 0x4d, 0x34, 0x41, 0x20]),
      ),
    ).toEqual({ ok: true });
  });

  it("rejects unsupported filename extensions", () => {
    expect(validateUploadFileSignature("episode.exe", bytes([0x49, 0x44, 0x33, 0x04]))).toEqual({
      ok: false,
      message: "Only mp3, m4a, and wav audio files are supported.",
    });
  });

  it("rejects files whose headers do not match their extension", () => {
    expect(validateUploadFileSignature("episode.mp3", bytes([0x4d, 0x5a, 0x90, 0x00]))).toEqual({
      ok: false,
      message: "Audio file contents do not match a supported mp3, m4a, or wav file.",
    });
  });
});
