-- Document updates, withdrawals and FK cascades must locate every configuration's
-- cached windows without scanning unrelated vectors. This index grants no scope.
CREATE INDEX idx_embedding_vectors_document ON knowledge_embedding_vectors(doc_key);
