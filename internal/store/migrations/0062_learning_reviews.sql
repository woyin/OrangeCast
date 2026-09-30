CREATE TABLE learning_review_settings (
 id INTEGER PRIMARY KEY CHECK(id=1),enabled INTEGER NOT NULL DEFAULT 0,
 timezone TEXT NOT NULL DEFAULT 'UTC',weekday INTEGER NOT NULL DEFAULT 0,
 clock_time TEXT NOT NULL DEFAULT '18:00',enabled_at TEXT NOT NULL DEFAULT '',updated_at TEXT NOT NULL DEFAULT(datetime('now'))
);
INSERT INTO learning_review_settings(id)VALUES(1);
CREATE TABLE learning_review_batches (
 id TEXT PRIMARY KEY,profile_id TEXT NOT NULL REFERENCES editorial_profiles(id),week_key TEXT NOT NULL,
 timezone TEXT NOT NULL,start_utc TEXT NOT NULL,end_utc TEXT NOT NULL,input_json TEXT NOT NULL,
 provider TEXT NOT NULL,model TEXT NOT NULL,prompt_version TEXT NOT NULL,status TEXT NOT NULL DEFAULT 'queued',
 reason TEXT NOT NULL DEFAULT '',job_id TEXT NOT NULL DEFAULT '',automated INTEGER NOT NULL DEFAULT 0,
 created_at TEXT NOT NULL DEFAULT(datetime('now')),UNIQUE(profile_id,week_key)
);
CREATE TABLE learning_review_items (
 id TEXT PRIMARY KEY,batch_id TEXT NOT NULL REFERENCES learning_review_batches(id) ON DELETE CASCADE,
 position INTEGER NOT NULL,question TEXT NOT NULL,answer_basis TEXT NOT NULL,material_ids_json TEXT NOT NULL,
 state TEXT NOT NULL DEFAULT 'pending',answer TEXT NOT NULL DEFAULT '',assessment TEXT NOT NULL DEFAULT '',
 revision INTEGER NOT NULL DEFAULT 0,note_id TEXT NOT NULL DEFAULT '',revealed INTEGER NOT NULL DEFAULT 0,
 updated_at TEXT NOT NULL DEFAULT(datetime('now')),UNIQUE(batch_id,position)
);
CREATE TABLE learning_review_answers (
 id TEXT PRIMARY KEY,item_id TEXT NOT NULL REFERENCES learning_review_items(id) ON DELETE CASCADE,
 revision INTEGER NOT NULL,answer TEXT NOT NULL,assessment TEXT NOT NULL,state TEXT NOT NULL,
 created_at TEXT NOT NULL DEFAULT(datetime('now')),UNIQUE(item_id,revision)
);
CREATE INDEX idx_review_pending ON learning_review_items(state,batch_id);
