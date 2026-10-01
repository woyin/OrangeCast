package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
)

const KnowledgeEmbeddingCapacity = 50000

// Source choices are Owner-confirmed, not inferred from a successful model call.
type EmbeddingSource struct {
	SourceType string `json:"source_type"`
	SourceID   string `json:"source_id"`
}
type KnowledgeEmbeddingConfig struct {
	provider.EmbeddingConfig
	Enabled  bool `json:"enabled"`
	Revision int  `json:"revision"`
}
type EmbeddingWindow struct {
	DocKey        string            `json:"doc_key"`
	WindowNo      int               `json:"window_no"`
	Revision      int               `json:"revision"`
	ContentHash   string            `json:"content_hash"`
	Input         string            `json:"input"`
	ScopeRevision int               `json:"scope_revision"`
	Sources       []EmbeddingSource `json:"sources"`
}

type EmbeddingIndexStatus struct {
	Config                                         *KnowledgeEmbeddingConfig
	Sources                                        []EmbeddingSource
	IndexedWindows, PendingObjects, SkippedObjects int
	DeleteEpoch                                    int64
	CapacityBlocked                                bool
}

// RegisterKnowledgeEmbeddingConfig is called after an explicit successful
// preflight. The registered dimension is immutable, and indexing stays disabled.
func (s *Store) RegisterKnowledgeEmbeddingConfig(ctx context.Context, cfg provider.EmbeddingConfig) error {
	for _, id := range []string{cfg.ID, cfg.ConnectionID} {
		raw, err := hex.DecodeString(id)
		if err != nil || len(raw) != 32 {
			return ErrInvalidEditorialState
		}
	}
	expected := fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%d", cfg.ConnectionID, cfg.Model, cfg.Dimensions))))
	if cfg.ID != expected || cfg.Provider != "embedding-"+cfg.ConnectionID[:16] || cfg.Dimensions < 1 || cfg.Dimensions > provider.EmbeddingMaxDimensions || cfg.Unit != "input_tokens" || strings.TrimSpace(cfg.Model) == "" || len(cfg.Model) > 200 {
		return ErrInvalidEditorialState
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO knowledge_embedding_configs(id,connection_id,provider,model,dimensions,unit)VALUES(?,?,?,?,?,?) ON CONFLICT(id)DO NOTHING`, cfg.ID, cfg.ConnectionID, cfg.Provider, cfg.Model, cfg.Dimensions, cfg.Unit)
	return err
}

func (s *Store) GetKnowledgeEmbeddingConfig(ctx context.Context, id string) (*KnowledgeEmbeddingConfig, error) {
	c := &KnowledgeEmbeddingConfig{}
	err := s.DB.QueryRowContext(ctx, `SELECT id,connection_id,provider,model,dimensions,unit,enabled,revision FROM knowledge_embedding_configs WHERE id=?`, id).Scan(&c.ID, &c.ConnectionID, &c.Provider, &c.Model, &c.Dimensions, &c.Unit, &c.Enabled, &c.Revision)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return c, err
}

// ChangeKnowledgeEmbeddingScope requires a measured config and explicit choices.
// Every change clears its vectors and advances the cache epoch in the same tx.
func (s *Store) ChangeKnowledgeEmbeddingScope(ctx context.Context, id string, expected int, enabled bool, sources []EmbeddingSource) (*KnowledgeEmbeddingConfig, error) {
	if expected < 1 || len(sources) > 1000 {
		return nil, ErrInvalidEditorialState
	}
	seen := map[EmbeddingSource]bool{}
	for _, source := range sources {
		if !validSourceType(models.SourceType(source.SourceType)) || source.SourceID == "" || len(source.SourceID) > 200 || seen[source] {
			return nil, ErrInvalidEditorialState
		}
		seen[source] = true
	}
	cfg, err := s.GetKnowledgeEmbeddingConfig(ctx, id)
	if err != nil {
		return nil, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE knowledge_embedding_configs SET enabled=?,revision=revision+1,updated_at=datetime('now') WHERE id=? AND revision=?`, enabled, id, expected)
	if err != nil {
		return nil, err
	}
	count, _ := result.RowsAffected()
	if count != 1 {
		return nil, ErrConflict
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM knowledge_embedding_sources WHERE config_id=?`, id); err != nil {
		return nil, err
	}
	for _, source := range sources {
		var exists int
		if err = tx.QueryRowContext(ctx, fmt.Sprintf(`SELECT COUNT(*) FROM %s WHERE id=? AND archived_at IS NULL`, sourceTable(models.SourceType(source.SourceType))), source.SourceID).Scan(&exists); err != nil {
			return nil, err
		}
		if exists != 1 {
			return nil, ErrNotFound
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO knowledge_embedding_sources(config_id,source_type,source_id)VALUES(?,?,?)`, id, source.SourceType, source.SourceID); err != nil {
			return nil, err
		}
	}
	for _, query := range []string{`DELETE FROM knowledge_embedding_vectors WHERE config_id=?`, `DELETE FROM knowledge_embedding_events WHERE config_id=?`, `INSERT INTO knowledge_embedding_events(config_id,doc_key,action) SELECT ?,key,'upsert' FROM knowledge_search_docs WHERE visibility='current'`, `UPDATE knowledge_embedding_state SET delete_epoch=delete_epoch+1,index_epoch=index_epoch+1 WHERE id=1`} {
		if strings.Contains(query, "?") {
			_, err = tx.ExecContext(ctx, query, id)
		} else {
			_, err = tx.ExecContext(ctx, query)
		}
		if err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	cfg.Enabled = enabled
	cfg.Revision = expected + 1
	return cfg, nil
}

