import { json, redirect, type ActionFunctionArgs } from "@remix-run/cloudflare";
import { Form, useActionData } from "@remix-run/react";
import { hashPassword, normalizeEmail, sessionStorage, validatePassword } from "../lib/auth.server";
import { requireEnv } from "../lib/env.server";
import { createUser, findUserByEmail } from "../lib/repositories/users.server";

export async function action({ request, context }: ActionFunctionArgs) {
  const env = requireEnv(context);
  const form = await request.formData();
  const email = normalizeEmail(String(form.get("email") || ""));
  const password = String(form.get("password") || "");

  if (!email) {
    return json({ error: "Email is required." }, { status: 400 });
  }

  const passwordValidation = validatePassword(password);
  if (!passwordValidation.ok) {
    return json({ error: passwordValidation.message }, { status: 400 });
  }

  const existingUser = await findUserByEmail(env.DB, email);
  if (existingUser) {
    return json({ error: "An account already exists for that email." }, { status: 400 });
  }

  const user = await createUser(env.DB, email, await hashPassword(password));
  const storage = sessionStorage(env);
  const session = await storage.getSession(request.headers.get("Cookie"));
  session.set("userId", user.id);

  return redirect("/dashboard", {
    headers: { "Set-Cookie": await storage.commitSession(session) },
  });
}

export default function Register() {
  const actionData = useActionData<typeof action>();

  return (
    <main>
      <h1>Register</h1>
      {actionData?.error ? <p role="alert">{actionData.error}</p> : null}
      <Form method="post">
        <label>
          Email
          <input name="email" type="email" autoComplete="email" />
        </label>
        <label>
          Password
          <input name="password" type="password" autoComplete="new-password" />
        </label>
        <button type="submit">Register</button>
      </Form>
    </main>
  );
}
