package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/woyin/orangecast/internal/auth"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
)

func knowledgeUpdateStatus(state string) string {
	labels := map[string]string{"pending": "待核对/接受", "analysing": "正在判断更新", "accepted": "已接受", "generating": "正在更新并审校", "needs_review": "新稿待审", "completed": "更新稿已通过", "parent_changed": "父稿已变化", "insufficient": "材料不足或权限失效", "new_direction": "适合独立新方向", "no_change": "没有实质增量", "failed": "处理失败", "ignored": "已忽略", "deferred": "稍后处理", "update": "适合更新原文"}
	if value, ok := labels[state]; ok {
		return value
	}
	return "待处理"
}
func knowledgeUpdateAction(action string) string {
	labels := map[string]string{"keep": "保留", "add": "补充", "remove": "删除", "refute": "反驳"}
	if value, ok := labels[action]; ok {
		return value
	}
	return "待核对"
}

type knowledgeUpdateActionView struct {
	Action, Reason, ParentLabel, ParentHref string
	MaterialLabels                          []string
}

// RunKnowledgeUpdates inspects local signals even with automation disabled.
// Only the independent opt-in admits model calls, and failed/ignored/deferred
// proposals are never replayed by the scheduler.
func (srv *Server) RunKnowledgeUpdates(ctx context.Context) error {
	if err := srv.store.CollectKnowledgeUpdateSignals(ctx); err != nil {
		return err
	}
	ids, err := srv.store.PendingKnowledgeUpdateArticles(ctx)
	if err != nil {
		return err
	}
	for _, id := range ids {
		a, e := srv.store.GetKnowledgeArticle(ctx, id)
		if e != nil {
			return e
		}
		if a.Status == "discover" || a.Status == "select" || a.Status == "write" || a.Status == "review" || a.Status == "revise" || a.Status == "review_final" {
			continue
		}
		if _, _, e = srv.store.ReserveKnowledgeUpdateProposal(ctx, id, a.WorkingRevision, srv.cfg.KnowledgeStageModels()); e != nil && !errors.Is(e, store.ErrConflict) && !errors.Is(e, store.ErrNotFound) {
			return e
		}
	}
	settings, err := srv.store.GetKnowledgeUpdateSettings(ctx)
	if err != nil || !settings.Enabled || !srv.cfg.PodAvailable() {
		return err
	}
	proposals, err := srv.store.PendingAutomaticKnowledgeUpdates(ctx)
	if err != nil {
		return err
	}
	// Oldest decisions first, with a bounded inbox and transactional daily cap.
	for _, p := range proposals {
		if p.AnalysisJSON == "{}" {
			err = srv.store.QueueKnowledgeUpdateAnalysis(ctx, p.ID, true)
		} else {
			err = srv.store.AcceptKnowledgeUpdate(ctx, p.ID, true)
		}
		if errors.Is(err, store.ErrAutoDailyLimitReached) {
			return nil
		}
		if err != nil && !errors.Is(err, store.ErrConflict) && !errors.Is(err, store.ErrNotFound) {
			return err
		}
	}
	return nil
}

func (srv *Server) handleKnowledgeUpdates(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "方法不允许", 405)
		return
	}
	list, err := srv.store.ListKnowledgeUpdateProposals(r.Context(), r.URL.Query().Get("article"), r.URL.Query().Get("state"))
	if err != nil {
		questionHTTPError(w, err)
		return
	}
	settings, err := srv.store.GetKnowledgeUpdateSettings(r.Context())
	if err != nil {
		http.Error(w, "读取更新设置失败", 500)
		return
	}
	if err = srv.tmpl.Render(w, "knowledge_updates.html", map[string]any{"Proposals": list, "Settings": settings, "Query": r.URL.Query(), "Available": srv.cfg.PodAvailable(), "CSRF": auth.CSRFValue(r)}); err != nil {
		http.Error(w, "渲染更新列表失败", 500)
	}
}