// Qualified SQL is shared by prepare/adopt/cache reads, so a cached vector cannot
// outlive a Source policy or a passed-article's complete source provenance.
func embeddingSourceQualified(alias string) string {
	var alternatives []string
	for _, source := range []struct{ kind, table string }{{"episode", "episodes"}, {"upload", "uploads"}, {"document", "documents"}} {
		alternatives = append(alternatives, `(`+alias+`.source_type='`+source.kind+`' AND EXISTS(SELECT 1 FROM `+source.table+` es JOIN knowledge_embedding_sources chosen ON chosen.config_id=:config AND chosen.source_type='`+source.kind+`' AND chosen.source_id=es.id WHERE es.id=`+alias+`.source_id AND es.archived_at IS NULL AND NOT EXISTS(SELECT 1 FROM source_snapshots purged WHERE purged.source_type='`+source.kind+`' AND purged.source_id=es.id AND purged.status='purged') AND (es.model_data_policy='external_allowed' OR (es.model_data_policy='approved_providers_only' AND EXISTS(SELECT 1 FROM json_each(CASE WHEN json_valid(es.approved_providers_json) THEN es.approved_providers_json ELSE '[]' END) ap WHERE lower(trim(ap.value))=lower(trim(:provider)))))))`)
	}
	return "(" + strings.Join(alternatives, " OR ") + ")"
}
func embeddingDocumentQualified() string {
	return `d.visibility='current' AND d.kind IN ('original','document','keypoint','source_note','owner_reflection','article') AND (d.kind!='keypoint' OR EXISTS(SELECT 1 FROM keypoint_index k WHERE k.id=d.object_id AND k.stale_at IS NULL AND k.evidence_status!='stale' AND k.production_status!='dismissed' AND k.quality_status IN ('ready','owner_confirmed'))) AND (` + embeddingSourceQualified("d") + ` OR (d.kind='article' AND EXISTS(SELECT 1 FROM knowledge_article_material_refs m WHERE m.article_id=d.object_id AND m.revision=d.revision) AND NOT EXISTS(SELECT 1 FROM knowledge_article_material_refs m WHERE m.article_id=d.object_id AND m.revision=d.revision AND NOT ` + embeddingSourceQualified("m") + `)))`
}
func embeddingSQLArgs(cfg *KnowledgeEmbeddingConfig) []any {
	return []any{sql.Named("config", cfg.ID), sql.Named("provider", cfg.Provider)}
}

