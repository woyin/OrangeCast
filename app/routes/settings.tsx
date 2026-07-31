import { json, redirect, type ActionFunctionArgs, type LoaderFunctionArgs } from "@remix-run/cloudflare";
import { Form, useActionData, useLoaderData } from "@remix-run/react";
import { requireUserId } from "../lib/auth.server";
import { requireEnv } from "../lib/env.server";
import { getUserSettings, saveUserSettings } from "../lib/settings.server";

// Hardcoded defaults shown as placeholders in the form
const DEFAULTS = {
  transcription: "gpt-4o-mini-transcribe",
  analysis: "gpt-4.1-mini",
  chat: "gpt-4.1-mini",
};

// Env var values (from wrangler.toml [vars]) — shown as "system default"
function envDefaults(env: Record<string, string | undefined>) {
  return {
    transcription: env.OPENAI_TRANSCRIPTION_MODEL || DEFAULTS.transcription,
    analysis: env.OPENAI_ANALYSIS_MODEL || DEFAULTS.analysis,
    chat: env.OPENAI_CHAT_MODEL || DEFAULTS.chat,
  };
}

export async function loader({ request, context }: LoaderFunctionArgs) {
  const env = requireEnv(context);
  const userId = await requireUserId(request, env);
  const settings = await getUserSettings(env.DB, userId);
  const sysDefaults = envDefaults(env as unknown as Record<string, string | undefined>);

  return json({
    // What the user has explicitly set (null = using system default)
    userSettings: settings,
    // System defaults for display
    sysDefaults,
  });
}

export async function action({ request, context }: ActionFunctionArgs) {
  const env = requireEnv(context);
  const userId = await requireUserId(request, env);
  const formData = await request.formData();

  const transcription_model = (formData.get("transcription_model") as string | null) ?? "";
  const analysis_model = (formData.get("analysis_model") as string | null) ?? "";
  const chat_model = (formData.get("chat_model") as string | null) ?? "";

  // Basic validation: max length, alphanumeric + dash/dot
  const modelRegex = /^[a-zA-Z0-9][a-zA-Z0-9.\-_]{0,63}$/;
  const fields = { transcription_model, analysis_model, chat_model };

  for (const [key, value] of Object.entries(fields)) {
    const trimmed = value.trim();
    if (trimmed && !modelRegex.test(trimmed)) {
      return json(
        { error: `Invalid model name "${trimmed}" in ${key}. Only letters, numbers, dots, dashes, underscores allowed.` },
        { status: 400 },
      );
    }
  }

  await saveUserSettings(env.DB, userId, fields);

  return json({ ok: true, saved: fields });
}

export default function Settings() {
  const { userSettings, sysDefaults } = useLoaderData<typeof loader>();
  const actionData = useActionData<typeof action>();

  const field = (userVal: string | null, label: string, name: string, sysDefault: string) => (
    <div style={{ marginBottom: "1.5rem" }}>
      <label htmlFor={name} style={{ display: "block", fontWeight: 600, marginBottom: "0.25rem" }}>
        {label}
      </label>
      <input
        id={name}
        name={name}
        type="text"
        defaultValue={userVal ?? ""}
        placeholder={sysDefault}
        style={{
          width: "100%",
          maxWidth: 400,
          padding: "0.5rem 0.75rem",
          border: "1px solid #ccc",
          borderRadius: 4,
          fontSize: 14,
          fontFamily: "monospace",
        }}
      />
      <p style={{ fontSize: 12, color: "#666", marginTop: 4 }}>
        留空 = 使用系统默认 <code>{sysDefault}</code>
      </p>
    </div>
  );

  return (
    <main style={{ maxWidth: 640, margin: "2rem auto", padding: "0 1rem" }}>
      <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", marginBottom: "1.5rem" }}>
        <h1 style={{ margin: 0 }}>⚙️ 模型设置</h1>
        <a href="/dashboard" style={{ fontSize: 14, textDecoration: "none" }}>
          ← 返回 Dashboard
        </a>
      </div>

      {actionData?.error && (
        <div style={{ background: "#fee", border: "1px solid #c33", padding: "0.75rem 1rem", borderRadius: 4, marginBottom: "1rem" }}>
          ❌ {actionData.error}
        </div>
      )}
      {actionData?.ok && (
        <div style={{ background: "#efe", border: "1px solid #3c3", padding: "0.75rem 1rem", borderRadius: 4, marginBottom: "1rem" }}>
          ✅ 设置已保存
        </div>
      )}

      <Form method="post">
        <p style={{ color: "#666", marginBottom: "1.5rem" }}>
          在这里自定义 AI 模型名称。留空的字段将使用系统默认值（<code>wrangler.toml</code> 中的配置或硬编码回退值）。
        </p>

        {field(userSettings.transcription_model, "🎤 转录模型 (Transcription)", "transcription_model", sysDefaults.transcription)}
        {field(userSettings.analysis_model, "🧠 分析模型 (Analysis / Knowledge Card)", "analysis_model", sysDefaults.analysis)}
        {field(userSettings.chat_model, "💬 对话模型 (Chat / Q&A)", "chat_model", sysDefaults.chat)}

        <button
          type="submit"
          style={{
            padding: "0.6rem 1.5rem",
            background: "#2563eb",
            color: "white",
            border: "none",
            borderRadius: 4,
            fontSize: 15,
            cursor: "pointer",
          }}
        >
          💾 保存设置
        </button>
      </Form>

      {userSettings.updated_at && (
        <p style={{ fontSize: 12, color: "#999", marginTop: "2rem" }}>
          上次更新：{new Date(userSettings.updated_at).toLocaleString("zh-CN")}
        </p>
      )}
    </main>
  );
}
