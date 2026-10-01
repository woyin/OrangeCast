CREATE TABLE learning_preferences (
 id INTEGER PRIMARY KEY CHECK(id=1),
 current_question_id TEXT REFERENCES learning_questions(id) ON DELETE SET NULL,
 reflection_prompt INTEGER NOT NULL DEFAULT 0 CHECK(reflection_prompt IN(0,1)),
 revision INTEGER NOT NULL DEFAULT 1,
 updated_at TEXT NOT NULL DEFAULT(datetime('now'))
);
INSERT INTO learning_preferences(id) VALUES(1);
CREATE TABLE learning_preference_actions (
 request_key TEXT PRIMARY KEY, payload_hash TEXT NOT NULL,
 created_at TEXT NOT NULL DEFAULT(datetime('now'))
);
CREATE TRIGGER learning_preference_question_clear AFTER UPDATE OF current_question_id ON learning_preferences
 WHEN NEW.current_question_id IS NOT OLD.current_question_id AND NEW.revision=OLD.revision
 BEGIN UPDATE learning_preferences SET revision=revision+1,updated_at=datetime('now') WHERE id=1; END;
