-- R13（C03 / ADR-0024 §6）：诊断候选提升为提案的持久关联与幂等。
-- creation_proposals.ideation_round_id 记录提案来源轮次（不承担 OwnerClaim）；
-- 同一轮次同一主张只允许一个提案（部分唯一索引），重复提升动作幂等。

ALTER TABLE creation_proposals ADD COLUMN ideation_round_id TEXT NOT NULL DEFAULT '';

CREATE UNIQUE INDEX IF NOT EXISTS idx_creation_proposals_round_claim
    ON creation_proposals(ideation_round_id, proposed_claim) WHERE ideation_round_id != '';
