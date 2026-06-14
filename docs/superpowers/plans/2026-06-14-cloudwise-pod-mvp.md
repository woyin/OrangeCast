# CloudWisePod MVP Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the first usable CloudWisePod MVP: authenticated multi-user Cloudflare app for RSS podcasts and controlled audio uploads, manual AI processing, transcript/analysis storage, single-episode Q&A, search, and Obsidian-friendly Markdown/zip export.

**Architecture:** Use a Remix/React Router app deployed on Cloudflare Pages/Workers. Keep route files thin and move business logic into service modules; store metadata in D1 and large artifacts in R2; run long work through Queues; access AI through provider interfaces with mock providers for tests and swappable production adapters.

**Tech Stack:** TypeScript, Remix/React Router, Vite, Cloudflare Workers/Pages, D1, R2, Queues, Vitest, Zod, Drizzle ORM or direct D1 SQL, bcrypt-compatible password hashing for Workers, cookie sessions, RSS parser, JSZip.

---

## Scope Decomposition

The product spec contains several subsystems. Implement them in this order so every phase leaves working, testable software:

1. Project scaffold and Cloudflare bindings.
2. D1 schema, repository layer, and ownership guards.
3. Authentication.
4. RSS podcast ingestion.
5. Controlled audio uploads.
6. Processing job state machine with mock providers.
7. Transcript and analysis artifacts in R2.
8. Knowledge card UI.
9. Search.
10. Single-episode Q&A.
11. Markdown and zip export.
12. Production provider adapters and deployment hardening.

This plan intentionally starts with mock AI providers. Real provider adapters are added only after the internal contracts, storage, retries, and UI are stable.

## Planned File Structure

Create or modify these files:

- `package.json`: scripts and dependencies.
- `vite.config.ts`: Vite/Vitest config.
- `tsconfig.json`: TypeScript config.
- `wrangler.toml`: Cloudflare resources and bindings.
- `app/root.tsx`: app shell.
- `app/routes/_index.tsx`: redirect/dashboard entry.
- `app/routes/login.tsx`, `app/routes/register.tsx`, `app/routes/logout.tsx`: auth routes.
- `app/routes/dashboard.tsx`: main dashboard.
- `app/routes/podcasts._index.tsx`, `app/routes/podcasts.new.tsx`, `app/routes/podcasts.$podcastId.tsx`: podcast flows.
- `app/routes/uploads._index.tsx`, `app/routes/uploads.new.tsx`: upload flows.
- `app/routes/sources.$sourceType.$sourceId.tsx`: unified processed source detail.
- `app/routes/search.tsx`: search UI.
- `app/routes/exports.tsx`: export UI and downloads.
- `app/lib/env.server.ts`: typed Cloudflare binding access.
- `app/lib/auth.server.ts`: session, password, current-user helpers.
- `app/lib/db.server.ts`: D1 database wrapper.
- `app/lib/schema.sql`: D1 schema.
- `app/lib/repositories/*.server.ts`: focused repository modules.
- `app/lib/services/*.server.ts`: business services.
- `app/lib/providers/*.server.ts`: provider interfaces and mock/production adapters.
- `app/lib/export/*.server.ts`: Markdown and zip rendering.
- `app/lib/search/*.server.ts`: search indexing/query helpers.
- `app/lib/queue/*.server.ts`: queue message types and worker handlers.
- `app/worker.ts`: Cloudflare queue and cron entrypoint if the chosen deployment needs a separate worker entry.
- `tests/**/*.test.ts`: unit and integration tests.
- `docs/superpowers/specs/2026-06-14-cloudwise-pod-design.md`: source design, already committed.

---

## Task 1: Scaffold Remix/React Router Cloudflare Project

**Files:**
- Create: `package.json`
- Create: `tsconfig.json`
- Create: `vite.config.ts`
- Create: `app/root.tsx`
- Create: `app/routes/_index.tsx`
- Create: `app/routes/dashboard.tsx`
- Create: `tests/smoke.test.ts`

- [ ] **Step 1: Write the initial smoke test**

Create `tests/smoke.test.ts`:

```ts
import { describe, expect, it } from "vitest";

describe("project scaffold", () => {
  it("runs TypeScript tests", () => {
    expect("CloudWisePod").toContain("Pod");
  });
});
```

- [ ] **Step 2: Add project metadata and scripts**

Create `package.json`:

```json
{
  "name": "cloudwise-pod",
  "private": true,
  "type": "module",
  "scripts": {
    "dev": "vite",
    "build": "vite build",
    "typecheck": "tsc --noEmit",
    "test": "vitest run",
    "test:watch": "vitest",
    "cf:dev": "wrangler pages dev ./build/client",
    "db:migrate:local": "wrangler d1 execute cloudwise_pod --local --file=app/lib/schema.sql"
  },
  "dependencies": {
    "@cloudflare/workers-types": "latest",
    "@remix-run/cloudflare": "latest",
    "@remix-run/react": "latest",
    "@remix-run/serve": "latest",
    "isbot": "latest",
    "jszip": "latest",
    "react": "latest",
    "react-dom": "latest",
    "zod": "latest"
  },
  "devDependencies": {
    "@remix-run/dev": "latest",
    "@testing-library/react": "latest",
    "@types/react": "latest",
    "@types/react-dom": "latest",
    "typescript": "latest",
    "vite": "latest",
    "vitest": "latest",
    "wrangler": "latest"
  }
}
```

