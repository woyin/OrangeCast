package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/woyin/orangecast/internal/provider"
	"strings"
)

// understandingSendSQL requires every Reference to be live and allowed; source-less Owner ideas still require their own policy.
func understandingSendSQL(alias, param string, embedding bool) string {
	source := func(a string) string {
		if embedding {
			return embeddingSourceQualified(a)
		}
		var parts []string
		for _, v := range []struct{ k, t string }{{"episode", "episodes"}, {"upload", "uploads"}, {"document", "documents"}} {
			parts = append(parts, `(`+a+`.source_type='`+v.k+`' AND EXISTS(SELECT 1 FROM `+v.t+` us WHERE us.id=`+a+`.source_id AND us.archived_at IS NULL AND NOT EXISTS(SELECT 1 FROM source_snapshots p WHERE p.source_type='`+v.k+`' AND p.source_id=us.id AND p.status='purged') AND (us.model_data_policy='external_allowed' OR (us.model_data_policy='approved_providers_only' AND EXISTS(SELECT 1 FROM json_each(CASE WHEN json_valid(us.approved_providers_json) THEN us.approved_providers_json ELSE '[]' END) ap WHERE lower(trim(ap.value))=lower(trim(`+param+`)))))))`)
		}
		return "(" + strings.Join(parts, " OR ") + ")"
	}
	selected := ""
	if embedding {
		selected = ` AND EXISTS(SELECT 1 FROM knowledge_embedding_understandings ue JOIN understanding_heads uh ON uh.current_snapshot_id=ue.snapshot_id WHERE ue.config_id=:config AND ue.snapshot_id=us.id)`
	}
	closure := `WITH RECURSIVE chain(id) AS (SELECT us.id UNION SELECT am.material_id FROM chain c JOIN understanding_references rr ON rr.snapshot_id=c.id AND rr.kind='article' JOIN knowledge_article_material_refs am ON am.article_id=rr.object_id AND am.revision=rr.version AND am.kind='understanding')`
	badPolicy := `NOT EXISTS(SELECT 1 FROM understanding_snapshots nested WHERE nested.id=c.id AND (nested.model_data_policy='external_allowed' OR (nested.model_data_policy='approved_providers_only' AND EXISTS(SELECT 1 FROM json_each(nested.approved_providers_json) ap WHERE lower(trim(ap.value))=lower(trim(` + param + `))))))`
	badRefs := `EXISTS(SELECT 1 FROM understanding_references ur WHERE ur.snapshot_id=c.id AND (ur.purged=1 OR (ur.kind='keypoint' AND NOT EXISTS(SELECT 1 FROM keypoint_index kp WHERE kp.id=ur.object_id AND kp.card_version=ur.version AND kp.content=ur.body AND kp.stale_at IS NULL AND kp.evidence_status!='stale' AND kp.production_status!='dismissed' AND kp.quality_status IN('ready','owner_confirmed'))) OR (ur.kind!='article' AND NOT ` + source("ur") + `) OR (ur.kind='article' AND (NOT EXISTS(SELECT 1 FROM knowledge_article_revisions ar WHERE ar.article_id=ur.object_id AND ar.revision=ur.version AND ar.evidence_status='valid') OR NOT EXISTS(SELECT 1 FROM knowledge_article_material_refs am WHERE am.article_id=ur.object_id AND am.revision=ur.version) OR EXISTS(SELECT 1 FROM knowledge_article_material_refs am WHERE am.article_id=ur.object_id AND am.revision=ur.version AND am.kind!='understanding' AND NOT ` + source("am") + `)))))`
	return `(` + alias + `.kind='understanding' AND EXISTS(SELECT 1 FROM understanding_snapshots us WHERE us.id=` + alias + `.object_id AND us.version=` + alias + `.revision` + selected + ` AND NOT EXISTS(` + closure + ` SELECT 1 FROM chain c WHERE ` + badPolicy + ` OR ` + badRefs + `)))`

}

