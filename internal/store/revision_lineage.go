// revision_lineage.go 手工/AI 修订的主张血缘与并发保存（R21 / C12 / ADR-0024 §6）。
//
// 手工修订真实保存路径（SaveOwnerRevisionWithClaimLineage）在单事务中完成：
//   - CAS：draft.current_revision_id 仍等于 base revision，过期编辑返回 ErrConflict，
//     不产生孤儿 revision；
//   - 唯一定位继承：ClaimMap（及旧 EvidenceMap 兼容投影）的 excerpt 必须非空且在
//     旧正文与新正文中各恰好出现 1 次才继承——重复句歧义、修改、删除/挪段均不继承；
//   - 不可变 revision 创建 + 继承映射 + current 指针更新原子；
//   - 旧 ClaimReview/StyleReview/EvidenceReview 一律不复制：新修订按 definition
//     无任何审校行，readiness 重新回到 reviewing/blocked，旧修订历史保持可查。
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/models"
)

// countExactOccurrences 统计 excerpt 在正文中恰好出现的次数（非重叠）。
func countExactOccurrences(markdown, excerpt string) int {
	if excerpt == "" {
		return 0
	}
	return strings.Count(markdown, excerpt)
}

// uniquelyLocated 判定一个片段是否可在两份正文中都唯一定位（继承的必要条件）。
func uniquelyLocated(oldMarkdown, newMarkdown, excerpt string) bool {
	if strings.TrimSpace(excerpt) == "" {
		return false
	}
	return countExactOccurrences(oldMarkdown, excerpt) == 1 &&
		countExactOccurrences(newMarkdown, excerpt) == 1
}

