ALTER TABLE owner_notes ADD COLUMN anchor_json TEXT NOT NULL DEFAULT '{}';
CREATE TABLE owner_note_revisions (
 note_id TEXT NOT NULL, revision INTEGER NOT NULL,
 source_type TEXT NOT NULL, source_id TEXT NOT NULL,kind TEXT NOT NULL,
 content TEXT NOT NULL,citations_json TEXT NOT NULL,references_json TEXT NOT NULL,anchor_json TEXT NOT NULL,
 created_at TEXT NOT NULL DEFAULT (datetime('now')),
 PRIMARY KEY(note_id,revision)
);
-- Backfill only known current content. Earlier revision counts are not evidence.
INSERT INTO owner_note_revisions SELECT id,revision,source_type,source_id,kind,content,citations_json,references_json,anchor_json,updated_at FROM owner_notes;
CREATE TRIGGER owner_note_revision_insert AFTER INSERT ON owner_notes BEGIN
 INSERT INTO owner_note_revisions(note_id,revision,source_type,source_id,kind,content,citations_json,references_json,anchor_json)
 VALUES(new.id,new.revision,new.source_type,new.source_id,new.kind,new.content,new.citations_json,new.references_json,new.anchor_json);
END;
CREATE TRIGGER owner_note_revision_update AFTER UPDATE ON owner_notes WHEN new.revision!=old.revision BEGIN
 INSERT INTO owner_note_revisions(note_id,revision,source_type,source_id,kind,content,citations_json,references_json,anchor_json)
 VALUES(new.id,new.revision,new.source_type,new.source_id,new.kind,new.content,new.citations_json,new.references_json,new.anchor_json);
END;
