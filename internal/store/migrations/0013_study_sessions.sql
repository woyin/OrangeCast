-- Migration 0013: StudySession 学习会话（GeneratedDerivative，ADR-0018 R3）
--
-- StudyChat 是围绕单一 Source 的多轮学习对话，属 GeneratedDerivative，挂 Reference（relation_kind='reference'）。
-- 它不纳入 ArtifactVersion（多轮有状态、无版本比较价值），而是按会话保存：
--   study_sessions = 一次会话的容器（绑定一个 Source）
--   study_messages = 该会话内每一轮消息（user 问题 / assistant 回答 + 所挂 Reference）
-- StudySession 可由 Owner 整体删除；Purge 一个 Source 时连带删除其全部 StudySession。

CREATE TABLE IF NOT EXISTS study_sessions (
    id          TEXT PRIMARY KEY,
    source_type TEXT NOT NULL,
    source_id   TEXT NOT NULL,
    title       TEXT NOT NULL DEFAULT '',   -- 首个问题的摘要，便于 Owner 识别会话
    created_at  TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at  TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_study_sessions_source ON study_sessions(source_type, source_id, created_at DESC);

CREATE TABLE IF NOT EXISTS study_messages (
    id                  TEXT PRIMARY KEY,
    session_id          TEXT NOT NULL REFERENCES study_sessions(id) ON DELETE CASCADE,
    role                TEXT NOT NULL,                 -- user | assistant
    content             TEXT NOT NULL,
    reference_segment_ids TEXT NOT NULL DEFAULT '[]',  -- JSON 数组，assistant 回答所参考的 Segment（Reference）
    relation_kind       TEXT NOT NULL DEFAULT 'reference',
    suppressed          INTEGER NOT NULL DEFAULT 0,    -- ReferenceCheck 失败被抑制时为 1
    created_at          TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_study_messages_session ON study_messages(session_id, created_at);
