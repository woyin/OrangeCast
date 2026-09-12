-- B03（ADR-0024 §5 / ADR-0009）：以远端调用身份唯一记账。
-- receipt_id 标识一次远端调用（任务 attempt 内的调用序），重放/重复写账不重复累计；
-- estimated_cost 为 NULL 表示价格未知（不冒充免费），已知零成本（本地 TTS）写 0。

ALTER TABLE usage_records ADD COLUMN receipt_id TEXT NOT NULL DEFAULT '';
ALTER TABLE usage_records ADD COLUMN attempt_id TEXT NOT NULL DEFAULT '';

CREATE UNIQUE INDEX IF NOT EXISTS idx_usage_receipt ON usage_records(receipt_id) WHERE receipt_id != '';
