import type { AppEnv } from "./lib/env.server";
import { refreshAllPodcastFeeds, RSS_REFRESH_BATCH_SIZE } from "./lib/services/rss-refresh.server";

export default {
  async scheduled(_controller: ScheduledController, env: AppEnv, _ctx: ExecutionContext): Promise<void> {
    const result = await refreshAllPodcastFeeds(env.DB, { batchSize: RSS_REFRESH_BATCH_SIZE });
    console.log("RSS refresh cron completed", result);
  },

  async fetch(): Promise<Response> {
    return new Response("CloudWisePod RSS refresh worker", { status: 200 });
  },
};
