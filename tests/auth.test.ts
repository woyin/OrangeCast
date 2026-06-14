import { describe, expect, it } from "vitest";
import { normalizeEmail, validatePassword } from "../app/lib/auth.server";
import { requireEnv } from "../app/lib/env.server";
import { loader as logoutLoader } from "../app/routes/logout";

function fakeEnv(overrides: Record<string, unknown> = {}) {
  return {
    DB: {},
    R2: {},
    PROCESSING_QUEUE: {},
    APP_NAME: "CloudWisePod",
    SESSION_COOKIE_NAME: "cloudwise_session",
    SESSION_SECRET: "test-session-secret",
    UPLOAD_MAX_BYTES: "104857600",
    UPLOAD_MAX_SECONDS: "7200",
    ...overrides,
  };
}

describe("auth helpers", () => {
  it("normalizes email", () => {
    expect(normalizeEmail("  USER@Example.COM ")).toBe("user@example.com");
  });

  it("requires password length", () => {
    expect(validatePassword("short").ok).toBe(false);
    expect(validatePassword("long-enough-password").ok).toBe(true);
  });
});

describe("auth environment", () => {
  it("requires a session secret", () => {
    expect(() => requireEnv({ env: fakeEnv({ SESSION_SECRET: undefined }) as never })).toThrow(
      Response,
    );
  });
});

describe("logout route", () => {
  it("does not destroy the session from the loader", async () => {
    expect(logoutLoader()).toBeNull();
  });
});
