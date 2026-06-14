import { describe, expect, it } from "vitest";
import { normalizeEmail, validatePassword } from "../app/lib/auth.server";

describe("auth helpers", () => {
  it("normalizes email", () => {
    expect(normalizeEmail("  USER@Example.COM ")).toBe("user@example.com");
  });

  it("requires password length", () => {
    expect(validatePassword("short").ok).toBe(false);
    expect(validatePassword("long-enough-password").ok).toBe(true);
  });
});
