import { redirect, type ActionFunctionArgs, type LoaderFunctionArgs } from "@remix-run/cloudflare";
import { sessionStorage } from "../lib/auth.server";
import { requireEnv } from "../lib/env.server";

async function logout(request: Request, context: ActionFunctionArgs["context"]) {
  const env = requireEnv(context);
  const storage = sessionStorage(env);
  const session = await storage.getSession(request.headers.get("Cookie"));

  return redirect("/login", {
    headers: { "Set-Cookie": await storage.destroySession(session) },
  });
}

export async function action({ request, context }: ActionFunctionArgs) {
  return logout(request, context);
}

export async function loader({ request, context }: LoaderFunctionArgs) {
  return logout(request, context);
}
