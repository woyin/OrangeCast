package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/woyin/orangecast/internal/models"
)

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

	// R22：真实下一步——继续听（listening_progress → 来源/DJ）、继续写
	//（drafting/reviewing/blocked 文章草稿 → 精确 draft，按画像过滤）、失败任务
	//（按 job 类型精确路由并显示 last_error）。全部只读，不入队不调用模型。

	// 继续听：有 DJ 计划进度时链接 /sources/<type>/<id>/dj，否则到原音来源页。
	listenRows, err := s.DB.QueryContext(ctx,
		`SELECT source_type, source_id, plan_id FROM listening_progress ORDER BY updated_at DESC LIMIT 10`)
	if err != nil {
		return nil, err
	}
	for listenRows.Next() {
		var sourceType, sourceID, planID string
		if err := listenRows.Scan(&sourceType, &sourceID, &planID); err != nil {
			listenRows.Close()
			return nil, err
		}
		href := fmt.Sprintf("/sources/%s/%s", sourceType, sourceID)
		if planID != "" {
			href += "/dj"
		}
		items = append(items, AttentionItem{
			Lane: "learning", Kind: "continue_listening", ID: sourceID,
			Title: "继续听", Detail: "上次播放未结束，从持久进度继续。",
			Href: href,
		})
	}
	if err := listenRows.Err(); err != nil {
		listenRows.Close()
		return nil, err
	}
	listenRows.Close()

	// 继续写：drafting/reviewing/blocked 的文章草稿，按传入画像过滤。
	writeRows, err := s.DB.QueryContext(ctx,
		`SELECT id, title, status FROM article_drafts
		 WHERE editorial_profile_id=? AND status IN ('drafting','reviewing','blocked')
		 ORDER BY updated_at DESC LIMIT 10`, profileID)
	if err != nil {
		return nil, err
	}
	for writeRows.Next() {
		var id, title, status string
		if err := writeRows.Scan(&id, &title, &status); err != nil {
			writeRows.Close()
			return nil, err
		}
		items = append(items, AttentionItem{
			Lane: "creation", Kind: "continue_writing", ID: id,
			Title:  "继续写：" + title,
			Detail: "文章状态：" + status + "。",
			Href:   "/workbench/drafts/" + id,
		})
	}
	if err := writeRows.Err(); err != nil {
		writeRows.Close()
		return nil, err
	}
	writeRows.Close()

	// 失败任务：单条 SQL LEFT JOIN 一次带出各任务类型对应的画像（draft 系按
	// article_drafts、ideation diagnosis 按 ideation_sessions、curator brief 按
	// creation_proposals），全部行 Scan 进内存后立即 Close——Store.MaxOpenConns=1
	// 下严禁在迭代 rows 时再发起查询（单连接死锁）。
	failRows, err := s.DB.QueryContext(ctx,
		`SELECT j.job_type, j.source_type, j.source_id, COALESCE(j.last_error,''),
		        d.editorial_profile_id, sess.editorial_profile_id, p.editorial_profile_id
		 FROM processing_jobs j
		 LEFT JOIN article_drafts d
		        ON j.job_type IN ('claim_writing','claim_revision','claim_review','style_review')
		       AND d.id = j.source_id
		 LEFT JOIN ideation_sessions sess
		        ON j.job_type = 'ideation_diagnosis' AND sess.id = j.source_id
		 LEFT JOIN creation_proposals p
		        ON j.job_type = 'curator_brief' AND p.id = j.source_id
		 WHERE j.status='failed'
		 ORDER BY j.updated_at DESC LIMIT 20`)
	if err != nil {
		return nil, err
	}
	type failedJob struct {
		jobType, sourceType, sourceID, lastError   string
		draftProfile, sessionProfile, briefProfile sql.NullString
	}
	var failed []failedJob
	for failRows.Next() {
		var f failedJob
		if err := failRows.Scan(&f.jobType, &f.sourceType, &f.sourceID, &f.lastError,
			&f.draftProfile, &f.sessionProfile, &f.briefProfile); err != nil {
			failRows.Close()
			return nil, err
		}
		failed = append(failed, f)
	}
	if err := failRows.Err(); err != nil {
		failRows.Close()
		return nil, err
	}
	failRows.Close()

	for _, f := range failed {
		item := AttentionItem{ID: f.jobType + ":" + f.sourceID, Detail: f.lastError}
		switch models.JobType(f.jobType) {
		case models.JobClaimWriting, models.JobClaimRevision, models.JobClaimReview, models.JobStyleReview:
			// draft 系任务：source_id 是 draft 身份；按文章所属画像过滤。
			if !f.draftProfile.Valid || f.draftProfile.String != profileID {
				continue
			}
			item.Lane, item.Kind, item.Title = "creation", "failed_job", "失败任务："+f.jobType
			item.Href = "/workbench/drafts/" + f.sourceID
		case models.JobIdeationDiagnosis:
			// source_id 是 ideation session；按 session 所属画像过滤。
			if !f.sessionProfile.Valid || f.sessionProfile.String != profileID {
				continue
			}
			item.Lane, item.Kind, item.Title = "creation", "failed_job", "失败任务："+f.jobType
			item.Href = "/workbench/ideation/rounds?session_id=" + f.sourceID
		case models.JobCuratorBrief:
			// source_id 是 creation_proposal id（EnqueueCuratorBriefJob 语义）；
			// 按提案所属画像过滤，链接带画像的 workbench。
			if !f.briefProfile.Valid || f.briefProfile.String != profileID {
				continue
			}
			item.Lane, item.Kind, item.Title = "creation", "failed_job", "失败任务："+f.jobType
			item.Href = "/workbench?profile=" + f.briefProfile.String
		case models.JobTranscribe, models.JobAnalyze, models.JobDigest, models.JobHighlight, models.JobNarration, models.JobKeypointQuality, models.JobDigestRewrite, models.JobDJPlan:
			// 来源处理失败属于 learning 泳道；episode/upload 走来源页，document 走文档页。
			item.Lane, item.Kind, item.Title = "learning", "failed_job", "失败任务："+f.jobType
			if models.SourceType(f.sourceType) == models.SourceDocument {
				item.Href = "/documents/" + f.sourceID
			} else {
				item.Href = fmt.Sprintf("/sources/%s/%s", f.sourceType, f.sourceID)
			}
		default:
			continue // 未知/本地任务不猜链接
		}
		items = append(items, item)
	}
	return items, nil
}
