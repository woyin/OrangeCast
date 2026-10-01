CREATE TABLE listening_queue_state (
 id INTEGER PRIMARY KEY CHECK(id=1),revision INTEGER NOT NULL DEFAULT 0,
 current_item_id TEXT NOT NULL DEFAULT '',autoplay INTEGER NOT NULL DEFAULT 0 CHECK(autoplay IN(0,1))
);
INSERT INTO listening_queue_state(id)VALUES(1);
CREATE TABLE listening_queue_entries (
 id TEXT PRIMARY KEY,source_type TEXT NOT NULL CHECK(source_type IN('episode','upload')),
 source_id TEXT NOT NULL,mode TEXT NOT NULL CHECK(mode IN('original','dj')),
 plan_id TEXT NOT NULL DEFAULT '',plan_version INTEGER NOT NULL DEFAULT 0,
 audio_sha256 TEXT NOT NULL DEFAULT '',title TEXT NOT NULL DEFAULT '',position INTEGER NOT NULL,
 invalid_reason TEXT NOT NULL DEFAULT '',created_at TEXT NOT NULL DEFAULT(datetime('now')),
 UNIQUE(source_type,source_id,mode,plan_id,plan_version)
);
CREATE INDEX idx_listening_queue_order ON listening_queue_entries(position,id);
CREATE TRIGGER listening_queue_episode_purge AFTER DELETE ON episodes BEGIN
 UPDATE listening_queue_state SET current_item_id=CASE WHEN current_item_id IN(SELECT id FROM listening_queue_entries WHERE source_type='episode' AND source_id=old.id) THEN '' ELSE current_item_id END,revision=revision+1 WHERE EXISTS(SELECT 1 FROM listening_queue_entries WHERE source_type='episode' AND source_id=old.id);
 UPDATE listening_queue_entries SET invalid_reason='来源已删除',title='已删除来源',audio_sha256='' WHERE source_type='episode' AND source_id=old.id;
END;
CREATE TRIGGER listening_queue_upload_purge AFTER DELETE ON uploads BEGIN
 UPDATE listening_queue_state SET current_item_id=CASE WHEN current_item_id IN(SELECT id FROM listening_queue_entries WHERE source_type='upload' AND source_id=old.id) THEN '' ELSE current_item_id END,revision=revision+1 WHERE EXISTS(SELECT 1 FROM listening_queue_entries WHERE source_type='upload' AND source_id=old.id);
 UPDATE listening_queue_entries SET invalid_reason='来源已删除',title='已删除来源',audio_sha256='' WHERE source_type='upload' AND source_id=old.id;
END;
ALTER TABLE listening_progress ADD COLUMN audio_sha256 TEXT NOT NULL DEFAULT '';
