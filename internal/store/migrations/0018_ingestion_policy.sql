-- Migration 0018: Podcast 受控自动摄取（ADR-0021 / roadmap Phase 9）
-- 默认 manual，确保既有订阅不会在升级后未经 Owner 授权产生 AI 调用。
ALTER TABLE podcasts ADD COLUMN ingestion_policy TEXT NOT NULL DEFAULT 'manual';
CREATE INDEX IF NOT EXISTS idx_podcasts_ingestion_policy ON podcasts(ingestion_policy);