// checkUnderstandingMaterialTransaction validates frozen identity and all policies on the accepting transaction's connection.
func checkUnderstandingMaterialTransaction(ctx context.Context, tx *sql.Tx, name string, m provider.KnowledgeMaterial) error {
	return checkUnderstandingMaterialReader(ctx, tx, name, m)
}
func checkUnderstandingMaterialReader(ctx context.Context, tx reviewReader, name string, m provider.KnowledgeMaterial) error {
	var allowed bool
	if e := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM knowledge_search_docs d WHERE d.object_id=? AND d.revision=? AND `+understandingSendSQL("d", ":understanding_provider", false)+`)`, m.ID, m.Version, sql.Named("understanding_provider", name)).Scan(&allowed); e != nil {
		return e
	}
	if !allowed {
		return ErrConflict
	}
	v, e := scanUnderstanding(tx.QueryRowContext(ctx, `SELECT `+understandingColumns+` FROM understanding_snapshots WHERE id=? AND version=?`, m.ID, m.Version))
	if e != nil {
		return e
	}
	want := provider.KnowledgeMaterial{ID: v.ID, Kind: "understanding", Version: v.Version, SourceTitle: "Owner个人理解（Reference不是Citation）", Content: v.Answer, Description: "仍不确定：" + v.Uncertainty + "\n下一步：" + v.NextStep, NoPosition: true, Citations: []string{}}
	var rawRefs string
	e = tx.QueryRowContext(ctx, `SELECT COALESCE(json_group_array(json_object('kind',kind,'object_id',object_id,'version',version,'source_type',source_type,'source_id',source_id,'body',body,'purged',json(CASE WHEN purged=1 THEN 'true' ELSE 'false' END))),'[]') FROM (SELECT * FROM understanding_references WHERE snapshot_id=? ORDER BY ordinal)`, m.ID).Scan(&rawRefs)
	if e != nil {
		return e
	}
	var refs []provider.KnowledgeUnderstandingReference
	if json.Unmarshal([]byte(rawRefs), &refs) != nil {
		return ErrConflict
	}
	if len(refs) > 0 {
		want.UnderstandingReferences = refs
	}
	m.RetrievalReason = ""
	m.PreviousContent = ""
	a, _ := json.Marshal(want)
	b, _ := json.Marshal(m)
	if string(a) != string(b) {
		return ErrConflict
	}
	return nil
}

// setEmbeddingUnderstandings freezes exact, explicitly selected current snapshots in the configuration transaction.
func setEmbeddingUnderstandings(ctx context.Context, tx *sql.Tx, configID string, ids []string) error {
	if len(ids) > 500 {
		return ErrInvalidEditorialState
	}
	if _, e := tx.ExecContext(ctx, `DELETE FROM knowledge_embedding_understandings WHERE config_id=?`, configID); e != nil {
		return e
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if id == "" || seen[id] {
			return ErrInvalidEditorialState
		}
		seen[id] = true
		var exists bool
		if e := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM understanding_snapshots us JOIN understanding_heads h ON h.current_snapshot_id=us.id WHERE us.id=?)`, id).Scan(&exists); e != nil {
			return e
		}
		if !exists {
			return ErrConflict
		}
		if _, e := tx.ExecContext(ctx, `INSERT INTO knowledge_embedding_understandings(config_id,snapshot_id)VALUES(?,?)`, configID, id); e != nil {
			return e
		}
	}
	return nil
}
func understandingSourceRowsSQL() string {
	return `WITH RECURSIVE chain(id) AS (SELECT ? UNION SELECT am.material_id FROM chain c JOIN understanding_references rr ON rr.snapshot_id=c.id AND rr.kind='article' JOIN knowledge_article_material_refs am ON am.article_id=rr.object_id AND am.revision=rr.version AND am.kind='understanding') SELECT DISTINCT r.source_type,r.source_id FROM chain c JOIN understanding_references r ON r.snapshot_id=c.id WHERE r.kind!='article' AND r.source_id!='' UNION SELECT DISTINCT m.source_type,m.source_id FROM chain c JOIN understanding_references r ON r.snapshot_id=c.id JOIN knowledge_article_material_refs m ON m.article_id=r.object_id AND m.revision=r.version WHERE r.kind='article' AND m.kind!='understanding' AND m.source_id!='' AND ? IS NOT NULL ORDER BY source_type,source_id`
}
