package store

import "context"

// AttentionItem is a concise owner-facing next action across the two workspaces.
type AttentionItem struct{ Lane, Kind, ID, Title, Detail, Href string }

// AttentionQueue returns only actionable learning and creation records. Failures,
// stale records, and blocking research remain visible instead of being hidden.
func (s *Store) AttentionQueue(ctx context.Context, profileID string) ([]AttentionItem, error) {
	items := []AttentionItem{}
	queries := []struct{ lane, kind, sql string }{
		{"learning", "material_review", `SELECT id,content,'需要确认学习候选' FROM material_candidates WHERE status='pending' ORDER BY created_at DESC LIMIT 20`},
		{"learning", "stale_keypoint", `SELECT id,content,stale_reason FROM keypoint_index WHERE stale_at IS NOT NULL ORDER BY stale_at DESC LIMIT 20`},
		{"creation", "proposal_batch", `SELECT id,shortage_reason,CASE status WHEN 'failed' THEN '自动发现失败' WHEN 'stale' THEN '自动发现素材已失效' ELSE '待处理自动发现批次' END FROM proposal_batches WHERE editorial_profile_id=? AND status IN ('ready','reviewing','failed','stale') ORDER BY created_at DESC LIMIT 20`},
		{"creation", "ideation", `SELECT id,intent,'定向构思等待继续' FROM ideation_sessions WHERE editorial_profile_id=? AND status='active' ORDER BY updated_at DESC LIMIT 20`},
		{"creation", "research", `SELECT r.id,r.question,CASE r.severity WHEN 'blocking' THEN '阻断型研究缺口' ELSE '可增强的研究缺口' END FROM research_needs r JOIN creation_proposals p ON p.id=r.creation_proposal_id WHERE p.editorial_profile_id=? AND r.status!='resolved' ORDER BY r.created_at DESC LIMIT 20`},
		{"creation", "brief", `SELECT b.id,b.owner_claim,'待确认 CreationBrief' FROM creation_briefs b JOIN creation_proposals p ON p.id=b.creation_proposal_id WHERE p.editorial_profile_id=? AND b.status='draft' ORDER BY b.updated_at DESC LIMIT 20`},
	}
	for _, q := range queries {
		args := []any{}
		if q.lane == "creation" {
			args = []any{profileID}
		}
		rows, err := s.DB.QueryContext(ctx, q.sql, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var i AttentionItem
			i.Lane, i.Kind = q.lane, q.kind
			if q.lane == "learning" {
				i.Href = "/keypoints"
			} else {
				i.Href = "/workbench?profile=" + profileID
			}
			if err := rows.Scan(&i.ID, &i.Title, &i.Detail); err != nil {
				rows.Close()
				return nil, err
			}
			items = append(items, i)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	return items, nil
}
