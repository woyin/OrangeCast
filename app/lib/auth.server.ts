import { createCookieSessionStorage, redirect } from "@remix-run/cloudflare";
import type { AppEnv } from "./env.server";

const MIN_PASSWORD_LENGTH = 8;
function bytesToHex(bytes: ArrayBuffer): string {
  return Array.from(new Uint8Array(bytes))
    .map((byte) => byte.toString(16).padStart(2, "0"))
    .join("");
}

export function normalizeEmail(email: string): string {
  return email.trim().toLowerCase();
}

export function validatePassword(password: string): { ok: true } | { ok: false; message: string } {
  if (password.length < MIN_PASSWORD_LENGTH) {
    return { ok: false, message: "Password must be at least 8 characters." };
  }

  return { ok: true };
}

export async function hashPassword(password: string): Promise<string> {
  // Development placeholder: replace with a Workers-compatible slow password hash
  // before production.
  const data = new TextEncoder().encode(password);
  const digest = await globalThis.crypto.subtle.digest("SHA-256", data);
  return bytesToHex(digest);
}

export async function verifyPassword(password: string, hash: string): Promise<boolean> {
  return (await hashPassword(password)) === hash;
}

export function sessionStorage(env: AppEnv) {
  return createCookieSessionStorage({
    cookie: {
      name: env.SESSION_COOKIE_NAME,
      httpOnly: true,
      path: "/",
      sameSite: "lax",
      secrets: [env.SESSION_SECRET],
      secure: true,
    },
  });
}

export async function requireUserId(request: Request, env: AppEnv): Promise<string> {
  const storage = sessionStorage(env);
  const session = await storage.getSession(request.headers.get("Cookie"));
  const userId = session.get("userId");

  if (typeof userId !== "string" || userId.length === 0) {
    throw redirect("/login");
  }

  return userId;
}
