-- Migration 0007: Highlight 产物 + 处理进度 current_step（ADR-0015 / ADR-0016）

-- Source 指向当前 Highlight 版本（ArtifactVersion kind='highlight' 复用现有表）
ALTER TABLE episodes ADD COLUMN current_highlight_version INTEGER;
ALTER TABLE uploads ADD COLUMN current_highlight_version INTEGER;

-- 处理进度细步骤预留（ADR-0015：暂不写值，未来细化用）
ALTER TABLE processing_jobs ADD COLUMN current_step TEXT;
