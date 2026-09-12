-- B07（ADR-0024 §5）：解说任务与内容感知缓存身份。
-- cache_key = SHA256(文本指纹 + highlight_id + 输入高光版本 + Provider + 模型 + 音色 + 语言)。
-- 同一 cache_key 直接复用既有音频；旧行 cache_key='' 为 legacy，不参与缓存命中（可重生成）。

ALTER TABLE narrations ADD COLUMN cache_key TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_narrations_cache ON narrations(source_type, source_id, highlight_id, cache_key);