// SaveOwnerRevisionWithClaimLineage 手工修订真实保存路径（R21）。
// baseRevisionID 是页面表单携带的精确 base/current revision；尚无任何修订的新草稿
// 允许 baseRevisionID 为空（CAS 校验 current 仍为空，不继承任何映射）。
// 单事务内 CAS 校验、唯一定位继承（ClaimMap + EvidenceMap 兼容）、不可变 revision
// 创建与 current 指针更新原子完成。冲突返回 ErrConflict；任何失败不产生孤儿 revision。
func (s *Store) SaveOwnerRevisionWithClaimLineage(ctx context.Context, draftID, baseRevisionID, title, markdown string) (*models.ArticleRevision, error) {
	if strings.TrimSpace(draftID) == "" || strings.TrimSpace(markdown) == "" {
		return nil, fmt.Errorf("%w: draft 与正文必填", ErrInvalidEditorialState)
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	// 1) CAS：draft 的 current 仍等于页面携带的 base（新草稿 base 为空）。
	var currentID string
	err = tx.QueryRowContext(ctx, `SELECT COALESCE(current_revision_id,'') FROM article_drafts WHERE id=?`, draftID).Scan(&currentID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if currentID != baseRevisionID {
		return nil, fmt.Errorf("%w: 页面已过期：文章已有更新的修订（current=%s base=%s）", ErrConflict, currentID, baseRevisionID)
	}

	// base revision 存在性（current 非空时必须可读，并作为继承来源）。
	base := &models.ArticleRevision{}
	if baseRevisionID != "" {
		err = tx.QueryRowContext(ctx,
			`SELECT id, draft_id, title, markdown FROM article_revisions WHERE id=?`, baseRevisionID).
			Scan(&base.ID, &base.DraftID, &base.Title, &base.Markdown)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		if err != nil {
			return nil, err
		}
		if base.DraftID != draftID {
			return nil, fmt.Errorf("%w: base revision 不属于该草稿", ErrInvalidEditorialState)
		}
	}

	// 2) 唯一定位继承：ClaimMap（新契约与一切有映射的修订）与旧 EvidenceMap 兼容
	// 投影应用同一规则——excerpt 非空、在旧/新正文各恰好出现 1 次，且 base 映射中
	// 该 excerpt 身份唯一（同一 excerpt 多条映射 = 身份歧义，全部不继承）；
	// 旧审校行不复制（新 revision 天然无审校记录）。
	var inheritedClaims []models.ClaimMapEntry
	inheritedOwnerClaims := map[string]string{}
	var inheritedEvidence []models.EvidenceMap
	if base.ID != "" {
		claimEntries, err := listClaimMapTx(ctx, tx, base.DraftID, base.ID)
		if err != nil {
			return nil, err
		}
		identityAmbiguous := ambiguousExcerpts(len(claimEntries), func(i int) string { return claimEntries[i].Excerpt })
		// canonical claim_maps 与 entries 必须对该 excerpt 都唯一才可对齐继承；
		// canonical 重复（身份歧义）时该 excerpt 全部不继承。
		canonicalRows, err := listCanonicalClaimMapsTx(ctx, tx, base.ID)
		if err != nil {
			return nil, err
		}
		canonicalAmbiguous := ambiguousExcerpts(len(canonicalRows), func(i int) string { return canonicalRows[i].excerpt })
		canonicalByExcerpt := map[string]canonicalClaimMap{}
		for _, row := range canonicalRows {
			if !canonicalAmbiguous[row.excerpt] {
				canonicalByExcerpt[row.excerpt] = row
			}
		}
		for _, e := range claimEntries {
			if identityAmbiguous[e.Excerpt] || canonicalAmbiguous[e.Excerpt] {
				continue
			}
			row, aligned := canonicalByExcerpt[e.Excerpt]
			if !aligned {
				continue // entries 与 canonical 无法一一对应：不继承
			}
			// 完整身份一致才继承 owner_claim；kind/materials/citations 不一致
			// 说明两份投影漂移，该 excerpt 不继承。
			if row.claimKind != e.ClaimKind ||
				!sameStringIdentity(row.materials, e.MaterialIDs) ||
				!sameStringIdentity(row.citations, e.CitationRefs) {
				continue
			}
			if !uniquelyLocated(base.Markdown, markdown, e.Excerpt) {
				continue
			}
			inheritedClaims = append(inheritedClaims, e)
			inheritedOwnerClaims[e.Excerpt] = row.ownerClaim
		}
		evidenceMaps, err := listEvidenceMapsTx(ctx, tx, base.ID)
		if err != nil {
			return nil, err
		}
		evidenceAmbiguous := ambiguousExcerpts(len(evidenceMaps), func(i int) string { return evidenceMaps[i].Excerpt })
		for _, m := range evidenceMaps {
			if !evidenceAmbiguous[m.Excerpt] && uniquelyLocated(base.Markdown, markdown, m.Excerpt) {
				inheritedEvidence = append(inheritedEvidence, models.EvidenceMap{Kind: m.Kind, Excerpt: m.Excerpt, KeyPointIDs: m.KeyPointIDs})
			}
		}
	}

	// 3) 原子创建：immutable revision（SQL 级守卫 CAS 更新 current 指针）+ 继承映射。
	var version int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(version),0)+1 FROM article_revisions WHERE draft_id=?`, draftID).Scan(&version); err != nil {
		return nil, err
	}
	revision := &models.ArticleRevision{
		ID: uuid.NewString(), DraftID: draftID, Version: version,
		Title: strings.TrimSpace(title), Markdown: markdown, Origin: "owner",
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO article_revisions (id, draft_id, version, origin_job_id, title, markdown, origin, provider, model, prompt_version, cost_cents)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		revision.ID, revision.DraftID, revision.Version, revision.OriginJobID, revision.Title, revision.Markdown, revision.Origin, revision.Provider, revision.Model, revision.PromptVersion, revision.CostCents); err != nil {
		return nil, err
	}
	// 乐观并发守卫：有 base 时要求 current 仍等于 base；空基准（首次修订）要求
	// current 仍为 NULL——两个首次并发请求只有一个能成功。
	var guard string
	if baseRevisionID == "" {
		guard = `UPDATE article_drafts SET title=?, current_revision_id=?, status='reviewing', updated_at=datetime('now') WHERE id=? AND current_revision_id IS NULL`
	} else {
		guard = `UPDATE article_drafts SET title=?, current_revision_id=?, status='reviewing', updated_at=datetime('now') WHERE id=? AND current_revision_id=?`
	}
	guardArgs := []any{revision.Title, revision.ID, draftID}
	if baseRevisionID != "" {
		guardArgs = append(guardArgs, baseRevisionID)
	}
	res, err := tx.ExecContext(ctx, guard, guardArgs...)
	if err != nil {
		return nil, err
	}
	if n, err := res.RowsAffected(); err != nil {
		return nil, err
	} else if n != 1 {
		return nil, fmt.Errorf("%w: 页面已过期：文章已有更新的修订（current=%s base=%q）", ErrConflict, currentID, baseRevisionID)
	}
	if err != nil {
		return nil, err
	}
	if err := insertEvidenceMapsTx(ctx, tx, revision.ID, inheritedEvidence); err != nil {
		return nil, err
	}
	for _, e := range inheritedClaims {
		materials, err := json.Marshal(e.MaterialIDs)
		if err != nil {
			return nil, fmt.Errorf("序列化继承 ClaimMap 材料: %w", err)
		}
		citations, err := json.Marshal(e.CitationRefs)
		if err != nil {
			return nil, fmt.Errorf("序列化继承 ClaimMap 引用: %w", err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO claim_maps (id,work_revision_id,claim_kind,excerpt,keypoint_ids_json,owner_claim,verified_fact_source_ids_json) VALUES (?,?,?,?,?,?,?)`,
			uuid.NewString(), revision.ID, e.ClaimKind, e.Excerpt, string(materials), inheritedOwnerClaims[e.Excerpt], string(citations)); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO claim_map_entries (id,draft_id,revision_id,excerpt,claim_kind,material_ids_json,source_title,citation_refs_json) VALUES (?,?,?,?,?,?,?,?)`,
			uuid.NewString(), draftID, revision.ID, e.Excerpt, e.ClaimKind, string(materials), e.SourceTitle, string(citations)); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetArticleRevision(ctx, revision.ID)
}

// ambiguousExcerpts 收集出现次数 >1 的 excerpt（身份歧义集合）。
func ambiguousExcerpts(n int, at func(i int) string) map[string]bool {
	counts := make(map[string]int, n)
	for i := 0; i < n; i++ {
		counts[at(i)]++
	}
	ambiguous := make(map[string]bool)
	for excerpt, c := range counts {
		if c > 1 {
			ambiguous[excerpt] = true
		}
	}
	return ambiguous
}

// canonicalClaimMap 是 canonical claim_maps 行的完整身份血缘视图。
type canonicalClaimMap struct {
	excerpt    string
	claimKind  string
	materials  []string
	citations  []string
	ownerClaim string
}

// listCanonicalClaimMapsTx 在事务内读取一个修订的全部 canonical claim_maps 行
// （含完整身份：kind/materials/citations，供与 entries 对齐比较）。
func listCanonicalClaimMapsTx(ctx context.Context, tx *sql.Tx, revisionID string) ([]canonicalClaimMap, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT excerpt, claim_kind, keypoint_ids_json, verified_fact_source_ids_json, COALESCE(owner_claim,'')
		 FROM claim_maps WHERE work_revision_id=? ORDER BY rowid`, revisionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []canonicalClaimMap
	for rows.Next() {
		var row canonicalClaimMap
		var materials, citations string
		if err := rows.Scan(&row.excerpt, &row.claimKind, &materials, &citations, &row.ownerClaim); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(materials), &row.materials); err != nil {
			return nil, fmt.Errorf("解析 canonical ClaimMap 材料 ID（revision %s）: %w", revisionID, err)
		}
		if err := json.Unmarshal([]byte(citations), &row.citations); err != nil {
			return nil, fmt.Errorf("解析 canonical ClaimMap 引用（revision %s）: %w", revisionID, err)
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// listEvidenceMapsTx 在事务内读取一个修订的全部 EvidenceMap 行。
func listEvidenceMapsTx(ctx context.Context, tx *sql.Tx, revisionID string) ([]*models.EvidenceMap, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT id, revision_id, kind, excerpt, keypoint_ids_json FROM evidence_maps WHERE revision_id=? ORDER BY rowid`, revisionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*models.EvidenceMap
	for rows.Next() {
		m := &models.EvidenceMap{}
		if err := rows.Scan(&m.ID, &m.RevisionID, &m.Kind, &m.Excerpt, &m.KeyPointIDs); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
