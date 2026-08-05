-- Migration 0010: Provider API key + base URL 存 SQLite（可页面配置）
-- nullable：空则 fallback 到 .env 环境变量（向后兼容）

ALTER TABLE settings ADD COLUMN groq_api_key TEXT;
ALTER TABLE settings ADD COLUMN groq_base_url TEXT;
ALTER TABLE settings ADD COLUMN openai_api_key TEXT;
ALTER TABLE settings ADD COLUMN openai_base_url TEXT;
