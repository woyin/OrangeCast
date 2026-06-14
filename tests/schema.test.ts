import { describe, expect, it } from "vitest";
import { readFileSync } from "node:fs";
import { requireEnv, type AppEnv } from "../app/lib/env.server";

const schema = readFileSync("app/lib/schema.sql", "utf8");

const configuredEnv = {
  DB: {},
  R2: {},
  PROCESSING_QUEUE: {},
  APP_NAME: "CloudWisePod",
  SESSION_COOKIE_NAME: "cloudwise_session",
  UPLOAD_MAX_BYTES: "104857600",
  UPLOAD_MAX_SECONDS: "7200"
} as AppEnv;

describe("D1 schema", () => {
  it("adds user_id to user-owned tables", () => {
    for (const table of ["sessions", "podcasts", "episodes", "uploads", "processing_jobs", "transcripts", "analyses", "usage_records", "exports"]) {
      expect(schema).toContain(`CREATE TABLE IF NOT EXISTS ${table}`);
      const start = schema.indexOf(`CREATE TABLE IF NOT EXISTS ${table}`);
      const end = schema.indexOf(";", start);
      expect(schema.slice(start, end)).toContain("user_id TEXT NOT NULL");
    }
  });
});

describe("Cloudflare environment", () => {
  it("rejects missing required string vars", () => {
    for (const key of ["APP_NAME", "SESSION_COOKIE_NAME", "UPLOAD_MAX_BYTES", "UPLOAD_MAX_SECONDS"] satisfies Array<keyof AppEnv>) {
      const env = { ...configuredEnv, [key]: "" };

      expect(() => requireEnv({ env })).toThrow(Response);
    }
  });
});
