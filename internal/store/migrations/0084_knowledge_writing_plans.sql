CREATE TABLE knowledge_writing_plans (
 article_id TEXT NOT NULL REFERENCES knowledge_articles(id) ON DELETE CASCADE,
 parent_revision INTEGER NOT NULL,
 revision INTEGER NOT NULL CHECK(revision>0),
 hash TEXT NOT NULL,
 request_json TEXT NOT NULL,
 provider TEXT NOT NULL,
 model TEXT NOT NULL,
 automated INTEGER NOT NULL DEFAULT 0,
 state TEXT NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','superseded','confirmed')),
 job_id TEXT NOT NULL DEFAULT '',
 created_at TEXT NOT NULL DEFAULT (datetime('now')),
 PRIMARY KEY(article_id,revision)
);
CREATE UNIQUE INDEX knowledge_writing_plan_pending ON knowledge_writing_plans(article_id) WHERE state='pending';

ALTER TABLE knowledge_article_settings ADD COLUMN writing_mode TEXT NOT NULL DEFAULT 'synthesis';
ALTER TABLE knowledge_article_settings ADD COLUMN preview_writing_plan INTEGER NOT NULL DEFAULT 0;
