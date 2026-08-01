-- Migration 0004: EvidenceAudio、持久任务 lease、可恢复 Purge（ADR-0005/0006/0010/0012）
--
-- 变更：
--   1. evidence_audio：每个 Source 长期保存的标准化音频（ADR-0005）。
--      - path 为 DATA_DIR/evidence 下的相对路径；sha256 用于校验与备份去重。
--      - 一次处理尝试成功落盘并记录后才允许删除原始输入。
--   2. processing_jobs 增加 lease 列（ADR-0006）：至少一次执行 + 过期回收。
--      - lease_until：当前持有者租约到期时间；heartbeat_at：持有者心跳。
--      - 启动时清空 running（旧进程已死，任务回收为 queued），随后由 worker 重新领取。
--   3. purges：可恢复的两阶段删除（ADR-0012）。先记 intent，再删文件，最后在事务中删 DB 行。
--      - 任一步崩溃后，重启 ResumePurges 会从中断处继续（文件删除幂等）。

CREATE TABLE IF NOT EXISTS evidence_audio (
    id            TEXT PRIMARY KEY,
    source_type   TEXT NOT NULL,
    source_id     TEXT NOT NULL,
    rel_path      TEXT NOT NULL,             -- DATA_DIR/evidence 下相对路径
    format        TEXT NOT NULL DEFAULT 'mp3',
    size_bytes    INTEGER NOT NULL DEFAULT 0,
    sha256        TEXT NOT NULL,
    status        TEXT NOT NULL DEFAULT 'ready', -- ready | missing
    created_at    TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at    TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE(source_type, source_id)
);
CREATE INDEX IF NOT EXISTS idx_evidence_source ON evidence_audio(source_type, source_id);

ALTER TABLE processing_jobs ADD COLUMN lease_until TEXT;
ALTER TABLE processing_jobs ADD COLUMN heartbeat_at TEXT;
CREATE INDEX IF NOT EXISTS idx_jobs_lease ON processing_jobs(status, lease_until);

CREATE TABLE IF NOT EXISTS purges (
    id            TEXT PRIMARY KEY,
    source_type   TEXT NOT NULL,
    source_id     TEXT NOT NULL,
    status        TEXT NOT NULL DEFAULT 'pending', -- pending | done
    created_at    TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE(source_type, source_id)
);
CREATE INDEX IF NOT EXISTS idx_purges_status ON purges(status);
