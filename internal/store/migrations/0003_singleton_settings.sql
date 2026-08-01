-- Migration 0003: settings 收敛为实例级单例，移除全局 active_provider（ADR-0007 / ADR-0009）
--
-- 背景：ADR-0007 要求"仅凭据保留单例 Owner"，settings 是实例配置而非用户配置；
-- ADR-0009 要求移除全局 active_provider 切换——Groq 是默认零成本 Provider，
-- 付费 Provider 只按单次 ProcessingJob 尝试显式授权（授权随任务记录，不在本迁移建表，
-- 相关字段在 ArtifactVersion 阶段引入）。
--
-- 本迁移把 settings 重建为固定单行（id=1）：
--   - 移除 user_id 主键与外键（不再属于某个用户）。
--   - 移除 active_provider 列（不再存在全局切换）。
--   - 保留模型选择列（转录/分析/QA 模型），这些是实例级偏好，仍可配置。

CREATE TABLE settings_new (
    id                  INTEGER PRIMARY KEY CHECK (id = 1),
    transcription_model TEXT,
    analysis_model      TEXT,
    qa_model            TEXT,
    updated_at          TEXT NOT NULL DEFAULT (datetime('now'))
);

-- 继承旧 settings 中单例 Owner（任意一行）的模型配置；active_provider 被丢弃。
INSERT INTO settings_new (id, transcription_model, analysis_model, qa_model)
SELECT 1, transcription_model, analysis_model, qa_model FROM settings LIMIT 1;

DROP TABLE settings;
ALTER TABLE settings_new RENAME TO settings;
