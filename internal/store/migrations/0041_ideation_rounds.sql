-- C02（ADR-0024 §1）：定向构思轮次。
-- 每轮冻结用户输入与前一轮身份；材料快照与输出引用挂轮次；
-- client_nonce 唯一：并发/重复提交同一轮只产生一条（幂等 key）。

CREATE TABLE ideation_rounds (
    id                  TEXT PRIMARY KEY,
    session_id          TEXT NOT NULL REFERENCES ideation_sessions(id) ON DELETE CASCADE,
    round_no            INTEGER NOT NULL,
    prev_round_id       TEXT NOT NULL DEFAULT '',
    client_nonce        TEXT NOT NULL,
    user_input          TEXT NOT NULL DEFAULT '',
    constraints_json    TEXT NOT NULL DEFAULT '{}',
    material_snapshot_json TEXT NOT NULL DEFAULT '[]',
    status              TEXT NOT NULL DEFAULT 'recorded', -- recorded | diagnosed | failed
    output_diagnosis_id TEXT NOT NULL DEFAULT '',
    created_at          TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE(session_id, round_no),
    UNIQUE(session_id, client_nonce)
);
CREATE INDEX IF NOT EXISTS idx_ideation_rounds_session ON ideation_rounds(session_id, round_no);
