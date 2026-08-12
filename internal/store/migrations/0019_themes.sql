-- Migration 0019: cross-episode Theme layer (ADR-0021 / roadmap Phase 10).
CREATE TABLE themes (
  id TEXT PRIMARY KEY,
  editorial_profile_id TEXT NOT NULL REFERENCES editorial_profiles(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'suggested' CHECK(status IN ('suggested','confirmed','ignored')),
  created_at TEXT NOT NULL DEFAULT (datetime('now')),
  updated_at TEXT NOT NULL DEFAULT (datetime('now')),
  UNIQUE(editorial_profile_id, name)
);

CREATE TABLE theme_keypoints (
  theme_id TEXT NOT NULL REFERENCES themes(id) ON DELETE CASCADE,
  keypoint_id TEXT NOT NULL REFERENCES keypoint_index(id) ON DELETE CASCADE,
  relationship TEXT NOT NULL DEFAULT 'supports' CHECK(relationship IN ('supports','complements','conflicts')),
  created_at TEXT NOT NULL DEFAULT (datetime('now')),
  PRIMARY KEY(theme_id, keypoint_id)
);

CREATE INDEX idx_themes_profile_status ON themes(editorial_profile_id, status, updated_at DESC);
CREATE INDEX idx_theme_keypoints_keypoint ON theme_keypoints(keypoint_id);
