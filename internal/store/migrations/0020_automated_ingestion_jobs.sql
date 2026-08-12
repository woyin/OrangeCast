-- 自动订阅发现的任务只能形成可编辑的知识素材；衍生产物必须由 Owner 后续触发。
ALTER TABLE processing_jobs ADD COLUMN is_automated INTEGER NOT NULL DEFAULT 0;
CREATE INDEX IF NOT EXISTS idx_processing_jobs_automated ON processing_jobs(is_automated, status);
