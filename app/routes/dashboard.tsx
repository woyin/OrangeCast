import { json, type LoaderFunctionArgs } from "@remix-run/cloudflare";
import { Link, useLoaderData } from "@remix-run/react";
import { requireUserId } from "../lib/auth.server";
import { requireEnv } from "../lib/env.server";

export async function loader({ request, context }: LoaderFunctionArgs) {
  const env = requireEnv(context);
  const userId = await requireUserId(request, env);
  return json({ userId });
}

export default function Dashboard() {
  const { userId } = useLoaderData<typeof loader>();

  return (
    <main>
      <h1>CloudWisePod</h1>
      <p>Podcast knowledge cards on Cloudflare.</p>
      <p>Logged in as {userId}.</p>
      <p><Link to="/uploads">Uploads</Link></p>
    </main>
  );
}
