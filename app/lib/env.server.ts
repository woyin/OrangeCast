export interface AppEnv {
  DB: D1Database;
  R2: R2Bucket;
  PROCESSING_QUEUE: Queue;
  APP_NAME: string;
  SESSION_COOKIE_NAME: string;
  SESSION_SECRET: string;
  UPLOAD_MAX_BYTES: string;
  UPLOAD_MAX_SECONDS: string;
  OPENAI_API_KEY?: string;
  AI_PROVIDER?: string;
  OPENAI_TRANSCRIPTION_MODEL?: string;
  OPENAI_ANALYSIS_MODEL?: string;
  OPENAI_CHAT_MODEL?: string;
  ENVIRONMENT?: string;
  NODE_ENV?: string;
  ALLOW_MOCK_PROVIDER?: string;
}

/**
 * Extract AppEnv from the Remix load context.
 *
 * Supports three shapes:
 *   1. Pages Function: context.env  (where context is CloudflareEnv)
 *   2. Worker direct:  context IS AppEnv (passed as loadContext to createRequestHandler)
 *   3. Worker via cloudflare adapter: context.cloudflare.env
 */
export function requireEnv(context: any): AppEnv {
  const env: AppEnv | undefined =
    context?.cloudflare?.env ??   // (3) cloudflare adapter
    context?.env ??                // (1) Pages Function / Remix cloudflare
    (context?.DB ? context : undefined); // (2) direct AppEnv

  if (
    !env?.DB ||
    !env?.R2 ||
    !env?.PROCESSING_QUEUE ||
    !env?.APP_NAME ||
    !env?.SESSION_COOKIE_NAME ||
    !env?.SESSION_SECRET ||
    !env?.UPLOAD_MAX_BYTES ||
    !env?.UPLOAD_MAX_SECONDS
  ) {
    console.error("requireEnv failed – missing bindings", {
      hasDB: !!env?.DB,
      hasR2: !!env?.R2,
      hasQueue: !!env?.PROCESSING_QUEUE,
      hasAppName: !!env?.APP_NAME,
      hasSessionCookie: !!env?.SESSION_COOKIE_NAME,
      hasSessionSecret: !!env?.SESSION_SECRET,
    });
    throw new Response("Cloudflare bindings are not configured", { status: 500 });
  }
  return env;
}
