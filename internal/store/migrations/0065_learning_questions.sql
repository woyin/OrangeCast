CREATE TABLE learning_questions (
 id TEXT PRIMARY KEY, body TEXT NOT NULL, goal TEXT NOT NULL DEFAULT '',
 theme_id TEXT REFERENCES themes(id) ON DELETE SET NULL, target_date TEXT NOT NULL DEFAULT '',
 status TEXT NOT NULL DEFAULT 'active' CHECK(status IN ('active','paused','resolved','archived')),
 revision INTEGER NOT NULL DEFAULT 1, created_at TEXT NOT NULL DEFAULT (datetime('now')), updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX learning_questions_status ON learning_questions(status,updated_at DESC,id);
CREATE TABLE learning_question_links (
 question_id TEXT NOT NULL REFERENCES learning_questions(id) ON DELETE CASCADE,
 kind TEXT NOT NULL CHECK(kind IN ('source','keypoint','note','evidence','article')),
 object_id TEXT NOT NULL, source_type TEXT NOT NULL DEFAULT '', source_id TEXT NOT NULL DEFAULT '',
 version INTEGER NOT NULL DEFAULT 0, state TEXT NOT NULL CHECK(state IN ('confirmed','suggested')),
 origin TEXT NOT NULL DEFAULT 'owner', created_at TEXT NOT NULL DEFAULT (datetime('now')),
 PRIMARY KEY(question_id,kind,object_id)
);
CREATE INDEX learning_question_links_source ON learning_question_links(source_type,source_id);
CREATE INDEX learning_question_links_object ON learning_question_links(kind,object_id);
CREATE TABLE learning_question_operations (
 seq INTEGER PRIMARY KEY AUTOINCREMENT, question_id TEXT NOT NULL REFERENCES learning_questions(id) ON DELETE CASCADE,
 revision INTEGER NOT NULL, action TEXT NOT NULL, detail TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX learning_question_operations_question ON learning_question_operations(question_id,seq DESC);
CREATE TRIGGER learning_question_episode_purge BEFORE DELETE ON episodes BEGIN
 INSERT INTO learning_question_operations(question_id,revision,action,detail) SELECT id,revision+1,'source_purged','episode:'||old.id FROM learning_questions WHERE id IN(SELECT question_id FROM learning_question_links WHERE source_type='episode' AND source_id=old.id);
 UPDATE learning_questions SET revision=revision+1,updated_at=datetime('now') WHERE id IN(SELECT question_id FROM learning_question_links WHERE source_type='episode' AND source_id=old.id);
 DELETE FROM learning_question_links WHERE source_type='episode' AND source_id=old.id;
END;
CREATE TRIGGER learning_question_upload_purge BEFORE DELETE ON uploads BEGIN
 INSERT INTO learning_question_operations(question_id,revision,action,detail) SELECT id,revision+1,'source_purged','upload:'||old.id FROM learning_questions WHERE id IN(SELECT question_id FROM learning_question_links WHERE source_type='upload' AND source_id=old.id);
 UPDATE learning_questions SET revision=revision+1,updated_at=datetime('now') WHERE id IN(SELECT question_id FROM learning_question_links WHERE source_type='upload' AND source_id=old.id);
 DELETE FROM learning_question_links WHERE source_type='upload' AND source_id=old.id;
END;
CREATE TRIGGER learning_question_document_purge BEFORE DELETE ON documents BEGIN
 INSERT INTO learning_question_operations(question_id,revision,action,detail) SELECT id,revision+1,'source_purged','document:'||old.id FROM learning_questions WHERE id IN(SELECT question_id FROM learning_question_links WHERE source_type='document' AND source_id=old.id);
 UPDATE learning_questions SET revision=revision+1,updated_at=datetime('now') WHERE id IN(SELECT question_id FROM learning_question_links WHERE source_type='document' AND source_id=old.id);
 DELETE FROM learning_question_links WHERE source_type='document' AND source_id=old.id;
END;
CREATE TRIGGER learning_question_note_delete BEFORE DELETE ON owner_notes BEGIN
 UPDATE learning_questions SET revision=revision+1,updated_at=datetime('now') WHERE id IN(SELECT question_id FROM learning_question_links WHERE kind='note' AND object_id=old.id);
 DELETE FROM learning_question_links WHERE kind='note' AND object_id=old.id;
END;
CREATE TRIGGER learning_question_keypoint_delete BEFORE DELETE ON keypoint_index BEGIN
 UPDATE learning_questions SET revision=revision+1,updated_at=datetime('now') WHERE id IN(SELECT question_id FROM learning_question_links WHERE kind='keypoint' AND object_id=old.id);
 DELETE FROM learning_question_links WHERE kind='keypoint' AND object_id=old.id;
END;
CREATE TRIGGER learning_question_article_delete BEFORE DELETE ON knowledge_articles BEGIN
 DELETE FROM learning_question_links WHERE kind='article' AND object_id=old.id;
END;
ALTER TABLE knowledge_article_settings ADD COLUMN question_id TEXT NOT NULL DEFAULT '';
CREATE TRIGGER learning_question_delete_scope AFTER DELETE ON learning_questions BEGIN
 UPDATE knowledge_article_settings SET question_id='' WHERE question_id=old.id;
END;
