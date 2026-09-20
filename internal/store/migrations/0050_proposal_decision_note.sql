-- R16（C06 / ADR-0024 §6）：候选决策原子释放批次。
-- creation_proposals.decision_note 保留拒绝原因（拒绝来历）；接受时 Owner 编辑后
-- 的主张写入 owner_claim，来历（material_ids/history_relationship）不变。

ALTER TABLE creation_proposals ADD COLUMN decision_note TEXT NOT NULL DEFAULT '';

-- 迁移兼容回填：旧库中 ready 批次若已没有任何 proposed 候选，说明候选已被
-- 历史路径处理完或本来就是零候选；不能继续占用自动发现背压。保留既有
-- completed_at（通常为空），只在缺失时补当前时间。
UPDATE proposal_batches
SET status='completed', completed_at=COALESCE(completed_at, datetime('now'))
WHERE status='ready'
  AND NOT EXISTS (
    SELECT 1 FROM creation_proposals
    WHERE creation_proposals.proposal_batch_id=proposal_batches.id
      AND creation_proposals.status='proposed'
  );
