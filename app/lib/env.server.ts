export interface AppEnv {
  DB: D1Database;
  R2: R2Bucket;
  PROCESSING_QUEUE: Queue;
  APP_NAME: string;
  SESSION_COOKIE_NAME: string;
  SESSION_SECRET?: string;
  UPLOAD_MAX_BYTES: string;
  UPLOAD_MAX_SECONDS: string;
  OPENAI_API_KEY?: string;
}

export function requireEnv(context: { cloudflare?: { env: AppEnv } } | { env?: AppEnv }): AppEnv {
  const env =
    (context as { cloudflare?: { env: AppEnv } }).cloudflare?.env ??
    (context as { env?: AppEnv }).env;
  if (
    !env?.DB ||
    !env?.R2 ||
    !env?.PROCESSING_QUEUE ||
    !env.APP_NAME ||
    !env.SESSION_COOKIE_NAME ||
    !env.UPLOAD_MAX_BYTES ||
    !env.UPLOAD_MAX_SECONDS
  ) {
    throw new Response("Cloudflare bindings are not configured", { status: 500 });
  }
  return env;
}
