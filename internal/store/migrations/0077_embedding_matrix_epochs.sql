-- Cache epochs follow all vector mutations, including repaired/restored rows.
CREATE TRIGGER embedding_vector_insert AFTER INSERT ON knowledge_embedding_vectors BEGIN
 UPDATE knowledge_embedding_state SET index_epoch=index_epoch+1 WHERE id=1;
END;
CREATE TRIGGER embedding_vector_update AFTER UPDATE ON knowledge_embedding_vectors BEGIN
 UPDATE knowledge_embedding_state SET index_epoch=index_epoch+1 WHERE id=1;
END;
CREATE TRIGGER embedding_vector_delete AFTER DELETE ON knowledge_embedding_vectors BEGIN
 UPDATE knowledge_embedding_state SET index_epoch=index_epoch+1 WHERE id=1;
END;
