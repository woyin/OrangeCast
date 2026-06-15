# CloudWisePod deployment

CloudWisePod uses Cloudflare Pages for the Remix app and a small scheduled Worker for RSS refresh. The scheduled Worker refreshes podcasts in bounded batches every 30 minutes and only inserts new RSS episodes as `unprocessed`; it does **not** enqueue AI processing jobs.

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

Copy the returned `database_id` into `wrangler.toml` and `wrangler.rss-refresh.toml` under `[[d1_databases]]`.

Create the R2 bucket:

```bash
npx wrangler r2 bucket create cloudwise-pod
```

Create the processing queue used by user-initiated AI jobs:

```bash
npx wrangler queues create cloudwise-pod-processing
```

## Set secrets

Set application secrets for Pages:

```bash
npx wrangler pages secret put SESSION_SECRET --project-name cloudwise-pod
npx wrangler pages secret put OPENAI_API_KEY --project-name cloudwise-pod
```

Set the same secrets for the scheduled RSS Worker if the Worker is deployed separately from Pages:

```bash
npx wrangler secret put SESSION_SECRET --config wrangler.rss-refresh.toml
npx wrangler secret put OPENAI_API_KEY --config wrangler.rss-refresh.toml
```

`OPENAI_API_KEY` is not used by RSS refresh, but setting matching secrets keeps the Worker environment compatible with the shared app environment type.

## Run migrations

Apply the schema to the remote D1 database:

```bash
npx wrangler d1 execute cloudwise_pod --remote --file=app/lib/schema.sql
```

For local development, run:

```bash
npm run db:migrate:local
```

## Deploy the Pages app

Build and deploy the Remix app to Cloudflare Pages:

```bash
npm run build
npx wrangler pages deploy ./build/client --project-name cloudwise-pod
```

The Pages app uses the bindings in `wrangler.toml`: `DB`, `R2`, and `PROCESSING_QUEUE`.

## Deploy the scheduled RSS refresh Worker

The scheduled RSS refresh entrypoint is `app/worker.ts`. Cloudflare treats the checked-in `wrangler.toml` as the Pages project config because it contains `pages_build_output_dir`, so the scheduled Worker uses the separate Worker-only config `wrangler.rss-refresh.toml`. That config contains the shared bindings and cron trigger:

```toml
[triggers]
crons = ["*/30 * * * *"]
```

After copying the real D1 `database_id` into both Wrangler config files, deploy the Worker with:

```bash
npx wrangler deploy --config wrangler.rss-refresh.toml
```

## Verify deployment

Confirm the scheduled Worker can see bindings and starts without throwing:

```bash
npx wrangler tail cloudwise-pod-rss-refresh
```

After the next 30-minute cron tick, logs should include `RSS refresh cron completed` with attempted/refreshed/failed counts. New RSS episodes remain `unprocessed` until a user explicitly starts AI processing.
