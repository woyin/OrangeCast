import type { AppEnv } from "./lib/env.server";
import { handleProcessingQueueBatch } from "./lib/queue/processing-worker.server";
import type { ProcessingQueueMessage } from "./lib/queue/messages.server";
import { refreshAllPodcastFeeds, RSS_REFRESH_BATCH_SIZE } from "./lib/services/rss-refresh.server";

export default {
  async scheduled(_controller: ScheduledController, env: AppEnv, _ctx: ExecutionContext): Promise<void> {
    const result = await refreshAllPodcastFeeds(env.DB, { batchSize: RSS_REFRESH_BATCH_SIZE });
    console.log("RSS refresh cron completed", result);
  },

  async queue(batch: MessageBatch<ProcessingQueueMessage>, env: AppEnv): Promise<void> {
    await handleProcessingQueueBatch(env, batch);
  },

  async fetch(): Promise<Response> {
    return new Response("CloudWisePod worker", { status: 200 });
  },
};
