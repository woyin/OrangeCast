CREATE TABLE review_schedule_settings (
 id INTEGER PRIMARY KEY CHECK(id=1), session_size INTEGER NOT NULL DEFAULT 3 CHECK(session_size BETWEEN 1 AND 5)
);
INSERT INTO review_schedule_settings(id) VALUES(1);
CREATE TABLE review_schedules (
 item_id TEXT PRIMARY KEY REFERENCES learning_review_items(id) ON DELETE CASCADE,
 due_utc TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
 status TEXT NOT NULL DEFAULT 'active' CHECK(status IN('active','paused','ended')),
 timezone TEXT NOT NULL DEFAULT 'UTC', rule_version TEXT NOT NULL DEFAULT 'review-calendar-v1',
 streak INTEGER NOT NULL DEFAULT 0, revision INTEGER NOT NULL DEFAULT 1,
 last_answered_utc TEXT NOT NULL DEFAULT '', last_presented_utc TEXT NOT NULL DEFAULT '',
 question_id TEXT REFERENCES learning_questions(id) ON DELETE SET NULL
);
CREATE INDEX review_schedule_due ON review_schedules(status,due_utc,last_presented_utc,item_id);
INSERT INTO review_schedules(item_id) SELECT id FROM learning_review_items;
CREATE TRIGGER review_item_schedule AFTER INSERT ON learning_review_items BEGIN
 INSERT INTO review_schedules(item_id) VALUES(NEW.id);
END;
CREATE TABLE review_sessions (
 id TEXT PRIMARY KEY, request_key TEXT NOT NULL UNIQUE,
 status TEXT NOT NULL DEFAULT 'active' CHECK(status IN('active','complete','ended')),
 revision INTEGER NOT NULL DEFAULT 1, timezone TEXT NOT NULL,
 created_at TEXT NOT NULL, ended_at TEXT NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX one_active_review_session ON review_sessions(status) WHERE status='active';
CREATE TABLE review_session_items (
 session_id TEXT NOT NULL REFERENCES review_sessions(id) ON DELETE CASCADE,
 item_id TEXT NOT NULL REFERENCES learning_review_items(id) ON DELETE CASCADE,
 position INTEGER NOT NULL, question TEXT NOT NULL, answer_basis TEXT NOT NULL,
 input_json TEXT NOT NULL, material_ids_json TEXT NOT NULL,
 item_revision INTEGER NOT NULL, revision INTEGER NOT NULL DEFAULT 1,
 state TEXT NOT NULL DEFAULT 'pending' CHECK(state IN('pending','answered','later')),
 revealed INTEGER NOT NULL DEFAULT 0, answer TEXT NOT NULL DEFAULT '', assessment TEXT NOT NULL DEFAULT '',
 PRIMARY KEY(session_id,item_id), UNIQUE(session_id,position)
);
CREATE TABLE review_owner_actions (
 request_key TEXT PRIMARY KEY, target_type TEXT NOT NULL, target_id TEXT NOT NULL,
 action TEXT NOT NULL, payload_hash TEXT NOT NULL, created_at TEXT NOT NULL
);
ALTER TABLE learning_review_answers ADD COLUMN note_id TEXT NOT NULL DEFAULT '';
UPDATE learning_review_answers SET note_id=COALESCE((SELECT i.note_id FROM learning_review_items i WHERE i.id=learning_review_answers.item_id AND i.revision=learning_review_answers.revision),'');
