CREATE TABLE question_study_sessions (
 id TEXT PRIMARY KEY,
 question_id TEXT NOT NULL REFERENCES learning_questions(id) ON DELETE CASCADE,
 request_key TEXT NOT NULL UNIQUE,
 revision INTEGER NOT NULL DEFAULT 1,
 created_at TEXT NOT NULL DEFAULT (datetime('now')),
 updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX question_study_sessions_question ON question_study_sessions(question_id,updated_at,id);
CREATE TABLE question_study_turns (
 id TEXT PRIMARY KEY,
 session_id TEXT NOT NULL REFERENCES question_study_sessions(id) ON DELETE CASCADE,
 ordinal INTEGER NOT NULL CHECK(ordinal BETWEEN 1 AND 12),
 request_key TEXT NOT NULL UNIQUE,
 payload_hash TEXT NOT NULL,
 state TEXT NOT NULL DEFAULT 'queued' CHECK(state IN('queued','running','response_saved','checking','accepted','insufficient','unknown','blocked')),
 owner_input TEXT NOT NULL,
 frozen_json TEXT NOT NULL DEFAULT '{}',
 accepted_json TEXT NOT NULL DEFAULT '',
 generation_job_id TEXT REFERENCES processing_jobs(id) ON DELETE SET NULL,
 check_job_id TEXT REFERENCES processing_jobs(id) ON DELETE SET NULL,
 purged INTEGER NOT NULL DEFAULT 0 CHECK(purged IN(0,1)),
 created_at TEXT NOT NULL DEFAULT (datetime('now')),
 updated_at TEXT NOT NULL DEFAULT (datetime('now')),
 UNIQUE(session_id,ordinal)
);
CREATE TABLE question_study_sources (
 turn_id TEXT NOT NULL REFERENCES question_study_turns(id) ON DELETE CASCADE,
 source_type TEXT NOT NULL CHECK(source_type IN('episode','upload','document')),
 source_id TEXT NOT NULL,
 PRIMARY KEY(turn_id,source_type,source_id)
);
CREATE INDEX question_study_sources_source ON question_study_sources(source_type,source_id);
CREATE TRIGGER question_study_turn_delete BEFORE DELETE ON question_study_turns BEGIN
 UPDATE processing_jobs SET stop_requested=1,input_snapshot_json='{}',checkpoint_json='',result_json='{"purged":true}',last_error='问题或关联来源已清理',status=CASE WHEN status='queued' THEN 'failed' ELSE status END WHERE id IN(old.generation_job_id,old.check_job_id);
END;
CREATE TRIGGER question_study_turn_purge AFTER UPDATE OF purged ON question_study_turns WHEN new.purged=1 AND old.purged=0 BEGIN
 UPDATE question_study_sessions SET revision=revision+1,updated_at=datetime('now') WHERE id=new.session_id;
 UPDATE processing_jobs SET stop_requested=1,input_snapshot_json='{}',checkpoint_json='',result_json='{"purged":true}',last_error='关联来源已清理',status=CASE WHEN status='queued' THEN 'failed' ELSE status END WHERE id IN(new.generation_job_id,new.check_job_id);
END;
CREATE TRIGGER question_study_episode_delete BEFORE DELETE ON episodes BEGIN
 UPDATE question_study_turns SET purged=1,state='blocked',owner_input='',frozen_json='{}',accepted_json='',updated_at=datetime('now') WHERE id IN(SELECT turn_id FROM question_study_sources WHERE source_type='episode' AND source_id=old.id);
END;
CREATE TRIGGER question_study_upload_delete BEFORE DELETE ON uploads BEGIN
 UPDATE question_study_turns SET purged=1,state='blocked',owner_input='',frozen_json='{}',accepted_json='',updated_at=datetime('now') WHERE id IN(SELECT turn_id FROM question_study_sources WHERE source_type='upload' AND source_id=old.id);
END;
CREATE TRIGGER question_study_document_delete BEFORE DELETE ON documents BEGIN
 UPDATE question_study_turns SET purged=1,state='blocked',owner_input='',frozen_json='{}',accepted_json='',updated_at=datetime('now') WHERE id IN(SELECT turn_id FROM question_study_sources WHERE source_type='document' AND source_id=old.id);
END;
CREATE TRIGGER question_study_snapshot_purge AFTER UPDATE OF status ON source_snapshots WHEN new.status='purged' BEGIN
 UPDATE question_study_turns SET purged=1,state='blocked',owner_input='',frozen_json='{}',accepted_json='',updated_at=datetime('now') WHERE id IN(SELECT turn_id FROM question_study_sources WHERE source_type=new.source_type AND source_id=new.source_id);
END;
