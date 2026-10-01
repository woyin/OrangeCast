-- Supplier unit availability is independent of price availability.
ALTER TABLE usage_records ADD COLUMN units_known INTEGER NOT NULL DEFAULT 1 CHECK(units_known IN(0,1));
CREATE TABLE knowledge_embedding_requests (
 request_key TEXT PRIMARY KEY,payload_hash TEXT NOT NULL,
 job_id TEXT NOT NULL REFERENCES processing_jobs(id) ON DELETE CASCADE,
 created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE TRIGGER embedding_job_episode_purge AFTER DELETE ON episodes BEGIN
 UPDATE processing_jobs SET status=CASE WHEN status='queued' THEN 'failed' ELSE status END,stop_requested=1,last_error='关联来源已清理',result_state=CASE WHEN result_state='complete' OR checkpoint_json!='' THEN 'complete' WHEN remote_call_started=1 THEN 'unknown' ELSE result_state END,input_snapshot_json='{}',checkpoint_json='',result_json='{"purged":true}' WHERE source_type='knowledge_index' AND EXISTS(SELECT 1 FROM json_each(CASE WHEN json_valid(input_snapshot_json) THEN input_snapshot_json ELSE '{}' END,'$.windows') w,json_each(w.value,'$.sources') ref WHERE json_extract(ref.value,'$.source_type')='episode' AND json_extract(ref.value,'$.source_id')=old.id);
END;
CREATE TRIGGER embedding_job_upload_purge AFTER DELETE ON uploads BEGIN
 UPDATE processing_jobs SET status=CASE WHEN status='queued' THEN 'failed' ELSE status END,stop_requested=1,last_error='关联来源已清理',result_state=CASE WHEN result_state='complete' OR checkpoint_json!='' THEN 'complete' WHEN remote_call_started=1 THEN 'unknown' ELSE result_state END,input_snapshot_json='{}',checkpoint_json='',result_json='{"purged":true}' WHERE source_type='knowledge_index' AND EXISTS(SELECT 1 FROM json_each(CASE WHEN json_valid(input_snapshot_json) THEN input_snapshot_json ELSE '{}' END,'$.windows') w,json_each(w.value,'$.sources') ref WHERE json_extract(ref.value,'$.source_type')='upload' AND json_extract(ref.value,'$.source_id')=old.id);
END;
CREATE TRIGGER embedding_job_document_purge AFTER DELETE ON documents BEGIN
 UPDATE processing_jobs SET status=CASE WHEN status='queued' THEN 'failed' ELSE status END,stop_requested=1,last_error='关联来源已清理',result_state=CASE WHEN result_state='complete' OR checkpoint_json!='' THEN 'complete' WHEN remote_call_started=1 THEN 'unknown' ELSE result_state END,input_snapshot_json='{}',checkpoint_json='',result_json='{"purged":true}' WHERE source_type='knowledge_index' AND EXISTS(SELECT 1 FROM json_each(CASE WHEN json_valid(input_snapshot_json) THEN input_snapshot_json ELSE '{}' END,'$.windows') w,json_each(w.value,'$.sources') ref WHERE json_extract(ref.value,'$.source_type')='document' AND json_extract(ref.value,'$.source_id')=old.id);
END;
CREATE TRIGGER embedding_job_snapshot_purge AFTER UPDATE OF status ON source_snapshots WHEN new.status='purged' BEGIN
 UPDATE processing_jobs SET status=CASE WHEN status='queued' THEN 'failed' ELSE status END,stop_requested=1,last_error='关联来源已清理',result_state=CASE WHEN result_state='complete' OR checkpoint_json!='' THEN 'complete' WHEN remote_call_started=1 THEN 'unknown' ELSE result_state END,input_snapshot_json='{}',checkpoint_json='',result_json='{"purged":true}' WHERE source_type='knowledge_index' AND EXISTS(SELECT 1 FROM json_each(CASE WHEN json_valid(input_snapshot_json) THEN input_snapshot_json ELSE '{}' END,'$.windows') w,json_each(w.value,'$.sources') ref WHERE json_extract(ref.value,'$.source_type')=new.source_type AND json_extract(ref.value,'$.source_id')=new.source_id);
END;
