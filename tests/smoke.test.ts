import { describe, expect, it } from "vitest";

describe("project scaffold", () => {
  it("runs TypeScript tests", () => {
    expect("CloudWisePod").toContain("Pod");
  });
});