- [ ] **Step 3: Add TypeScript and Vite config**

Create `tsconfig.json`:

```json
{
  "include": ["app", "tests", "vite.config.ts"],
  "compilerOptions": {
    "lib": ["DOM", "DOM.Iterable", "ES2022"],
    "types": ["@cloudflare/workers-types", "vitest/globals"],
    "isolatedModules": true,
    "esModuleInterop": true,
    "jsx": "react-jsx",
    "module": "ESNext",
    "moduleResolution": "Bundler",
    "resolveJsonModule": true,
    "target": "ES2022",
    "strict": true,
    "skipLibCheck": true,
    "noEmit": true
  }
}
```

Create `vite.config.ts`:

```ts
import { defineConfig } from "vite";
import { vitePlugin as remix } from "@remix-run/dev";

export default defineConfig({
  plugins: [remix()],
  test: {
    environment: "node",
    include: ["tests/**/*.test.ts", "tests/**/*.test.tsx"]
  }
});
```

- [ ] **Step 4: Add minimal routes**

Create `app/root.tsx`:

```tsx
import { Links, Meta, Outlet, Scripts, ScrollRestoration } from "@remix-run/react";

export default function App() {
  return (
    <html lang="en">
      <head>
        <meta charSet="utf-8" />
        <meta name="viewport" content="width=device-width, initial-scale=1" />
        <Meta />
        <Links />
      </head>
      <body>
        <Outlet />
        <ScrollRestoration />
        <Scripts />
      </body>
    </html>
  );
}
```

Create `app/routes/_index.tsx`:

```tsx
import { redirect } from "@remix-run/cloudflare";

export async function loader() {
  return redirect("/dashboard");
}
```

Create `app/routes/dashboard.tsx`:

```tsx
export default function Dashboard() {
  return (
    <main>
      <h1>CloudWisePod</h1>
      <p>Podcast knowledge cards on Cloudflare.</p>
    </main>
  );
}
```

- [ ] **Step 5: Verify scaffold**

Run:

```bash
npm install
npm test
npm run typecheck
```

Expected: tests pass and TypeScript reports no errors.

- [ ] **Step 6: Commit**

```bash
git add package.json package-lock.json tsconfig.json vite.config.ts app tests
git commit -m "chore: scaffold CloudWisePod app"
```

---

## Task 2: Add Cloudflare Bindings and D1 Schema

**Files:**
- Create: `wrangler.toml`
- Create: `app/lib/schema.sql`
- Create: `app/lib/env.server.ts`
- Create: `app/lib/db.server.ts`
- Create: `tests/schema.test.ts`

- [ ] **Step 1: Write schema invariants test**

Create `tests/schema.test.ts`:

```ts
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
```

- [ ] **Step 2: Add Cloudflare bindings**

Create `wrangler.toml`:

```toml
name = "cloudwise-pod"
compatibility_date = "2026-06-14"
pages_build_output_dir = "build/client"

[[d1_databases]]
binding = "DB"
database_name = "cloudwise_pod"
database_id = "replace-with-cloudflare-d1-id"

[[r2_buckets]]
binding = "R2"
bucket_name = "cloudwise-pod"

[[queues.producers]]
binding = "PROCESSING_QUEUE"
queue = "cloudwise-pod-processing"

[[queues.consumers]]
queue = "cloudwise-pod-processing"
max_batch_size = 1
max_batch_timeout = 30

[vars]
APP_NAME = "CloudWisePod"
SESSION_COOKIE_NAME = "cloudwise_session"
UPLOAD_MAX_BYTES = "104857600"
UPLOAD_MAX_SECONDS = "7200"
```

- [ ] **Step 3: Add D1 schema**

Create `app/lib/schema.sql` with the tables from the spec. Use TEXT ids generated by the app, ISO datetime strings, and indexes for ownership and source lookup.

