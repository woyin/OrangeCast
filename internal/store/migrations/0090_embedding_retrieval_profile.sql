-- Empty profile preserves existing identities; task-specific spaces are distinct.
ALTER TABLE knowledge_embedding_configs ADD COLUMN profile TEXT NOT NULL DEFAULT '' CHECK(profile IN ('','jina-retrieval-v1'));