func (srv *Server) handleKnowledgeUpdateDetail(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "方法不允许", 405)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/knowledge-updates/")
	p, err := srv.store.GetKnowledgeUpdateProposal(r.Context(), id)
	if err != nil {
		questionHTTPError(w, err)
		return
	}
	a, err := srv.store.GetKnowledgeArticle(r.Context(), p.ArticleID)
	if err != nil {
		questionHTTPError(w, err)
		return
	}
	parent, err := srv.store.GetKnowledgeRevision(r.Context(), p.ArticleID, p.ParentRevision)
	if err != nil {
		questionHTTPError(w, err)
		return
	}
	var req provider.KnowledgeArticleRequest
	var analysis *provider.KnowledgeUpdateAnalysis
	if json.Unmarshal([]byte(p.InputJSON), &req) != nil || json.Unmarshal([]byte(p.AnalysisJSON), &analysis) != nil {
		http.Error(w, "更新输入损坏", 500)
		return
	}
	if analysis != nil && analysis.Decision == "" {
		analysis = nil
	}
	executions, err := srv.store.ListKnowledgeExecutions(r.Context(), p.ArticleID)
	if err != nil {
		http.Error(w, "读取执行记录失败", 500)
		return
	}
	var working *store.KnowledgeRevision
	if p.GeneratedRevision > 0 {
		working, err = srv.store.GetKnowledgeRevision(r.Context(), p.ArticleID, p.GeneratedRevision)
		if err != nil {
			questionHTTPError(w, err)
			return
		}
	}
	data := map[string]any{"Proposal": p, "Article": a, "Parent": parent, "Working": working, "ParentChanged": a.WorkingRevision != p.ParentRevision && a.WorkingRevision != p.GeneratedRevision, "Request": req, "Analysis": analysis, "Executions": executions, "Available": srv.cfg.PodAvailable(), "CSRF": auth.CSRFValue(r)}
	var parentBlocks, workingBlocks []provider.KnowledgeBlock
	if json.Unmarshal([]byte(parent.BlocksJSON), &parentBlocks) != nil {
		http.Error(w, "父稿损坏", 500)
		return
	}
	if working != nil && json.Unmarshal([]byte(working.BlocksJSON), &workingBlocks) != nil {
		http.Error(w, "生成稿损坏", 500)
		return
	}
	data["ParentBlocks"], data["WorkingBlocks"] = parentBlocks, workingBlocks
	var actions []knowledgeUpdateActionView
	if analysis != nil {
		for _, c := range analysis.Changes {
			v := knowledgeUpdateActionView{Action: c.Action, Reason: c.Reason, ParentLabel: "新增章节"}
			for i, b := range req.Blocks {
				if b.ID == c.BlockID {
					v.ParentLabel = fmt.Sprintf("父稿第%d段", i+1)
					v.ParentHref = fmt.Sprintf("/knowledge-articles/%s?revision=%d#paragraph-%d", a.ID, p.ParentRevision, i)
					break
				}
			}
			for _, id := range c.MaterialIDs {
				for _, m := range req.Materials {
					if m.ID == id {
						v.MaterialLabels = append(v.MaterialLabels, m.SourceTitle+" · "+m.Content)
						break
					}
				}
			}
			actions = append(actions, v)
		}
	}
	data["Actions"] = actions
	if err = srv.tmpl.Render(w, "knowledge_update.html", data); err != nil {
		http.Error(w, "渲染更新提案失败", 500)
	}
}

func (srv *Server) handleKnowledgeUpdateAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", 405)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "表单无效", 400)
		return
	}
	action, id := r.FormValue("action"), r.FormValue("proposal_id")
	var err error
	href := "/knowledge-updates"
	switch action {
	case "settings":
		limit, e := strconv.Atoi(r.FormValue("daily_limit"))
		if e != nil {
			http.Error(w, "次数必须为整数", 400)
			return
		}
		settings := store.KnowledgeUpdateSettings{Enabled: r.FormValue("enabled") == "on", DailyLimit: limit}
		if settings.Enabled && !srv.cfg.PodAvailable() {
			http.Error(w, "请配置.env中的POD_*并重启", 400)
			return
		}
		err = srv.store.SetKnowledgeUpdateSettings(r.Context(), settings)
	case "inspect":
		expected, e := strconv.Atoi(r.FormValue("expected_revision"))
		if e != nil || expected < 1 {
			http.Error(w, "缺少有效父修订号", 400)
			return
		}
		p, _, e := srv.store.ReserveKnowledgeUpdateProposal(r.Context(), r.FormValue("article_id"), expected, srv.cfg.KnowledgeStageModels())
		err = e
		if err == nil {
			href += "/" + p.ID
		}
	case "analyse", "accept", "retry":
		if !srv.cfg.PodAvailable() {
			http.Error(w, "请配置.env中的POD_*并重启", 400)
			return
		}
		if action == "analyse" {
			err = srv.store.QueueKnowledgeUpdateAnalysis(r.Context(), id, false)
		}
		if action == "accept" {
			err = srv.store.AcceptKnowledgeUpdate(r.Context(), id, false)
		}
		if action == "retry" {
			err = srv.store.RetryKnowledgeUpdate(r.Context(), id)
		}
		href += "/" + id
	case "ignore", "defer":
		err = srv.store.SetKnowledgeUpdateDisposition(r.Context(), id, action, r.FormValue("reason"))
		href += "/" + id
	case "resume":
		err = srv.store.ResumeKnowledgeUpdate(r.Context(), id)
		href += "/" + id
	default:
		http.Error(w, "未知更新动作", 400)
		return
	}
	if err != nil {
		questionHTTPError(w, err)
		return
	}
	questionResult(w, r, href)
}
