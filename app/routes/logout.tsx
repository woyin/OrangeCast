import { redirect, type ActionFunctionArgs, type LoaderFunctionArgs } from "@remix-run/cloudflare";
import { Form } from "@remix-run/react";
import { sessionStorage } from "../lib/auth.server";
import { requireEnv } from "../lib/env.server";

export async function action({ request, context }: ActionFunctionArgs) {
  const env = requireEnv(context);
  const storage = sessionStorage(env);
  const session = await storage.getSession(request.headers.get("Cookie"));

  return redirect("/login", {
    headers: { "Set-Cookie": await storage.destroySession(session) },
  });
}

export function loader() {
  return redirect("/dashboard");
}

export default function Logout() {
  return (
    <main>
      <h1>Log out</h1>
      <p>Are you sure you want to log out?</p>
      <Form method="post">
        <button type="submit">Log out</button>
      </Form>
    </main>
  );
}
