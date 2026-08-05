-- Migration 0009: 每个任务独立配置 Provider + Model（ADR-0009 扩展）

ALTER TABLE settings ADD COLUMN transcription_provider TEXT DEFAULT 'groq';
ALTER TABLE settings ADD COLUMN analysis_provider TEXT DEFAULT 'groq';
ALTER TABLE settings ADD COLUMN highlight_provider TEXT DEFAULT 'groq';
ALTER TABLE settings ADD COLUMN qa_provider TEXT DEFAULT 'groq';

-- highlight_model 字段（原来没有）
ALTER TABLE settings ADD COLUMN highlight_model TEXT;
