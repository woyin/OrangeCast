-- B02（ADR-0024 §5）：持久化任务输入、结果与幂等身份。
-- intent_id 冻结任务意图：同 (source, job_type, intent) 只允许一个 queued/running
-- 任务（部分唯一索引即并发入队 CAS）；显式"重新生成"换新 intent 即可再次入队。
-- input_snapshot_json 在入队时冻结已知输入；checkpoint_json 保存步骤断点；
-- result_json/result_state 先于终态写入（崩溃恢复时结果可复用，不重放远端调用）。
-- 全部新列带默认值，旧行兼容读取。

ALTER TABLE processing_jobs ADD COLUMN intent_id TEXT NOT NULL DEFAULT '';
ALTER TABLE processing_jobs ADD COLUMN input_snapshot_json TEXT NOT NULL DEFAULT '';
ALTER TABLE processing_jobs ADD COLUMN config_version TEXT NOT NULL DEFAULT '';
ALTER TABLE processing_jobs ADD COLUMN configured_provider TEXT NOT NULL DEFAULT '';
ALTER TABLE processing_jobs ADD COLUMN configured_model TEXT NOT NULL DEFAULT '';
ALTER TABLE processing_jobs ADD COLUMN checkpoint_json TEXT NOT NULL DEFAULT '';
ALTER TABLE processing_jobs ADD COLUMN result_json TEXT NOT NULL DEFAULT '';
ALTER TABLE processing_jobs ADD COLUMN result_state TEXT NOT NULL DEFAULT ''; -- ''|complete|unknown

CREATE UNIQUE INDEX IF NOT EXISTS idx_jobs_intent_active
    ON processing_jobs(source_type, source_id, job_type, intent_id)
    WHERE status IN ('queued','running') AND intent_id != '';