func embeddingWindows(key string, revision int, title, body string, scopeRevision int) ([]EmbeddingWindow, error) {
	// Never slice an Owner expression or a source paragraph to fit a model cap.
	// All chunks retain the underlying stable document/segment identity.
	prefix := strings.TrimSpace(title) + "\n\n"
	if len(prefix) >= provider.EmbeddingMaxInputBytes {
		return nil, ErrInvalidEditorialState
	}
	var chunks []string
	current := ""
	for _, paragraph := range strings.Split(body, "\n\n") {
		if strings.TrimSpace(paragraph) == "" {
			continue
		}
		if len(prefix)+len(paragraph) > provider.EmbeddingMaxInputBytes {
			return nil, ErrInvalidEditorialState
		}
		if current != "" && len(prefix)+len(current)+2+len(paragraph) > provider.EmbeddingMaxInputBytes {
			chunks = append(chunks, prefix+current)
			current = ""
		}
		if current != "" {
			current += "\n\n"
		}
		current += paragraph
	}
	if current != "" {
		chunks = append(chunks, prefix+current)
	}
	if len(chunks) == 0 {
		return nil, ErrInvalidEditorialState
	}
	out := make([]EmbeddingWindow, len(chunks))
	for i, input := range chunks {
		hash := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d\x00%d\x00%s", key, revision, i, input)))
		out[i] = EmbeddingWindow{DocKey: key, WindowNo: i, Revision: revision, ContentHash: fmt.Sprintf("%x", hash), Input: input, ScopeRevision: scopeRevision}
	}
	return out, nil
}

type embeddingDocument struct {
	key, title, body, kind, objectID, sourceType, sourceID string
	revision                                               int
}

func (s *Store) embeddingWindowSources(ctx context.Context, doc embeddingDocument) ([]EmbeddingSource, error) {
	if doc.kind != "article" {
		return []EmbeddingSource{{doc.sourceType, doc.sourceID}}, nil
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT DISTINCT source_type,source_id FROM knowledge_article_material_refs WHERE article_id=? AND revision=? ORDER BY source_type,source_id`, doc.objectID, doc.revision)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EmbeddingSource
	for rows.Next() {
		var v EmbeddingSource
		if err = rows.Scan(&v.SourceType, &v.SourceID); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// PrepareKnowledgeEmbeddingBatch returns at most 16 complete windows. It neither
// enqueues jobs nor calls a model, even if there are incremental events.
func (s *Store) PrepareKnowledgeEmbeddingBatch(ctx context.Context, id string) ([]EmbeddingWindow, error) {
	cfg, err := s.GetKnowledgeEmbeddingConfig(ctx, id)
	if err != nil {
		return nil, err
	}
	if !cfg.Enabled {
		return nil, ErrInvalidEditorialState
	}
	var count int
	if err = s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM knowledge_embedding_vectors WHERE config_id=?`, id).Scan(&count); err != nil {
		return nil, err
	}
	if count >= KnowledgeEmbeddingCapacity {
		return nil, fmt.Errorf("%w: embedding capacity reached", ErrInvalidEditorialState)
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT d.key,d.revision,d.title,d.body,d.kind,d.object_id,d.source_type,d.source_id FROM knowledge_embedding_events ev JOIN knowledge_search_docs d ON d.key=ev.doc_key WHERE ev.config_id=:config AND ev.action='upsert' AND ev.reason!='window_too_large' AND length(CAST(d.body AS BLOB))<=1048576 AND `+embeddingDocumentQualified()+` ORDER BY ev.created_at,ev.doc_key LIMIT 16`, embeddingSQLArgs(cfg)...)
	if err != nil {
		return nil, err
	}
	var docs []embeddingDocument
	for rows.Next() {
		var d embeddingDocument
		if err = rows.Scan(&d.key, &d.revision, &d.title, &d.body, &d.kind, &d.objectID, &d.sourceType, &d.sourceID); err != nil {
			rows.Close()
			return nil, err
		}
		docs = append(docs, d)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	var out []EmbeddingWindow
	for _, doc := range docs {
		windows, err := embeddingWindows(doc.key, doc.revision, doc.title, doc.body, cfg.Revision)
		if err != nil {
			if _, err = s.DB.ExecContext(ctx, `UPDATE knowledge_embedding_events SET reason='window_too_large' WHERE config_id=? AND doc_key=?`, id, doc.key); err != nil {
				return nil, err
			}
			continue
		}
		refs, err := s.embeddingWindowSources(ctx, doc)
		if err != nil {
			return nil, err
		}
		for _, window := range windows {
			var exists int
			if err = s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM knowledge_embedding_vectors WHERE config_id=? AND doc_key=? AND window_no=? AND revision=? AND content_hash=? AND dimensions=?`, id, doc.key, window.WindowNo, window.Revision, window.ContentHash, cfg.Dimensions).Scan(&exists); err != nil {
				return nil, err
			}
			if exists == 1 {
				continue
			}
			window.Sources = refs
			out = append(out, window)
			if len(out) >= provider.EmbeddingMaxBatch || count+len(out) >= KnowledgeEmbeddingCapacity {
				return out, nil
			}
		}
	}
	return out, nil
}

