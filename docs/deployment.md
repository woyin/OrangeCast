# CloudWisePod deployment

> **历史文档，不再用于部署。** 本文描述已废弃的 Cloudflare/TypeScript 架构。当前版本通过 Go 单二进制或 Docker 部署，见 [`../README.md`](../README.md)；目标部署边界见 [`product-goal.md`](product-goal.md)。

CloudWisePod deploys as two Cloudflare Workers:

1. **Pages app** (`wrangler.toml`): Serves the Remix web application. Uses `functions/[[path]].ts` as the Pages Function adapter. Bindings: `DB`, `R2`, `PROCESSING_QUEUE` (producer).
2. **Worker** (`wrangler.rss-refresh.toml`, `app/worker.ts`): Handles RSS cron refresh AND processes queue messages (transcribe/analyze jobs).

## Prerequisites

```bash
npm install
npx wrangler login
```

## Create Cloudflare resources

Create the D1 database:

```bash
npx wrangler d1 create cloudwise_pod
```

Copy the returned `database_id` into **both** `wrangler.toml` and `wrangler.rss-refresh.toml` under `[[d1_databases]]`.

Create the R2 bucket:

```bash
npx wrangler r2 bucket create cloudwise-pod
```

Create the processing queue used by user-initiated AI jobs:

```bash
npx wrangler queues create cloudwise-pod-processing
```

## Set secrets

The Pages app requires `SESSION_SECRET` and, for production AI, `OPENAI_API_KEY`:

```bash
npx wrangler pages secret put SESSION_SECRET --project-name cloudwise-pod
npx wrangler pages secret put OPENAI_API_KEY --project-name cloudwise-pod
```

The Worker requires `SESSION_SECRET` (for env type compatibility):

```bash
npx wrangler secret put SESSION_SECRET --config wrangler.rss-refresh.toml
```

If using OpenAI in production, also set it on the Worker:

```bash
npx wrangler secret put OPENAI_API_KEY --config wrangler.rss-refresh.toml
npx wrangler secret put AI_PROVIDER --config wrangler.rss-refresh.toml
```

## Environment variables

| Variable | Where | Required | Description |
|---|---|---|---|
| `SESSION_SECRET` | Pages + Worker secret | **Yes** | Cookie session encryption key |
| `AI_PROVIDER` | Pages + Worker | **Yes (prod)** | `openai` for production, `mock` for dev/test |
| `ALLOW_MOCK_PROVIDER` | Pages + Worker | No | Set to `true` to allow mock fallback |
| `OPENAI_API_KEY` | Pages + Worker secret | **Yes (prod)** | OpenAI API key |
| `ENVIRONMENT` | Pages + Worker | No | `production` disables mock fallback |
| `APP_NAME` | wrangler.toml vars | Yes | App name |
| `SESSION_COOKIE_NAME` | wrangler.toml vars | Yes | Cookie name |
| `UPLOAD_MAX_BYTES` | wrangler.toml vars | Yes | Max upload size (default 100MB) |
| `UPLOAD_MAX_SECONDS` | wrangler.toml vars | Yes | Max audio duration (default 7200s) |

### Production AI provider setup

For production, set these as secrets on **both** the Pages project and the Worker:

```bash
# On Pages
npx wrangler pages secret put AI_PROVIDER --project-name cloudwise-pod
# Type: openai

# On Worker
npx wrangler secret put AI_PROVIDER --config wrangler.rss-refresh.toml
# Type: openai
```

Without `AI_PROVIDER=openai`, the app throws a 500 error in production (no silent mock fallback).

## Run migrations

Apply the schema to the remote D1 database:

```bash
npm run db:migrate:remote
```

For local development:

```bash
npm run db:migrate:local
```

## Deploy the Pages app

Build and deploy the Remix app to Cloudflare Pages:

```bash
npm run build
npx wrangler pages deploy ./build/client --project-name cloudwise-pod
```

The Pages project uses `functions/[[path]].ts` as the Pages Function adapter, which imports the Remix server build from `build/server/index.js`.

## Deploy the Worker (cron + queue consumer)

The Worker entrypoint `app/worker.ts` handles:
- **Cron**: RSS feed refresh every 30 minutes (discovers new episodes, no auto-processing).
- **Queue consumer**: Processes transcribe/analyze jobs when users click "Process" on an episode or upload.

```bash
npx wrangler deploy --config wrangler.rss-refresh.toml
```

## Verify deployment

Confirm the Worker can see bindings and starts without throwing:

```bash
npx wrangler tail cloudwise-pod-rss-refresh
```

After the next 30-minute cron tick, logs should include `RSS refresh cron completed` with attempted/refreshed/failed counts. New RSS episodes remain `unprocessed` until a user explicitly starts AI processing.

## Deployment checklist

- [ ] D1 database created, `database_id` updated in both toml files
- [ ] R2 bucket created
- [ ] Queue created
- [ ] `SESSION_SECRET` set on Pages + Worker
- [ ] `AI_PROVIDER=openai` set on Pages + Worker (production)
- [ ] `OPENAI_API_KEY` set on Pages + Worker (production)
- [ ] Schema migrated to remote D1
- [ ] Pages app deployed
- [ ] Worker deployed
- [ ] Verify Worker logs show cron execution
