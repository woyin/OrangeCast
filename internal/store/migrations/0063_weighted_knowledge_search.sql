-- Keep the original projection/token column for old callers. New FTS columns
-- represent real title/question/body text, so weights have distinct semantics.
DROP TRIGGER knowledge_search_insert;
DROP TRIGGER knowledge_search_delete;
DROP TRIGGER knowledge_search_update;
DROP TABLE knowledge_search_fts;
ALTER TABLE knowledge_search_docs ADD COLUMN title_tokens TEXT NOT NULL DEFAULT '';
ALTER TABLE knowledge_search_docs ADD COLUMN question TEXT NOT NULL DEFAULT '';
ALTER TABLE knowledge_search_docs ADD COLUMN question_tokens TEXT NOT NULL DEFAULT '';
ALTER TABLE knowledge_search_docs ADD COLUMN body_tokens TEXT NOT NULL DEFAULT '';
UPDATE knowledge_search_docs SET question=COALESCE((SELECT CASE WHEN json_valid(a.topic_json) THEN json_extract(a.topic_json,'$.question') ELSE '' END FROM knowledge_articles a WHERE a.id=knowledge_search_docs.object_id AND knowledge_search_docs.kind='article'),'');
UPDATE knowledge_search_docs SET title_tokens=cwp_search_tokens(title),body_tokens=cwp_search_tokens(body),
 question_tokens=cwp_search_tokens(COALESCE((SELECT CASE WHEN json_valid(a.topic_json) THEN json_extract(a.topic_json,'$.question') ELSE '' END FROM knowledge_articles a WHERE a.id=knowledge_search_docs.object_id AND knowledge_search_docs.kind='article'),''));
CREATE VIRTUAL TABLE knowledge_search_fts USING fts5(title_tokens,question_tokens,body_tokens,content='knowledge_search_docs',content_rowid='rowid',tokenize='unicode61');
INSERT INTO knowledge_search_fts(knowledge_search_fts)VALUES('rebuild');
CREATE TRIGGER knowledge_search_insert AFTER INSERT ON knowledge_search_docs BEGIN
 UPDATE knowledge_search_docs SET title_tokens=cwp_search_tokens(new.title),body_tokens=cwp_search_tokens(new.body),
 question=COALESCE((SELECT CASE WHEN json_valid(a.topic_json) THEN json_extract(a.topic_json,'$.question') ELSE '' END FROM knowledge_articles a WHERE a.id=new.object_id AND new.kind='article'),new.question),
 question_tokens=cwp_search_tokens(COALESCE((SELECT CASE WHEN json_valid(a.topic_json) THEN json_extract(a.topic_json,'$.question') ELSE '' END FROM knowledge_articles a WHERE a.id=new.object_id AND new.kind='article'),'')) WHERE rowid=new.rowid;
 INSERT INTO knowledge_search_fts(rowid,title_tokens,question_tokens,body_tokens)SELECT rowid,title_tokens,question_tokens,body_tokens FROM knowledge_search_docs WHERE rowid=new.rowid;
END;
CREATE TRIGGER knowledge_search_delete AFTER DELETE ON knowledge_search_docs BEGIN
 INSERT INTO knowledge_search_fts(knowledge_search_fts,rowid,title_tokens,question_tokens,body_tokens)VALUES('delete',old.rowid,old.title_tokens,old.question_tokens,old.body_tokens);
END;
CREATE TRIGGER knowledge_search_update AFTER UPDATE OF title,body,kind,object_id ON knowledge_search_docs BEGIN
 INSERT INTO knowledge_search_fts(knowledge_search_fts,rowid,title_tokens,question_tokens,body_tokens)VALUES('delete',old.rowid,old.title_tokens,old.question_tokens,old.body_tokens);
 UPDATE knowledge_search_docs SET title_tokens=cwp_search_tokens(new.title),body_tokens=cwp_search_tokens(new.body),
 question=COALESCE((SELECT CASE WHEN json_valid(a.topic_json) THEN json_extract(a.topic_json,'$.question') ELSE '' END FROM knowledge_articles a WHERE a.id=new.object_id AND new.kind='article'),new.question),
 question_tokens=cwp_search_tokens(COALESCE((SELECT CASE WHEN json_valid(a.topic_json) THEN json_extract(a.topic_json,'$.question') ELSE '' END FROM knowledge_articles a WHERE a.id=new.object_id AND new.kind='article'),'')) WHERE rowid=new.rowid;
 INSERT INTO knowledge_search_fts(rowid,title_tokens,question_tokens,body_tokens)SELECT rowid,title_tokens,question_tokens,body_tokens FROM knowledge_search_docs WHERE rowid=new.rowid;
END;