```sql
CREATE TABLE IF NOT EXISTS users (
  id TEXT PRIMARY KEY,
  email TEXT NOT NULL UNIQUE,
  password_hash TEXT NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS sessions (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL,
  expires_at TEXT NOT NULL,
  created_at TEXT NOT NULL,
  FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS podcasts (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL,
  feed_url TEXT NOT NULL,
  title TEXT NOT NULL,
  description TEXT,
  image_url TEXT,
  site_url TEXT,
  last_fetched_at TEXT,
  created_at TEXT NOT NULL,
  FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
  UNIQUE(user_id, feed_url)
);

CREATE TABLE IF NOT EXISTS episodes (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL,
  podcast_id TEXT NOT NULL,
  guid TEXT NOT NULL,
  title TEXT NOT NULL,
  description TEXT,
  audio_url TEXT NOT NULL,
  duration_seconds INTEGER,
  published_at TEXT,
  processing_status TEXT NOT NULL DEFAULT 'unprocessed',
  created_at TEXT NOT NULL,
  FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
  FOREIGN KEY (podcast_id) REFERENCES podcasts(id) ON DELETE CASCADE,
  UNIQUE(user_id, podcast_id, guid)
);

CREATE TABLE IF NOT EXISTS uploads (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL,
  original_filename TEXT NOT NULL,
  content_type TEXT NOT NULL,
  size_bytes INTEGER NOT NULL,
  duration_seconds INTEGER,
  r2_object_key TEXT NOT NULL,
  processing_status TEXT NOT NULL DEFAULT 'unprocessed',
  created_at TEXT NOT NULL,
  FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS processing_jobs (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL,
  source_type TEXT NOT NULL,
  source_id TEXT NOT NULL,
  job_type TEXT NOT NULL,
  status TEXT NOT NULL,
  attempt_count INTEGER NOT NULL DEFAULT 0,
  error_message TEXT,
  provider TEXT,
  model TEXT,
  started_at TEXT,
  finished_at TEXT,
  created_at TEXT NOT NULL,
  FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS transcripts (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL,
  source_type TEXT NOT NULL,
  source_id TEXT NOT NULL,
  language TEXT,
  provider TEXT NOT NULL,
  model TEXT NOT NULL,
  text_r2_key TEXT NOT NULL,
  segments_r2_key TEXT NOT NULL,
  duration_seconds INTEGER,
  created_at TEXT NOT NULL,
  FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
  UNIQUE(user_id, source_type, source_id)
);

CREATE TABLE IF NOT EXISTS analyses (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL,
  source_type TEXT NOT NULL,
  source_id TEXT NOT NULL,
  provider TEXT NOT NULL,
  model TEXT NOT NULL,
  title TEXT NOT NULL,
  summary TEXT NOT NULL,
  content_json_r2_key TEXT NOT NULL,
  markdown_r2_key TEXT NOT NULL,
  created_at TEXT NOT NULL,
  FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
  UNIQUE(user_id, source_type, source_id)
);

CREATE TABLE IF NOT EXISTS usage_records (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL,
  source_type TEXT,
  source_id TEXT,
  provider TEXT NOT NULL,
  model TEXT NOT NULL,
  operation TEXT NOT NULL,
  input_units INTEGER NOT NULL DEFAULT 0,
  output_units INTEGER NOT NULL DEFAULT 0,
  estimated_cost REAL NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL,
  FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS exports (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL,
  export_type TEXT NOT NULL,
  r2_object_key TEXT NOT NULL,
  status TEXT NOT NULL,
  expires_at TEXT,
  created_at TEXT NOT NULL,
  FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_episodes_user_status ON episodes(user_id, processing_status);
CREATE INDEX IF NOT EXISTS idx_uploads_user_status ON uploads(user_id, processing_status);
CREATE INDEX IF NOT EXISTS idx_jobs_user_status ON processing_jobs(user_id, status);
CREATE INDEX IF NOT EXISTS idx_transcripts_source ON transcripts(user_id, source_type, source_id);
CREATE INDEX IF NOT EXISTS idx_analyses_source ON analyses(user_id, source_type, source_id);
```

- [ ] **Step 4: Add typed environment helpers**

Create `app/lib/env.server.ts`:

```ts
export interface AppEnv {
  DB: D1Database;
  R2: R2Bucket;
  PROCESSING_QUEUE: Queue;
  APP_NAME: string;
  SESSION_COOKIE_NAME: string;
  UPLOAD_MAX_BYTES: string;
  UPLOAD_MAX_SECONDS: string;
  OPENAI_API_KEY?: string;
}

export function requireEnv(context: { cloudflare?: { env: AppEnv } } | { env?: AppEnv }): AppEnv {
  const env = "cloudflare" in context ? context.cloudflare?.env : context.env;
  if (!env?.DB || !env?.R2 || !env?.PROCESSING_QUEUE) {
    throw new Response("Cloudflare bindings are not configured", { status: 500 });
  }
  return env;
}
```

Create `app/lib/db.server.ts`:

```ts
export type Db = D1Database;

export function nowIso(): string {
  return new Date().toISOString();
}

export function newId(prefix: string): string {
  return `${prefix}_${crypto.randomUUID()}`;
}
```

- [ ] **Step 5: Verify schema task**

Run:

```bash
npm test -- tests/schema.test.ts
npm run typecheck
```

Expected: schema test passes and TypeScript reports no errors.

- [ ] **Step 6: Commit**

```bash
git add wrangler.toml app/lib/schema.sql app/lib/env.server.ts app/lib/db.server.ts tests/schema.test.ts
git commit -m "feat: add Cloudflare bindings and D1 schema"
```

---

## Task 3: Authentication and Ownership Foundation

**Files:**
- Create: `app/lib/auth.server.ts`
- Create: `app/lib/repositories/users.server.ts`
- Create: `app/routes/register.tsx`
- Create: `app/routes/login.tsx`
- Create: `app/routes/logout.tsx`
- Modify: `app/routes/dashboard.tsx`
- Create: `tests/auth.test.ts`

- [ ] **Step 1: Write auth unit tests**

Create `tests/auth.test.ts`:

```ts
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
```

- [ ] **Step 2: Implement auth helpers**

Create `app/lib/auth.server.ts`:

