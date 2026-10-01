CREATE TABLE voice_reflection_adoptions (
 voice_id TEXT PRIMARY KEY REFERENCES voice_note_drafts(id) ON DELETE CASCADE,
 reflection_id TEXT NOT NULL REFERENCES listening_reflections(id) ON DELETE CASCADE,
 field TEXT NOT NULL CHECK(field IN('remember','uncertain','apply')),
 voice_revision INTEGER NOT NULL,
 created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX voice_reflection_target ON voice_reflection_adoptions(reflection_id);