// AdoptKnowledgeEmbeddings atomically admits only still-current complete windows.
// A changed config/scope returns a conflict. Source withdrawal discards that result.
func (s *Store) AdoptKnowledgeEmbeddings(ctx context.Context, id string, windows []EmbeddingWindow, result *provider.EmbeddingResult) (int, error) {
	cfg, err := s.GetKnowledgeEmbeddingConfig(ctx, id)
	if err != nil {
		return 0, err
	}
	if len(windows) < 1 || len(windows) > provider.EmbeddingMaxBatch || result == nil || len(result.Vectors) != len(windows) || result.Model != cfg.Model || result.Dimensions != cfg.Dimensions {
		return 0, ErrInvalidEditorialState
	}
	vectors := make([][]byte, len(windows))
	seen := map[string]bool{}
	for i, vec := range result.Vectors {
		key := fmt.Sprintf("%s:%d", windows[i].DocKey, windows[i].WindowNo)
		if len(vec) != cfg.Dimensions || seen[key] {
			return 0, ErrInvalidEditorialState
		}
		seen[key] = true
		var norm float64
		vectors[i] = make([]byte, 4*len(vec))
		for j, v := range vec {
			if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
				return 0, ErrInvalidEditorialState
			}
			norm += float64(v) * float64(v)
			binary.LittleEndian.PutUint32(vectors[i][j*4:], math.Float32bits(v))
		}
		if math.Abs(norm-1) > .0001 {
			return 0, ErrInvalidEditorialState
		}
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var enabled bool
	var revision int
	if err = tx.QueryRowContext(ctx, `SELECT enabled,revision FROM knowledge_embedding_configs WHERE id=?`, id).Scan(&enabled, &revision); err != nil {
		return 0, err
	}
	if !enabled || revision != cfg.Revision {
		return 0, ErrConflict
	}
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM knowledge_embedding_vectors WHERE config_id=?`, id).Scan(&count); err != nil {
		return 0, err
	}
	missing := 0
	for _, window := range windows {
		var exists int
		if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM knowledge_embedding_vectors WHERE config_id=? AND doc_key=? AND window_no=?`, id, window.DocKey, window.WindowNo).Scan(&exists); err != nil {
			return 0, err
		}
		if exists == 0 {
			missing++
		}
	}
	if count+missing > KnowledgeEmbeddingCapacity {
		return 0, ErrInvalidEditorialState
	}
	adopted := 0
	for i, window := range windows {
		if window.ScopeRevision != revision {
			return 0, ErrConflict
		}
		args := embeddingSQLArgs(cfg)
		args = append(args, sql.Named("key", window.DocKey))
		var doc embeddingDocument
		err = tx.QueryRowContext(ctx, `SELECT d.key,d.revision,d.title,d.body FROM knowledge_search_docs d WHERE d.key=:key AND `+embeddingDocumentQualified(), args...).Scan(&doc.key, &doc.revision, &doc.title, &doc.body)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return 0, err
		}
		current, err := embeddingWindows(doc.key, doc.revision, doc.title, doc.body, revision)
		if err != nil || window.WindowNo < 0 || window.WindowNo >= len(current) || current[window.WindowNo].ContentHash != window.ContentHash || current[window.WindowNo].Input != window.Input || window.Revision != doc.revision {
			continue
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO knowledge_embedding_vectors(config_id,doc_key,window_no,revision,content_hash,dimensions,vector)VALUES(?,?,?,?,?,?,?) ON CONFLICT(config_id,doc_key,window_no) DO UPDATE SET revision=excluded.revision,content_hash=excluded.content_hash,dimensions=excluded.dimensions,vector=excluded.vector,created_at=datetime('now')`, id, window.DocKey, window.WindowNo, window.Revision, window.ContentHash, cfg.Dimensions, vectors[i])
		if err != nil {
			return 0, err
		}
		adopted++
		var stored int
		if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM knowledge_embedding_vectors WHERE config_id=? AND doc_key=? AND revision=? AND dimensions=?`, id, window.DocKey, doc.revision, cfg.Dimensions).Scan(&stored); err != nil {
			return 0, err
		}
		if stored == len(current) {
			if _, err = tx.ExecContext(ctx, `DELETE FROM knowledge_embedding_events WHERE config_id=? AND doc_key=?`, id, window.DocKey); err != nil {
				return 0, err
			}
		}
	}
	if adopted > 0 {
		if _, err = tx.ExecContext(ctx, `UPDATE knowledge_embedding_state SET index_epoch=index_epoch+1 WHERE id=1`); err != nil {
			return 0, err
		}
	}
	if err = tx.Commit(); err != nil {
		return 0, err
	}
	return adopted, nil
}