```ts
import { createCookieSessionStorage, redirect } from "@remix-run/cloudflare";
import type { AppEnv } from "./env.server";

export function normalizeEmail(email: string): string {
  return email.trim().toLowerCase();
}

export function validatePassword(password: string): { ok: true } | { ok: false; message: string } {
  if (password.length < 12) return { ok: false, message: "Password must be at least 12 characters." };
  return { ok: true };
}

export async function hashPassword(password: string): Promise<string> {
  const data = new TextEncoder().encode(password);
  const digest = await crypto.subtle.digest("SHA-256", data);
  return `sha256:${Array.from(new Uint8Array(digest)).map((b) => b.toString(16).padStart(2, "0")).join("")}`;
}

export async function verifyPassword(password: string, hash: string): Promise<boolean> {
  return hash === await hashPassword(password);
}

export function sessionStorage(env: AppEnv) {
  return createCookieSessionStorage({
    cookie: {
      name: env.SESSION_COOKIE_NAME || "cloudwise_session",
      httpOnly: true,
      path: "/",
      sameSite: "lax",
      secure: true,
      secrets: [env.SESSION_SECRET || "dev-only-secret-change-me"]
    }
  });
}

export async function requireUserId(request: Request, env: AppEnv): Promise<string> {
  const storage = sessionStorage(env);
  const session = await storage.getSession(request.headers.get("Cookie"));
  const userId = session.get("userId");
  if (typeof userId !== "string") throw redirect("/login");
  return userId;
}
```

Add `SESSION_SECRET` to `AppEnv` in `app/lib/env.server.ts` as optional string.

- [ ] **Step 3: Implement user repository**

Create `app/lib/repositories/users.server.ts`:

