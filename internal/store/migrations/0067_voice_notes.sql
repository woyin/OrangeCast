CREATE TABLE voice_note_drafts (
 id TEXT PRIMARY KEY,source_type TEXT NOT NULL CHECK(source_type IN('episode','upload')),source_id TEXT NOT NULL,
 anchor_json TEXT NOT NULL,upload_sha256 TEXT NOT NULL,audio_sha256 TEXT NOT NULL,audio_file TEXT NOT NULL,
 duration_seconds REAL NOT NULL CHECK(duration_seconds>0 AND duration_seconds<=300),size_bytes INTEGER NOT NULL,
 text TEXT NOT NULL DEFAULT '',revision INTEGER NOT NULL DEFAULT 1,
 state TEXT NOT NULL DEFAULT 'uploaded' CHECK(state IN('uploaded','queued','transcribing','transcribed','failed','saved','deleted')),
 asr_text TEXT NOT NULL DEFAULT '',asr_base_revision INTEGER NOT NULL DEFAULT 0,job_id TEXT NOT NULL DEFAULT '',
 note_id TEXT NOT NULL DEFAULT '',keep_audio INTEGER NOT NULL DEFAULT 0,error TEXT NOT NULL DEFAULT '',
 expires_at TEXT NOT NULL DEFAULT(datetime('now','+7 days')),created_at TEXT NOT NULL DEFAULT(datetime('now')),updated_at TEXT NOT NULL DEFAULT(datetime('now'))
);
CREATE INDEX voice_note_drafts_list ON voice_note_drafts(state,created_at DESC,id);
CREATE INDEX voice_note_drafts_source ON voice_note_drafts(source_type,source_id);
CREATE TABLE voice_audio_cleanup (file TEXT PRIMARY KEY,created_at TEXT NOT NULL DEFAULT(datetime('now')));
CREATE TABLE asr_audio_prices(provider TEXT NOT NULL,model TEXT NOT NULL,cents_per_minute REAL NOT NULL CHECK(cents_per_minute>=0),PRIMARY KEY(provider,model));
ALTER TABLE usage_records ADD COLUMN unit_kind TEXT NOT NULL DEFAULT 'text_tokens';
ALTER TABLE usage_records ADD COLUMN audio_seconds REAL;
