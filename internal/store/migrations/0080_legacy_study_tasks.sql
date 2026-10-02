ALTER TABLE study_sessions ADD COLUMN revision INTEGER NOT NULL DEFAULT 1;
CREATE TABLE legacy_study_turns (
 id TEXT PRIMARY KEY,
 session_id TEXT NOT NULL REFERENCES study_sessions(id) ON DELETE CASCADE,
 request_key TEXT NOT NULL UNIQUE,
 state TEXT NOT NULL DEFAULT 'queued' CHECK(state IN('queued','running','response_saved','checking','accepted','insufficient','unknown','blocked')),
 generation_job_id TEXT REFERENCES processing_jobs(id) ON DELETE SET NULL,
 check_job_id TEXT REFERENCES processing_jobs(id) ON DELETE SET NULL,
 feedback TEXT NOT NULL DEFAULT '',
 answer_message_id TEXT REFERENCES study_messages(id) ON DELETE SET NULL,
 created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE TABLE legacy_study_requests (
 request_key TEXT PRIMARY KEY,
 payload_hash TEXT NOT NULL,
 turn_id TEXT NOT NULL REFERENCES legacy_study_turns(id) ON DELETE CASCADE,
 job_id TEXT NOT NULL REFERENCES processing_jobs(id) ON DELETE CASCADE
);
CREATE TRIGGER legacy_study_turn_delete BEFORE DELETE ON legacy_study_turns BEGIN
 UPDATE processing_jobs SET stop_requested=1,input_snapshot_json='{}',checkpoint_json='',result_json='{"purged":true}',last_error='旧学习会话或来源已清理',status=CASE WHEN status='queued' THEN 'failed' ELSE status END WHERE id IN(old.generation_job_id,old.check_job_id);
END;
CREATE TRIGGER legacy_study_episode_delete BEFORE DELETE ON episodes BEGIN
 DELETE FROM study_sessions WHERE source_type='episode' AND source_id=old.id;
END;
CREATE TRIGGER legacy_study_upload_delete BEFORE DELETE ON uploads BEGIN
 DELETE FROM study_sessions WHERE source_type='upload' AND source_id=old.id;
END;
CREATE TRIGGER legacy_study_snapshot_purge AFTER UPDATE OF status ON source_snapshots WHEN new.status='purged' BEGIN
 DELETE FROM study_sessions WHERE source_type=new.source_type AND source_id=new.source_id;
END;