```ts
import { newId, nowIso, type Db } from "../db.server";

export interface UserRecord {
  id: string;
  email: string;
  password_hash: string;
  created_at: string;
  updated_at: string;
}

export async function createUser(db: Db, email: string, passwordHash: string): Promise<UserRecord> {
  const user: UserRecord = {
    id: newId("usr"),
    email,
    password_hash: passwordHash,
    created_at: nowIso(),
    updated_at: nowIso()
  };
  await db.prepare(
    `INSERT INTO users (id, email, password_hash, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`
  ).bind(user.id, user.email, user.password_hash, user.created_at, user.updated_at).run();
  return user;
}

export async function findUserByEmail(db: Db, email: string): Promise<UserRecord | null> {
  return await db.prepare(`SELECT * FROM users WHERE email = ?`).bind(email).first<UserRecord>();
}

export async function findUserById(db: Db, id: string): Promise<UserRecord | null> {
  return await db.prepare(`SELECT * FROM users WHERE id = ?`).bind(id).first<UserRecord>();
}
```

- [ ] **Step 4: Add register/login/logout routes**

Implement routes that call `requireEnv`, `createUser`, `findUserByEmail`, `hashPassword`, `verifyPassword`, and set `session.set("userId", user.id)`. On success redirect to `/dashboard`; on failure render a plain form with an error message. Keep route UI minimal until the data flow is working.

Use this action pattern in both `register.tsx` and `login.tsx`:

```ts
const form = await request.formData();
const email = normalizeEmail(String(form.get("email") || ""));
const password = String(form.get("password") || "");
```

- [ ] **Step 5: Protect dashboard**

Modify `app/routes/dashboard.tsx` with a loader that calls `requireUserId(request, env)` and returns the user id.

- [ ] **Step 6: Verify auth**

Run:

```bash
npm test -- tests/auth.test.ts
npm run typecheck
```

Expected: auth helper tests pass and protected route typechecks.

- [ ] **Step 7: Commit**

```bash
git add app/lib/auth.server.ts app/lib/repositories/users.server.ts app/routes/register.tsx app/routes/login.tsx app/routes/logout.tsx app/routes/dashboard.tsx tests/auth.test.ts app/lib/env.server.ts
git commit -m "feat: add authentication foundation"
```

---

## Task 4: RSS Podcast Ingestion

**Files:**
- Create: `app/lib/services/rss-parser.server.ts`
- Create: `app/lib/repositories/podcasts.server.ts`
- Create: `app/lib/repositories/episodes.server.ts`
- Create: `app/routes/podcasts._index.tsx`
- Create: `app/routes/podcasts.new.tsx`
- Create: `app/routes/podcasts.$podcastId.tsx`
- Create: `tests/rss-parser.test.ts`

- [ ] **Step 1: Write RSS parser test**

Create `tests/rss-parser.test.ts` with a fixture string containing one `<item>` with `<enclosure url="https://example.com/a.mp3" type="audio/mpeg" />`. Assert parser returns podcast title, site URL, episode guid, title, audio URL, and publication date.

- [ ] **Step 2: Implement parser**

Create `app/lib/services/rss-parser.server.ts` using `DOMParser` available in Workers or a small XML parser dependency if DOMParser is not available in tests. Export:

```ts
export interface ParsedPodcast { title: string; description: string | null; siteUrl: string | null; imageUrl: string | null; episodes: ParsedEpisode[]; }
export interface ParsedEpisode { guid: string; title: string; description: string | null; audioUrl: string; durationSeconds: number | null; publishedAt: string | null; }
export function parsePodcastRss(xml: string): ParsedPodcast { /* parse and validate */ }
```

The function must skip items without audio enclosure and throw `Error("RSS feed has no playable audio episodes")` when no playable item exists.

- [ ] **Step 3: Implement repositories**

Create `podcasts.server.ts` with `upsertPodcastForUser`, `listPodcastsForUser`, `getPodcastForUser`.

Create `episodes.server.ts` with `upsertEpisodesForPodcast`, `listEpisodesForPodcast`, `getSourceEpisodeForUser`, `updateEpisodeStatus`.

Every query must include `user_id = ?`.

- [ ] **Step 4: Add podcast routes**

`podcasts.new.tsx` action:

1. Require user.
2. Read `feedUrl`.
3. Fetch URL.
4. Parse RSS.
5. Upsert podcast and episodes.
6. Redirect to `/podcasts/{podcastId}`.

`podcasts._index.tsx` lists user podcasts. `podcasts.$podcastId.tsx` lists episodes and shows a disabled/placeholder “Process” button until Task 6 adds queueing.

- [ ] **Step 5: Verify RSS**

Run:

```bash
npm test -- tests/rss-parser.test.ts
npm run typecheck
```

Expected: parser tests pass and repository/routes typecheck.

- [ ] **Step 6: Commit**

```bash
git add app/lib/services/rss-parser.server.ts app/lib/repositories/podcasts.server.ts app/lib/repositories/episodes.server.ts app/routes/podcasts.* tests/rss-parser.test.ts
git commit -m "feat: add RSS podcast ingestion"
```

---

## Task 5: Controlled Audio Uploads

**Files:**
- Create: `app/lib/services/upload-validation.server.ts`
- Create: `app/lib/repositories/uploads.server.ts`
- Create: `app/routes/uploads._index.tsx`
- Create: `app/routes/uploads.new.tsx`
- Create: `tests/upload-validation.test.ts`

- [ ] **Step 1: Write upload validation tests**

Create `tests/upload-validation.test.ts`:

```ts
import { describe, expect, it } from "vitest";
import { validateUploadMetadata } from "../app/lib/services/upload-validation.server";

describe("upload validation", () => {
  it("accepts mp3 under 100MB and 2 hours", () => {
    expect(validateUploadMetadata({ contentType: "audio/mpeg", sizeBytes: 1_000_000, durationSeconds: 300 }).ok).toBe(true);
  });

  it("rejects unsupported content type", () => {
    const result = validateUploadMetadata({ contentType: "video/mp4", sizeBytes: 1_000_000, durationSeconds: 300 });
    expect(result).toEqual({ ok: false, message: "Only mp3, m4a, and wav audio files are supported." });
  });

  it("rejects files over 100MB", () => {
    expect(validateUploadMetadata({ contentType: "audio/mpeg", sizeBytes: 104_857_601, durationSeconds: 300 }).ok).toBe(false);
  });

  it("rejects audio over 2 hours", () => {
    expect(validateUploadMetadata({ contentType: "audio/mpeg", sizeBytes: 1_000_000, durationSeconds: 7201 }).ok).toBe(false);
  });
});
```

- [ ] **Step 2: Implement validation**

Create `app/lib/services/upload-validation.server.ts`:

```ts
const SUPPORTED = new Set(["audio/mpeg", "audio/mp3", "audio/mp4", "audio/x-m4a", "audio/wav", "audio/wave"]);
const MAX_BYTES = 104_857_600;
const MAX_SECONDS = 7_200;

export function validateUploadMetadata(input: { contentType: string; sizeBytes: number; durationSeconds: number | null }) {
  if (!SUPPORTED.has(input.contentType)) return { ok: false as const, message: "Only mp3, m4a, and wav audio files are supported." };
  if (input.sizeBytes > MAX_BYTES) return { ok: false as const, message: "Audio file must be 100MB or smaller." };
  if (input.durationSeconds !== null && input.durationSeconds > MAX_SECONDS) return { ok: false as const, message: "Audio must be 2 hours or shorter." };
  return { ok: true as const };
}
```

- [ ] **Step 3: Implement upload repository and routes**

Create repository functions: `createUpload`, `listUploadsForUser`, `getUploadForUser`, `updateUploadStatus`.

Route `uploads.new.tsx` should accept a `File`, validate metadata, write to R2 key `users/{userId}/uploads/{uploadId}/{safeFilename}`, then insert upload metadata. Duration can be user-supplied in minutes for first version because Workers should not decode audio.

- [ ] **Step 4: Verify upload flow**

Run:

```bash
npm test -- tests/upload-validation.test.ts
npm run typecheck
```

Expected: validation tests pass and upload routes typecheck.

- [ ] **Step 5: Commit**

```bash
git add app/lib/services/upload-validation.server.ts app/lib/repositories/uploads.server.ts app/routes/uploads.* tests/upload-validation.test.ts
git commit -m "feat: add controlled audio uploads"
```

---

## Task 6: Processing Jobs, Queue Messages, and Mock Providers

**Files:**
- Create: `app/lib/providers/types.server.ts`
- Create: `app/lib/providers/mock.server.ts`
- Create: `app/lib/repositories/jobs.server.ts`
- Create: `app/lib/services/processing.server.ts`
- Create: `app/lib/queue/messages.server.ts`
- Create: `app/lib/queue/processing-worker.server.ts`
- Modify: podcast/upload/source routes to add Process action
- Create: `tests/processing-state.test.ts`

- [ ] **Step 1: Write state transition tests**

Create `tests/processing-state.test.ts` asserting that:

- `nextStatusForJob("transcribe", "running")` returns `transcribing`.
- `nextStatusForJob("transcribe", "succeeded")` returns `transcribed`.
- `nextStatusForJob("analyze", "running")` returns `analyzing`.
- `nextStatusForJob("analyze", "succeeded")` returns `processed`.
- failed jobs return `failed`.

- [ ] **Step 2: Define provider contracts**

Create `app/lib/providers/types.server.ts`:

```ts
export interface TranscriptSegment { startSeconds: number; endSeconds: number; text: string; }
export interface TranscriptionResult { text: string; segments: TranscriptSegment[]; language: string | null; durationSeconds: number | null; provider: string; model: string; }
export interface KnowledgeCard { title: string; summary: string; keyPoints: string[]; chapters: { title: string; startSeconds: number; endSeconds: number; summary: string }[]; quotes: { text: string; startSeconds: number | null }[]; entities: { name: string; type: string }[]; actionItems: string[]; glossary: { term: string; definition: string }[]; suggestedQuestions: string[]; tags: string[]; }
export interface AnalysisResult { card: KnowledgeCard; provider: string; model: string; }
export interface TranscriptionProvider { transcribe(input: { audioUrl: string; sourceTitle: string }): Promise<TranscriptionResult>; }
export interface AnalysisProvider { analyze(input: { title: string; transcript: string; segments: TranscriptSegment[] }): Promise<AnalysisResult>; }
export interface ChatProvider { answer(input: { question: string; title: string; transcript: string; analysis: KnowledgeCard }): Promise<{ answer: string; citations: { startSeconds: number | null; text: string }[]; provider: string; model: string }>; }
```

- [ ] **Step 3: Implement mock providers**

Create `mock.server.ts` returning deterministic transcript, analysis, and answer values so tests and UI can run without AI credentials.

- [ ] **Step 4: Implement job repository and processing service**

`jobs.server.ts` exports `createJob`, `markJobRunning`, `markJobSucceeded`, `markJobFailed`, `findPendingJob`.

`processing.server.ts` exports `enqueueProcessingForSource(env, db, userId, sourceType, sourceId)` which creates a transcribe job, updates source status to `queued`, and sends a queue message.

- [ ] **Step 5: Add Process actions to routes**

On episode/upload list and detail pages, add a form posting `intent=process`, `sourceType`, and `sourceId`. The action must require ownership before queueing.

- [ ] **Step 6: Verify processing contracts**

Run:

```bash
npm test -- tests/processing-state.test.ts
npm run typecheck
```

Expected: state tests pass and queue message types typecheck.

- [ ] **Step 7: Commit**

```bash
git add app/lib/providers app/lib/repositories/jobs.server.ts app/lib/services/processing.server.ts app/lib/queue app/routes tests/processing-state.test.ts
git commit -m "feat: add processing jobs and mock providers"
```

---

## Task 7: R2 Transcript and Analysis Artifacts

**Files:**
- Create: `app/lib/repositories/transcripts.server.ts`
- Create: `app/lib/repositories/analyses.server.ts`
- Create: `app/lib/services/artifacts.server.ts`
- Create: `app/lib/export/markdown.server.ts`
- Create: `tests/markdown-renderer.test.ts`

- [ ] **Step 1: Write Markdown renderer test**

Create a test that renders a card with title `Better Thinking`, tag `podcast`, entity `Naval`, and transcript appendix disabled. Assert output contains YAML frontmatter, `#podcast`, `[[Naval]]`, `## Summary`, and `## Key Points`.

- [ ] **Step 2: Implement artifact service**

Create helpers:

```ts
export function transcriptTextKey(userId: string, sourceType: string, sourceId: string) { return `users/${userId}/transcripts/${sourceType}/${sourceId}/text.txt`; }
export function transcriptSegmentsKey(userId: string, sourceType: string, sourceId: string) { return `users/${userId}/transcripts/${sourceType}/${sourceId}/segments.json`; }
export function analysisJsonKey(userId: string, sourceType: string, sourceId: string) { return `users/${userId}/analyses/${sourceType}/${sourceId}/content.json`; }
export function analysisMarkdownKey(userId: string, sourceType: string, sourceId: string) { return `users/${userId}/analyses/${sourceType}/${sourceId}/note.md`; }
```

- [ ] **Step 3: Implement Markdown renderer**

`renderKnowledgeCardMarkdown(card, metadata, options)` must output YAML frontmatter, tags, Obsidian double links for entities, structured sections, and optional transcript appendix.

- [ ] **Step 4: Wire queue worker**

`processing-worker.server.ts` should execute transcribe job, write transcript artifacts to R2, insert transcript metadata, enqueue analyze job, execute analysis job, write JSON and Markdown artifacts to R2, insert analysis metadata, and update source status.

- [ ] **Step 5: Verify artifacts**

Run:

```bash
npm test -- tests/markdown-renderer.test.ts
npm run typecheck
```

Expected: Markdown renderer test passes and worker typechecks.

- [ ] **Step 6: Commit**

```bash
git add app/lib/repositories/transcripts.server.ts app/lib/repositories/analyses.server.ts app/lib/services/artifacts.server.ts app/lib/export/markdown.server.ts app/lib/queue/processing-worker.server.ts tests/markdown-renderer.test.ts
git commit -m "feat: store transcripts and knowledge cards in R2"
```

---

## Task 8: Source Detail and Knowledge Card UI

**Files:**
- Create: `app/routes/sources.$sourceType.$sourceId.tsx`
- Modify: `app/routes/podcasts.$podcastId.tsx`
- Modify: `app/routes/uploads._index.tsx`

- [ ] **Step 1: Add detail loader**

Implement loader for `/sources/:sourceType/:sourceId` that:

1. Requires user.
2. Validates `sourceType` is `episode` or `upload`.
3. Loads source with ownership guard.
4. Loads transcript metadata and analysis metadata.
5. Reads analysis JSON and transcript segments from R2 only if they exist.

- [ ] **Step 2: Render source states**

Render clear states for `unprocessed`, `queued`, `transcribing`, `transcribed`, `analyzing`, `processed`, and `failed`. For `processed`, render all knowledge card sections and transcript segments.

- [ ] **Step 3: Link lists to detail page**

Episode and upload lists should link each row to `/sources/episode/{id}` or `/sources/upload/{id}`.

- [ ] **Step 4: Verify UI typecheck**

Run:

```bash
npm run typecheck
```

Expected: route loaders/actions and JSX typecheck.

- [ ] **Step 5: Commit**

```bash
git add app/routes/sources.\$sourceType.\$sourceId.tsx app/routes/podcasts.\$podcastId.tsx app/routes/uploads._index.tsx
git commit -m "feat: add source detail knowledge card UI"
```

---

## Task 9: Search

**Files:**
- Create: `app/lib/search/index.server.ts`
- Create: `app/routes/search.tsx`
- Create: `tests/search.test.ts`

- [ ] **Step 1: Write search ranking test**

Create `tests/search.test.ts` for a pure `scoreSearchResult` function. Assert title match scores higher than summary match, and no match scores 0.

- [ ] **Step 2: Implement search helper**

Create `scoreSearchResult(query, fields)` and a D1-backed `searchUserContent(db, userId, query)` that searches episode/upload titles and analysis title/summary first. Transcript full-text indexing can be added after MVP if D1 FTS setup is not ready; the route must state “Transcript body search is enabled after transcript indexing is configured” instead of silently failing.

- [ ] **Step 3: Add search route**

`/search?q=...` requires auth, displays search form, results with source type, title, summary snippet, and detail link.

- [ ] **Step 4: Verify search**

Run:

```bash
npm test -- tests/search.test.ts
npm run typecheck
```

Expected: search unit test passes and route typechecks.

- [ ] **Step 5: Commit**

```bash
git add app/lib/search/index.server.ts app/routes/search.tsx tests/search.test.ts
git commit -m "feat: add basic content search"
```

---

## Task 10: Single-Episode Q&A

**Files:**
- Create: `app/lib/services/qa.server.ts`
- Modify: `app/routes/sources.$sourceType.$sourceId.tsx`
- Create: `tests/qa.test.ts`

- [ ] **Step 1: Write context selection test**

Create `tests/qa.test.ts` asserting `selectRelevantTranscriptChunks("pricing", segments)` returns chunks containing the word `pricing` before unrelated chunks.

- [ ] **Step 2: Implement QA service**

Create `qa.server.ts` with:

```ts
export function selectRelevantTranscriptChunks(question: string, segments: TranscriptSegment[], maxChars = 12000): TranscriptSegment[] { /* keyword overlap ranking */ }
export async function answerSourceQuestion(input: { provider: ChatProvider; question: string; title: string; transcriptText: string; segments: TranscriptSegment[]; analysis: KnowledgeCard }) { /* select chunks and call provider */ }
```

- [ ] **Step 3: Add Q&A action and UI**

On the source detail route, add a form named `intent=ask`. The action loads the user-owned source, analysis, and transcript artifacts, calls `answerSourceQuestion`, and renders the answer plus citations on the same page.

- [ ] **Step 4: Verify Q&A**

Run:

```bash
npm test -- tests/qa.test.ts
npm run typecheck
```

Expected: chunk selection test passes and route action typechecks.

- [ ] **Step 5: Commit**

```bash
git add app/lib/services/qa.server.ts app/routes/sources.\$sourceType.\$sourceId.tsx tests/qa.test.ts
git commit -m "feat: add single source question answering"
```

---

## Task 11: Markdown Download and Batch Zip Export

**Files:**
- Create: `app/lib/export/zip.server.ts`
- Create: `app/lib/repositories/exports.server.ts`
- Create: `app/routes/exports.tsx`
- Modify: `app/routes/sources.$sourceType.$sourceId.tsx`
- Create: `tests/zip-export.test.ts`

- [ ] **Step 1: Write zip path test**

Test `exportPathForSource({ podcastTitle: "Acme Show", title: "Hello/World" })` returns a safe path like `Acme Show/Hello-World.md`.

- [ ] **Step 2: Implement zip export helpers**

Use JSZip to add one `.md` per selected source. Sanitize filenames by replacing `/`, `:`, `*`, `?`, `"`, `<`, `>`, `|` with `-`.

- [ ] **Step 3: Add single Markdown download**

On source detail, add a download action that streams or redirects to the existing `markdown_r2_key`. If user selects “include transcript”, render a fresh Markdown file with transcript appendix.

- [ ] **Step 4: Add batch export route**

`/exports` lets user select processed sources, creates an export job, stores zip under `users/{userId}/exports/{exportId}.zip`, and lists recent exports with download links.

- [ ] **Step 5: Verify export**

Run:

```bash
npm test -- tests/zip-export.test.ts
npm run typecheck
```

Expected: zip path tests pass and export route typechecks.

- [ ] **Step 6: Commit**

```bash
git add app/lib/export/zip.server.ts app/lib/repositories/exports.server.ts app/routes/exports.tsx app/routes/sources.\$sourceType.\$sourceId.tsx tests/zip-export.test.ts
git commit -m "feat: add markdown and zip exports"
```

---

## Task 12: Production AI Provider Adapters and Usage Records

**Files:**
- Create: `app/lib/providers/openai.server.ts`
- Create: `app/lib/repositories/usage-records.server.ts`
- Modify: `app/lib/queue/processing-worker.server.ts`
- Modify: `app/lib/services/qa.server.ts`
- Create: `tests/provider-contract.test.ts`

- [ ] **Step 1: Write provider contract test with mock fetch**

Test that the OpenAI analysis adapter rejects invalid JSON with `Error("Analysis provider returned invalid knowledge card JSON")` and accepts JSON matching `KnowledgeCard`.

- [ ] **Step 2: Implement OpenAI adapter behind interfaces**

Create `openai.server.ts` implementing `TranscriptionProvider`, `AnalysisProvider`, and `ChatProvider`. The adapter must:

- Read API key from `env.OPENAI_API_KEY`.
- Throw a clear 500 setup error if missing in production mode.
- Validate analysis JSON with Zod before returning.
- Return provider/model fields.

- [ ] **Step 3: Record usage**

Create `usage-records.server.ts` with `createUsageRecord`. Queue worker records operation, provider, model, estimated input/output units, and estimated cost after transcription, analysis, and Q&A.

- [ ] **Step 4: Add provider selection**

Create a small factory `getProviders(env)` that returns mock providers when `env.AI_PROVIDER === "mock"` and OpenAI providers when `env.AI_PROVIDER === "openai"`.

- [ ] **Step 5: Verify provider contracts**

Run:

```bash
npm test -- tests/provider-contract.test.ts
npm run typecheck
```

Expected: invalid JSON is rejected, valid JSON is accepted, and queue worker typechecks.

- [ ] **Step 6: Commit**

```bash
git add app/lib/providers/openai.server.ts app/lib/repositories/usage-records.server.ts app/lib/queue/processing-worker.server.ts app/lib/services/qa.server.ts tests/provider-contract.test.ts
git commit -m "feat: add production AI providers and usage records"
```

---

## Task 13: Cron RSS Refresh and Deployment Hardening

**Files:**
- Create: `app/lib/services/rss-refresh.server.ts`
- Modify: `app/worker.ts` or Cloudflare entrypoint
- Modify: `wrangler.toml`
- Create: `tests/rss-refresh.test.ts`

- [ ] **Step 1: Write RSS refresh test**

Test that `mergeFeedEpisodes(existingGuids, parsedEpisodes)` returns only new episodes and does not enqueue processing jobs.

- [ ] **Step 2: Implement refresh service**

`refreshPodcastFeed(db, userId, podcastId, fetcher)` fetches RSS, parses it, upserts new episodes, updates `last_fetched_at`, and never calls AI processing.

- [ ] **Step 3: Add cron entry**

Configure scheduled worker to refresh all podcasts in batches. Set cron in `wrangler.toml`, for example:

```toml
[triggers]
crons = ["*/30 * * * *"]
```

- [ ] **Step 4: Add deployment checklist doc**

Create `docs/deployment.md` with exact commands for creating D1, R2, Queue, setting secrets, running migrations, and deploying.

- [ ] **Step 5: Verify refresh**

Run:

```bash
npm test -- tests/rss-refresh.test.ts
npm run typecheck
npm test
```

Expected: refresh tests pass, typecheck passes, and full test suite passes.

- [ ] **Step 6: Commit**

```bash
git add app/lib/services/rss-refresh.server.ts app/worker.ts wrangler.toml tests/rss-refresh.test.ts docs/deployment.md
git commit -m "feat: add RSS cron refresh and deployment docs"
```

---

## Final Verification

- [ ] Run all tests:

```bash
npm test
```

Expected: all Vitest tests pass.

- [ ] Run typecheck:

```bash
npm run typecheck
```

Expected: no TypeScript errors.

- [ ] Build app:

```bash
npm run build
```

Expected: production build succeeds.

- [ ] Run local Cloudflare migration:

```bash
npm run db:migrate:local
```

Expected: D1 schema applies locally without SQL errors.

- [ ] Manual smoke test in browser:

```bash
npm run dev
```

Expected flow:

1. Register a user.
2. Add an RSS podcast.
3. See episodes.
4. Upload a small mp3.
5. Process one source with mock providers.
6. View transcript and knowledge card.
7. Ask a question.
8. Download Markdown.
9. Create a zip export.

- [ ] Commit any final fixes:

```bash
git status --short
git add .
git commit -m "chore: complete MVP verification fixes"
```

---

## Self-Review Notes

Spec coverage:

- Cloudflare deployment: covered by Tasks 1, 2, 13.
- Multi-user auth and user ownership: covered by Tasks 2, 3 and repository requirements in later tasks.
- RSS ingestion: covered by Tasks 4 and 13.
- Controlled audio upload: covered by Task 5.
- Manual processing: covered by Task 6.
- Provider abstraction: covered by Tasks 6 and 12.
- Transcript and analysis artifacts in R2: covered by Task 7.
- Knowledge card UI: covered by Task 8.
- Basic search: covered by Task 9.
- Single-episode Q&A: covered by Task 10.
- Markdown and zip export: covered by Task 11.
- Usage/cost records: covered by Task 12.
- Error handling and idempotency: covered by Tasks 6, 7, 12, and Final Verification.
- Tests: every implementation task includes focused tests plus final verification.

Known intentional MVP constraint: transcript body search may start with indexed metadata/searchable analysis fields if D1 FTS setup proves slow. The UI must disclose this clearly, and full transcript indexing should be promoted into a follow-up plan if not completed in Task 9.
