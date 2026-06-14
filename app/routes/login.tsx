import { json, redirect, type ActionFunctionArgs } from "@remix-run/cloudflare";
import { Form, useActionData } from "@remix-run/react";
import { normalizeEmail, sessionStorage, verifyPassword } from "../lib/auth.server";
import { requireEnv } from "../lib/env.server";
import { findUserByEmail } from "../lib/repositories/users.server";

export async function action({ request, context }: ActionFunctionArgs) {
  const env = requireEnv(context);
  const form = await request.formData();
  const email = normalizeEmail(String(form.get("email") || ""));
  const password = String(form.get("password") || "");

  const user = await findUserByEmail(env.DB, email);
  if (!user || !(await verifyPassword(password, user.password_hash))) {
    return json({ error: "Invalid email or password." }, { status: 400 });
  }

  const storage = sessionStorage(env);
  const session = await storage.getSession(request.headers.get("Cookie"));
  session.set("userId", user.id);

  return redirect("/dashboard", {
    headers: { "Set-Cookie": await storage.commitSession(session) },
  });
}

export default function Login() {
  const actionData = useActionData<typeof action>();

  return (
    <main>
      <h1>Login</h1>
      {actionData?.error ? <p role="alert">{actionData.error}</p> : null}
      <Form method="post">
        <label>
          Email
          <input name="email" type="email" autoComplete="email" />
        </label>
        <label>
          Password
          <input name="password" type="password" autoComplete="current-password" />
        </label>
        <button type="submit">Login</button>
      </Form>
    </main>
  );
}
