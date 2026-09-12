-- G08：创作历史关联确切精读修订。
-- digest_id 唯一（同修订重复登记幂等）；digest_content 复制登记时正文，
-- 来源删除后仍保留历史身份（依据状态由快照审计，不私存原文）。

ALTER TABLE creation_history ADD COLUMN digest_id TEXT NOT NULL DEFAULT '';
ALTER TABLE creation_history ADD COLUMN digest_content TEXT NOT NULL DEFAULT '';
CREATE UNIQUE INDEX IF NOT EXISTS idx_creation_history_digest ON creation_history(digest_id) WHERE digest_id != '';
