-- Migration 0014: Narration 解说音轨（ADR-0019）
--
-- Narration 是 GeneratedDerivative 的音频形态：AI 将 Highlight.Gist 的文字用 TTS 合成为 wav。
-- 它不进入 EvidenceAudio（不作为核验依据），存于独立的 narrations 目录，不进备份包（可重新生成）。
--
-- 版本化粒度：每个 Highlight 的 Gist 各有一段独立版本化的 Narration（二维版本空间：
-- highlight_id × version），current 取每 highlight_id 的 MAX(version)。
-- 不复用 artifact_versions：其 current 指针机制（source 表上的单列）无法表达
-- "每个 highlight_id 各有一个 current" 的二维语义（ADR-0019 R4）。

CREATE TABLE IF NOT EXISTS narrations (
    id               TEXT PRIMARY KEY,
    source_type      TEXT NOT NULL,
    source_id        TEXT NOT NULL,
    highlight_id     TEXT NOT NULL,          -- 关联到 Highlight.ID（Citation 集合的稳定 hash）
    version          INTEGER NOT NULL,       -- 该 highlight_id 下的版本号（重生成递增）
    voice            TEXT NOT NULL,          -- 音色标识（如 kokoro-af_heart）
    model            TEXT NOT NULL,          -- 引擎+模型（如 kokoro-82m-v1.0）
    relpath          TEXT NOT NULL,          -- 相对 NarrationDir 的路径
    duration_seconds REAL NOT NULL,
    char_count       INTEGER NOT NULL,
    provider         TEXT NOT NULL,          -- kokoro | openai | elevenlabs（付费按 ADR-0009 单次授权）
    created_at       TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE(source_type, source_id, highlight_id, version)
);
CREATE INDEX IF NOT EXISTS idx_narrations_highlight ON narrations(source_type, source_id, highlight_id);
CREATE INDEX IF NOT EXISTS idx_narrations_source ON narrations(source_type, source_id);
