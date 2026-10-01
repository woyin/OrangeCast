CREATE TABLE question_study_requests (
 request_key TEXT PRIMARY KEY,
 payload_hash TEXT NOT NULL,
 turn_id TEXT NOT NULL REFERENCES question_study_turns(id) ON DELETE CASCADE,
 job_id TEXT NOT NULL REFERENCES processing_jobs(id) ON DELETE CASCADE,
 created_at TEXT NOT NULL DEFAULT(datetime('now'))
);
