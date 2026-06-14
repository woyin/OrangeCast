import { describe, expect, it } from "vitest";
import { readFileSync } from "node:fs";

const schema = readFileSync("app/lib/schema.sql", "utf8");

describe("D1 schema", () => {
  it("adds user_id to user-owned tables", () => {
    for (const table of ["podcasts", "episodes", "uploads", "processing_jobs", "transcripts", "analyses", "usage_records", "exports"]) {
      expect(schema).toContain(`CREATE TABLE IF NOT EXISTS ${table}`);
      const start = schema.indexOf(`CREATE TABLE IF NOT EXISTS ${table}`);
      const end = schema.indexOf(";", start);
      expect(schema.slice(start, end)).toContain("user_id TEXT NOT NULL");
    }
  });
});
