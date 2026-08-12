-- Migration 0017: KeyPoint 素材 Inbox（ADR-0021 / roadmap Phase 9）
--
-- 旧 keypoint_index 是可整体重建的只读投影。现在保留该表和 FTS 索引，
-- 但把 Owner 的策展状态和手工 KeyPoint 放入同一稳定素材层。

ALTER TABLE keypoint_index ADD COLUMN origin TEXT NOT NULL DEFAULT 'automatic';
ALTER TABLE keypoint_index ADD COLUMN production_status TEXT NOT NULL DEFAULT 'inbox';
ALTER TABLE keypoint_index ADD COLUMN parent_keypoint_id TEXT;
ALTER TABLE keypoint_index ADD COLUMN evidence_status TEXT NOT NULL DEFAULT 'valid';

CREATE INDEX IF NOT EXISTS idx_keypoints_inbox ON keypoint_index(production_status, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_keypoints_origin_source ON keypoint_index(source_type, source_id, origin);
