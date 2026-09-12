-- G03（ADR-0024 §5 / ADR-0023）：精读修订血缘。
-- parent_digest_id 指向被调整的基础修订；reason 记录调整原因（如"剔除来源"）；
-- source_snapshot_id 关联 B01 来源快照。旧版本永不原地覆盖，均可审计。

ALTER TABLE episode_digests ADD COLUMN parent_digest_id TEXT NOT NULL DEFAULT '';
ALTER TABLE episode_digests ADD COLUMN reason TEXT NOT NULL DEFAULT '';
ALTER TABLE episode_digests ADD COLUMN source_snapshot_id TEXT NOT NULL DEFAULT '';
