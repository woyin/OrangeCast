CREATE TABLE listening_progress_v2 (
 id TEXT PRIMARY KEY, source_type TEXT NOT NULL, source_id TEXT NOT NULL,
 mode TEXT NOT NULL CHECK(mode IN ('original','dj')),
 plan_id TEXT NOT NULL DEFAULT '', plan_version INTEGER NOT NULL DEFAULT 0,
 item_position INTEGER NOT NULL DEFAULT 0, highlight_id TEXT NOT NULL DEFAULT '',
 item_offset_seconds REAL NOT NULL DEFAULT 0, speed REAL NOT NULL DEFAULT 1,
 seq INTEGER NOT NULL DEFAULT 0, revision INTEGER NOT NULL DEFAULT 1,
 updated_at TEXT NOT NULL DEFAULT (datetime('now')),
 UNIQUE(source_type,source_id,mode)
);
INSERT INTO listening_progress_v2
 SELECT id,source_type,source_id,CASE WHEN plan_id='' THEN 'original' ELSE 'dj' END,
 plan_id,plan_version,item_position,highlight_id,item_offset_seconds,speed,seq,1,updated_at
 FROM listening_progress;
DROP TABLE listening_progress;
ALTER TABLE listening_progress_v2 RENAME TO listening_progress;