func (s *Store) KnowledgeEmbeddingStatus(ctx context.Context, id string) (*EmbeddingIndexStatus, error) {
	cfg, err := s.GetKnowledgeEmbeddingConfig(ctx, id)
	if err != nil {
		return nil, err
	}
	status := &EmbeddingIndexStatus{Config: cfg}
	rows, err := s.DB.QueryContext(ctx, `SELECT source_type,source_id FROM knowledge_embedding_sources WHERE config_id=? ORDER BY source_type,source_id`, id)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var ref EmbeddingSource
		if err = rows.Scan(&ref.SourceType, &ref.SourceID); err != nil {
			rows.Close()
			return nil, err
		}
		status.Sources = append(status.Sources, ref)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if err = s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM knowledge_embedding_vectors v JOIN knowledge_search_docs d ON d.key=v.doc_key WHERE v.config_id=:config AND v.revision=d.revision AND `+embeddingDocumentQualified(), embeddingSQLArgs(cfg)...).Scan(&status.IndexedWindows); err != nil {
		return nil, err
	}
	if err = s.DB.QueryRowContext(ctx, `SELECT COALESCE(SUM(ev.reason!='window_too_large'),0),COALESCE(SUM(ev.reason='window_too_large'),0) FROM knowledge_embedding_events ev JOIN knowledge_search_docs d ON d.key=ev.doc_key WHERE ev.config_id=:config AND ev.action='upsert' AND `+embeddingDocumentQualified(), embeddingSQLArgs(cfg)...).Scan(&status.PendingObjects, &status.SkippedObjects); err != nil {
		return nil, err
	}
	if err = s.DB.QueryRowContext(ctx, `SELECT delete_epoch FROM knowledge_embedding_state WHERE id=1`).Scan(&status.DeleteEpoch); err != nil {
		return nil, err
	}
	status.CapacityBlocked = status.IndexedWindows >= KnowledgeEmbeddingCapacity
	return status, nil
}
