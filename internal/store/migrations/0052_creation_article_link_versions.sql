-- R18：link 绑定精确 CreationBrief revision；旧 contract_version 仍表示契约代际。
ALTER TABLE creation_article_links ADD COLUMN creation_brief_version INTEGER NOT NULL DEFAULT 1;
UPDATE creation_article_links
SET creation_brief_version=COALESCE((SELECT CASE WHEN confirmed_version>0 THEN confirmed_version WHEN current_version>0 THEN current_version ELSE 1 END FROM creation_briefs WHERE creation_briefs.id=creation_article_links.creation_brief_id),1)
WHERE creation_brief_version=1 OR creation_brief_version=0;
