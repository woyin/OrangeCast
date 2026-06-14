import { describe, expect, test } from "vitest";
import { nextStatusForJob } from "../app/lib/services/processing.server";

describe("nextStatusForJob", () => {
  test("maps transcribe and analyze job states to source processing statuses", () => {
    expect(nextStatusForJob("transcribe", "running")).toBe("transcribing");
    expect(nextStatusForJob("transcribe", "succeeded")).toBe("transcribed");
    expect(nextStatusForJob("analyze", "running")).toBe("analyzing");
    expect(nextStatusForJob("analyze", "succeeded")).toBe("processed");
  });

  test("maps failed jobs to failed source processing status", () => {
    expect(nextStatusForJob("transcribe", "failed")).toBe("failed");
    expect(nextStatusForJob("analyze", "failed")).toBe("failed");
  });
});
