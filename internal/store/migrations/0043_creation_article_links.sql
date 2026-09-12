-- C08（ADR-0024 §6）：新创作契约与旧文章存储的持久一对一映射。
-- creation_proposals ↔ article_proposals、creation_briefs ↔ article_briefs 各一行。
-- 旧 Article 记录继续可读可导出；新 Brief 确认后幂等建立映射。
-- contract_version 记录新契约版本号，为后续 C09–C11 契约升级留血缘。

CREATE TABLE creation_article_links (
    id                       TEXT PRIMARY KEY,
    creation_proposal_id     TEXT NOT NULL UNIQUE,
    creation_brief_id        TEXT NOT NULL DEFAULT '',
    article_proposal_id      TEXT NOT NULL UNIQUE,
    article_brief_id         TEXT NOT NULL DEFAULT '',
    contract_version         TEXT NOT NULL DEFAULT 'v2',
    created_at               TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_creation_links_article ON creation_article_links(article_proposal_id);
