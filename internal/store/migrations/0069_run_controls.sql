-- Controls preserve execution facts and do not introduce cancelled job states.
ALTER TABLE processing_jobs ADD COLUMN control_revision INTEGER NOT NULL DEFAULT 1;
ALTER TABLE processing_jobs ADD COLUMN stop_requested INTEGER NOT NULL DEFAULT 0 CHECK(stop_requested IN(0,1));
ALTER TABLE processing_jobs ADD COLUMN priority INTEGER NOT NULL DEFAULT 0 CHECK(priority BETWEEN -10 AND 10);
ALTER TABLE processing_jobs ADD COLUMN run_lane TEXT NOT NULL DEFAULT '';
CREATE TABLE run_controls(
 kind TEXT NOT NULL CHECK(kind IN('lane','direction')),target TEXT NOT NULL,
 paused INTEGER NOT NULL DEFAULT 0 CHECK(paused IN(0,1)),revision INTEGER NOT NULL DEFAULT 1,
 reason TEXT NOT NULL DEFAULT '',updated_at TEXT NOT NULL DEFAULT(datetime('now')),
 PRIMARY KEY(kind,target)
);
INSERT INTO run_controls(kind,target) VALUES('lane','knowledge'),('lane','updates'),('lane','review'),('lane','voice');
CREATE TABLE run_control_actions(
 request_key TEXT PRIMARY KEY,kind TEXT NOT NULL,target TEXT NOT NULL,action TEXT NOT NULL,
 payload_hash TEXT NOT NULL,reason TEXT NOT NULL,revision INTEGER NOT NULL,created_at TEXT NOT NULL DEFAULT(datetime('now'))
);
CREATE INDEX run_control_actions_target ON run_control_actions(kind,target,created_at);
UPDATE processing_jobs SET run_lane=CASE WHEN source_type='knowledge_article' THEN CASE WHEN json_extract(CASE WHEN json_valid(input_snapshot_json) THEN input_snapshot_json ELSE '{}' END,'$.request.update') IS NOT NULL OR json_extract(CASE WHEN json_valid(input_snapshot_json) THEN input_snapshot_json ELSE '{}' END,'$.stage')='update_propose' THEN 'updates' ELSE 'knowledge' END WHEN job_type='weekly_review' THEN 'review' WHEN source_type='voice_note' THEN 'voice' ELSE '' END;
CREATE TRIGGER run_control_admission BEFORE INSERT ON processing_jobs
 WHEN EXISTS(SELECT 1 FROM run_controls WHERE paused=1 AND
 ((kind='lane' AND target=(CASE WHEN NEW.source_type='knowledge_article' THEN CASE WHEN json_extract(CASE WHEN json_valid(NEW.input_snapshot_json) THEN NEW.input_snapshot_json ELSE '{}' END,'$.request.update') IS NOT NULL OR json_extract(CASE WHEN json_valid(NEW.input_snapshot_json) THEN NEW.input_snapshot_json ELSE '{}' END,'$.stage')='update_propose' THEN 'updates' ELSE 'knowledge' END WHEN NEW.job_type='weekly_review' THEN 'review' WHEN NEW.source_type='voice_note' THEN 'voice' ELSE '' END)) OR (kind='direction' AND target=NEW.source_type||':'||NEW.source_id)))
 BEGIN SELECT RAISE(ABORT,'Owner运行控制已暂停'); END;
CREATE TRIGGER run_control_lane AFTER INSERT ON processing_jobs BEGIN
 UPDATE processing_jobs SET run_lane=CASE WHEN NEW.source_type='knowledge_article' THEN CASE WHEN json_extract(CASE WHEN json_valid(NEW.input_snapshot_json) THEN NEW.input_snapshot_json ELSE '{}' END,'$.request.update') IS NOT NULL OR json_extract(CASE WHEN json_valid(NEW.input_snapshot_json) THEN NEW.input_snapshot_json ELSE '{}' END,'$.stage')='update_propose' THEN 'updates' ELSE 'knowledge' END WHEN NEW.job_type='weekly_review' THEN 'review' WHEN NEW.source_type='voice_note' THEN 'voice' ELSE '' END WHERE id=NEW.id;
 END;
CREATE INDEX run_control_claim ON processing_jobs(stop_requested,status,priority DESC,created_at);
