CREATE TABLE knowledge_article_settings (
 id INTEGER PRIMARY KEY CHECK(id=1),
 enabled INTEGER NOT NULL DEFAULT 0,
 daily_limit INTEGER NOT NULL DEFAULT 1,
 debounce_minutes INTEGER NOT NULL DEFAULT 30,
 audience TEXT NOT NULL DEFAULT '希望理解和应用知识的个人读者',
 style TEXT NOT NULL DEFAULT '清楚、具体、有依据；解释概念并连接材料，避免空泛总结',
 updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);
INSERT INTO knowledge_article_settings(id) VALUES(1);
CREATE TABLE knowledge_articles (
 id TEXT PRIMARY KEY,
 profile_id TEXT NOT NULL REFERENCES editorial_profiles(id),
 automated INTEGER NOT NULL DEFAULT 0,
 input_hash TEXT NOT NULL,
 input_json TEXT NOT NULL,
 status TEXT NOT NULL DEFAULT 'discover',
 stage TEXT NOT NULL DEFAULT 'discover',
 title TEXT NOT NULL DEFAULT '',
 thesis TEXT NOT NULL DEFAULT '',
 topics_json TEXT NOT NULL DEFAULT '[]',
 topic_json TEXT NOT NULL DEFAULT '{}',
 blocks_json TEXT NOT NULL DEFAULT '[]',
 issues_json TEXT NOT NULL DEFAULT '[]',
 reason TEXT NOT NULL DEFAULT '',
 provider TEXT NOT NULL,
 model TEXT NOT NULL,
 created_at TEXT NOT NULL DEFAULT (datetime('now')),
 updated_at TEXT NOT NULL DEFAULT (datetime('now')),
 UNIQUE(profile_id,input_hash)
);
CREATE UNIQUE INDEX idx_knowledge_articles_active ON knowledge_articles(profile_id)
 WHERE status IN ('discover','write','review','revise','review_final');
