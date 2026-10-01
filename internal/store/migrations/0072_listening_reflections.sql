CREATE TABLE listening_reflections (
 id TEXT PRIMARY KEY,
 source_type TEXT NOT NULL CHECK(source_type IN('episode','upload')),
 source_id TEXT NOT NULL,
 capture_json TEXT NOT NULL CHECK(json_valid(capture_json)),
 answers_json TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(answers_json)),
 question_id TEXT NOT NULL DEFAULT '',
 question_revision INTEGER NOT NULL DEFAULT 0,
 start_hash TEXT NOT NULL,
 revision INTEGER NOT NULL DEFAULT 1 CHECK(revision>0),
 state TEXT NOT NULL DEFAULT 'draft' CHECK(state IN('draft','saved','cancelled','expired','unavailable')),
 saved_note_id TEXT NOT NULL DEFAULT '',
 expires_at TEXT NOT NULL DEFAULT (datetime('now','+7 days')),
 created_at TEXT NOT NULL DEFAULT (datetime('now')),
 updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX listening_reflections_source ON listening_reflections(source_type,source_id);
CREATE TABLE listening_reflection_actions (
 request_key TEXT PRIMARY KEY,
 reflection_id TEXT NOT NULL REFERENCES listening_reflections(id) ON DELETE CASCADE,
 payload_hash TEXT NOT NULL,
 result_json TEXT NOT NULL CHECK(json_valid(result_json))
);
CREATE TRIGGER listening_reflection_purge AFTER UPDATE OF status ON source_snapshots
WHEN NEW.status='purged' BEGIN
 UPDATE listening_reflections SET state='unavailable',capture_json='{}',answers_json='{}',revision=revision+1,updated_at=datetime('now')
 WHERE source_type=NEW.source_type AND source_id=NEW.source_id AND state!='unavailable';
 DELETE FROM listening_reflection_actions WHERE reflection_id IN(SELECT id FROM listening_reflections WHERE source_type=NEW.source_type AND source_id=NEW.source_id);
END;
CREATE TRIGGER listening_reflection_episode_delete AFTER DELETE ON episodes BEGIN
 UPDATE listening_reflections SET state='unavailable',capture_json='{}',answers_json='{}',revision=revision+1 WHERE source_type='episode' AND source_id=OLD.id;
 DELETE FROM listening_reflection_actions WHERE reflection_id IN(SELECT id FROM listening_reflections WHERE source_type='episode' AND source_id=OLD.id);
END;
CREATE TRIGGER listening_reflection_upload_delete AFTER DELETE ON uploads BEGIN
 UPDATE listening_reflections SET state='unavailable',capture_json='{}',answers_json='{}',revision=revision+1 WHERE source_type='upload' AND source_id=OLD.id;
 DELETE FROM listening_reflection_actions WHERE reflection_id IN(SELECT id FROM listening_reflections WHERE source_type='upload' AND source_id=OLD.id);
END;
