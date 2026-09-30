package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/woyin/orangecast/internal/auth"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
)

// StartKnowledgeArticles runs the explicitly enabled automatic-article scheduler.
func (srv *Server) StartKnowledgeArticles(ctx context.Context) {
	go func() {
		if err := srv.RunKnowledgeArticles(ctx); err != nil {
			log.Printf("自动知识文章: %v", err)
		}
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := srv.RunKnowledgeArticles(ctx); err != nil {
					log.Printf("自动知识文章: %v", err)
				}
			}
		}
	}()
}

// RunKnowledgeArticles scans the bounded existing library when automation is enabled.
func (srv *Server) RunKnowledgeArticles(ctx context.Context) error {
	settings, err := srv.store.GetKnowledgeArticleSettings(ctx)
	if err != nil || !settings.Enabled {
		return err
	}
	if !srv.cfg.PodAvailable() {
		return fmt.Errorf("POD_* 配置不完整，自动文章暂停")
	}
	profile, err := srv.store.EnsureDefaultEditorialProfile(ctx)
	if err != nil {
		return err
	}
	_, _, err = srv.enqueueKnowledgeArticle(ctx, profile.ID, true)
	return err
}

func (srv *Server) enqueueKnowledgeArticle(ctx context.Context, profileID string, automatic bool) (*store.KnowledgeArticleRecord, bool, error) {
	if !srv.cfg.PodAvailable() {
		return nil, false, fmt.Errorf("请在 .env 配置 POD_BASE_URL、POD_API_KEY、POD_MODEL 并重启")
	}
	req, latest, err := srv.store.BuildKnowledgeArticleRequest(ctx, profileID, "pod")
	if err != nil {
		return nil, false, err
	}
	req.ReviewModel = srv.cfg.KnowledgeReviewModel()
	if automatic {
		settings, err := srv.store.GetKnowledgeArticleSettings(ctx)
		if err != nil {
			return nil, false, err
		}
		if !store.KnowledgeDebounceReady(latest, settings.DebounceMinutes, time.Now()) {
			return nil, false, nil
		}
	}
	return srv.store.ReserveKnowledgeArticle(ctx, profileID, "pod", srv.cfg.PodModel, req, automatic)
}

func (srv *Server) handleKnowledgeArticles(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "方法不允许", 405)
		return
	}
	records, err := srv.store.ListKnowledgeArticles(r.Context())
	if err != nil {
		http.Error(w, "读取文章失败", 500)
		return
	}
	settings, err := srv.store.GetKnowledgeArticleSettings(r.Context())
	if err != nil {
		http.Error(w, "读取自动化设置失败", 500)
		return
	}
	if err := srv.tmpl.Render(w, "knowledge_articles.html", map[string]any{"Articles": records, "Settings": settings, "Available": srv.cfg.PodAvailable(), "Model": srv.cfg.PodModel, "CSRF": auth.CSRFValue(r)}); err != nil {
		http.Error(w, "渲染文章列表失败", 500)
	}
}

func (srv *Server) handleKnowledgeArticleGenerate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", 405)
		return
	}
	profile, err := srv.store.EnsureDefaultEditorialProfile(r.Context())
	if err != nil {
		http.Error(w, "读取默认文章偏好失败", 500)
		return
	}
	article, _, err := srv.enqueueKnowledgeArticle(r.Context(), profile.ID, false)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	http.Redirect(w, r, "/knowledge-articles/"+article.ID, http.StatusSeeOther)
}

func (srv *Server) handleKnowledgeArticleSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", 405)
		return
	}
	daily, e1 := strconv.Atoi(r.FormValue("daily_limit"))
	debounce, e2 := strconv.Atoi(r.FormValue("debounce_minutes"))
	settings := store.KnowledgeArticleSettings{Enabled: r.FormValue("enabled") == "on", DailyLimit: daily, DebounceMinutes: debounce, Audience: r.FormValue("audience"), Style: r.FormValue("style")}
	if e1 != nil || e2 != nil {
		http.Error(w, "频率必须为整数", 400)
		return
	}
	if settings.Enabled && !srv.cfg.PodAvailable() {
		http.Error(w, "POD_* 配置不完整，无法开启自动生成", 400)
		return
	}
	if err := srv.store.SetKnowledgeArticleSettings(r.Context(), settings); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	http.Redirect(w, r, "/knowledge-articles", http.StatusSeeOther)
}

func (srv *Server) handleKnowledgeArticleRetry(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", 405)
		return
	}
	id := r.FormValue("article_id")
	if err := srv.store.RetryKnowledgeArticle(r.Context(), id); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	http.Redirect(w, r, "/knowledge-articles/"+id, http.StatusSeeOther)
}

type knowledgeArticleView struct {
	Kind, Text string
	RichHTML   template.HTML
	Links      []knowledgeMaterialLink
}
type knowledgeMaterialLink struct{ Label, Href, Preview, Kind string }

func knowledgeArticleViews(req provider.KnowledgeArticleRequest, blocks []provider.KnowledgeBlock) []knowledgeArticleView {
	materials := map[string]provider.KnowledgeMaterial{}
	for _, m := range req.Materials {
		materials[m.ID] = m
	}
	labels := map[string]string{"source": "来源整理", "reflection": "个人笔记", "synthesis": "AI 综合"}
	views := make([]knowledgeArticleView, 0, len(blocks))
	for _, b := range blocks {
		v := knowledgeArticleView{Kind: labels[b.Kind], Text: b.Text, RichHTML: template.HTML(wechatRichText(b.Text))}
		for _, id := range b.MaterialIDs {
			m, ok := materials[id]
			if !ok {
				continue
			}
			title := m.SourceTitle
			if title == "" {
				title = "个人笔记"
			}
			href := ""
			if m.SnapshotID != "" {
				if m.SourceType == "document" {
					href = fmt.Sprintf("/evidence/%s?position=%.0f", m.SnapshotID, m.Position)
				} else {
					href = fmt.Sprintf("/evidence/%s?t=%.1f", m.SnapshotID, m.Position)
				}
			} else {
				href = sourceHref(models.SourceType(m.SourceType), m.SourceID, 0)
			}
			v.Links = append(v.Links, knowledgeMaterialLink{Label: title, Href: href, Preview: m.Evidence, Kind: m.Kind})
		}
		views = append(views, v)
	}
	return views
}

func (srv *Server) handleKnowledgeArticleDetail(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "方法不允许", 405)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/knowledge-articles/")
	download := strings.HasSuffix(id, "/download")
	id = strings.TrimSuffix(id, "/download")
	article, err := srv.store.GetKnowledgeArticle(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if r.URL.Query().Get("state") == "1" {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(article)
		return
	}
	selectedRevision := article.PassedRevision
	if selectedRevision == 0 {
		selectedRevision = article.WorkingRevision
	}
	if value := r.URL.Query().Get("revision"); value != "" {
		number, e := strconv.Atoi(value)
		if e != nil || number < 1 {
			http.Error(w, "无效修订", 400)
			return
		}
		selectedRevision = number
	}
	var selected *store.KnowledgeRevision
	var evidenceState, evidenceReason string
	if selectedRevision > 0 {
		selected, err = srv.store.GetKnowledgeRevision(r.Context(), article.ID, selectedRevision)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		evidenceState, evidenceReason, err = srv.store.KnowledgeEvidenceState(r.Context(), article, selected)
		if err != nil {
			http.Error(w, "读取依据状态失败", 500)
			return
		}
	}
	var req provider.KnowledgeArticleRequest
	var blocks []provider.KnowledgeBlock
	var issues []string
	var topics []provider.KnowledgeTopic
	if json.Unmarshal([]byte(article.InputJSON), &req) != nil || json.Unmarshal([]byte(article.BlocksJSON), &blocks) != nil || json.Unmarshal([]byte(article.IssuesJSON), &issues) != nil || json.Unmarshal([]byte(article.TopicsJSON), &topics) != nil {
		http.Error(w, "文章数据损坏", 500)
		return
	}
	if selected != nil {
		if json.Unmarshal([]byte(selected.InputJSON), &req) != nil || json.Unmarshal([]byte(selected.BlocksJSON), &blocks) != nil {
			http.Error(w, "修订数据损坏", 500)
			return
		}
	}
	views := knowledgeArticleViews(req, blocks)
	if download {
		if (selected != nil && (!selected.Passed || evidenceState != "valid")) || (selected == nil && article.Status != "ready") {
			http.Error(w, "文章尚未通过审校，暂不能下载", 409)
			return
		}
		// Archived/removed/changed sources invalidate deliverability; snapshots do
		// not bypass a revoked source policy or changed personal note.
		usedIDs := map[string]bool{}
		for _, b := range blocks {
			for _, id := range b.MaterialIDs {
				usedIDs[id] = true
			}
		}
		var used []provider.KnowledgeMaterial
		for _, m := range req.Materials {
			if usedIDs[m.ID] {
				used = append(used, m)
			}
		}
		if err := srv.store.CheckKnowledgeMaterials(r.Context(), article.ProfileID, article.Provider, used); err != nil {
			http.Error(w, "来源或笔记已变化，需重新生成："+err.Error(), 409)
			return
		}
		var text strings.Builder
		title := article.Title
		if selected != nil {
			title = selected.Title
		}
		text.WriteString("# " + title + "\n\n")
		for _, v := range views {
			text.WriteString("**" + v.Kind + "**\n\n" + v.Text + "\n\n")
			for _, link := range v.Links {
				text.WriteString("[" + link.Label + "](" + strings.TrimRight(srv.cfg.PublicURL, "/") + link.Href + ") ")
			}
			text.WriteString("\n\n")
		}
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="knowledge-article-`+article.ID+`.md"`)
		_, _ = w.Write([]byte(text.String()))
		return
	}
	revisions, err := srv.store.ListKnowledgeRevisions(r.Context(), article.ID)
	if err != nil {
		http.Error(w, "读取修订失败", 500)
		return
	}
	reviews, err := srv.store.ListKnowledgeReviews(r.Context(), article.ID)
	if err != nil {
		http.Error(w, "读取审校失败", 500)
		return
	}
	feedback, err := srv.store.ListKnowledgeFeedback(r.Context(), article.ID)
	if err != nil {
		http.Error(w, "读取反馈失败", 500)
		return
	}
	executions, err := srv.store.ListKnowledgeExecutions(r.Context(), article.ID)
	if err != nil {
		http.Error(w, "读取执行记录失败", 500)
		return
	}
	workReq := req
	workBlocks := blocks
	workTitle := article.Title
	if article.WorkingRevision > 0 && article.WorkingRevision != selectedRevision {
		working, e := srv.store.GetKnowledgeRevision(r.Context(), article.ID, article.WorkingRevision)
		if e != nil {
			http.Error(w, "读取工作稿失败", 500)
			return
		}
		if json.Unmarshal([]byte(working.InputJSON), &workReq) != nil || json.Unmarshal([]byte(working.BlocksJSON), &workBlocks) != nil {
			http.Error(w, "工作稿损坏", 500)
			return
		}
		workTitle = working.Title
	}
	library, _, err := srv.store.BuildKnowledgeArticleRequest(r.Context(), article.ProfileID, article.Provider)
	if err != nil {
		http.Error(w, "读取补充材料失败", 500)
		return
	}
	materials := append([]provider.KnowledgeMaterial(nil), workReq.Materials...)
	seen := map[string]bool{}
	for _, m := range materials {
		seen[m.ID] = true
	}
	for _, m := range library.Materials {
		if !seen[m.ID] {
			materials = append(materials, m)
		}
	}
	editBlocks := make([]knowledgeEditBlock, 0, len(workBlocks))
	for _, block := range workBlocks {
		used := map[string]bool{}
		for _, id := range block.MaterialIDs {
			used[id] = true
		}
		var choices []knowledgeMaterialChoice
		for _, m := range materials {
			choices = append(choices, knowledgeMaterialChoice{Material: m, Selected: used[m.ID]})
		}
		editBlocks = append(editBlocks, knowledgeEditBlock{Block: block, Materials: choices})
	}
	diffs := knowledgeDiffs(revisions)
	data := map[string]any{"Article": article, "Selected": selected, "SelectedRevision": selectedRevision, "EvidenceState": evidenceState, "EvidenceReason": evidenceReason, "Blocks": views, "Issues": issues, "Topics": topics, "Revisions": revisions, "Reviews": reviews, "Feedback": feedback, "Executions": executions, "EditBlocks": editBlocks, "EditTitle": workTitle, "Materials": materials, "Diffs": diffs, "CSRF": auth.CSRFValue(r)}
	if err := srv.tmpl.Render(w, "knowledge_article.html", data); err != nil {
		http.Error(w, "渲染文章失败", 500)
	}
}

type knowledgeMaterialChoice struct {
	Material provider.KnowledgeMaterial
	Selected bool
}
type knowledgeEditBlock struct {
	Block     provider.KnowledgeBlock
	Materials []knowledgeMaterialChoice
}
type knowledgeRevisionDiff struct {
	From, To                                     int
	Before, After, MaterialBefore, MaterialAfter string
}

func knowledgeDiffs(revisions []*store.KnowledgeRevision) []knowledgeRevisionDiff {
	byNumber := map[int]*store.KnowledgeRevision{}
	for _, v := range revisions {
		byNumber[v.Revision] = v
	}
	var out []knowledgeRevisionDiff
	for _, v := range revisions {
		parent := byNumber[v.ParentRevision]
		if parent == nil {
			continue
		}
		out = append(out, knowledgeRevisionDiff{From: parent.Revision, To: v.Revision, Before: knowledgePlainText(parent.BlocksJSON), After: knowledgePlainText(v.BlocksJSON), MaterialBefore: knowledgeMaterialSummary(parent.InputJSON), MaterialAfter: knowledgeMaterialSummary(v.InputJSON)})
	}
	return out
}
func knowledgePlainText(raw string) string {
	var blocks []provider.KnowledgeBlock
	_ = json.Unmarshal([]byte(raw), &blocks)
	var text []string
	for _, b := range blocks {
		text = append(text, b.Text)
	}
	return strings.Join(text, "\n\n")
}
func knowledgeMaterialSummary(raw string) string {
	var req provider.KnowledgeArticleRequest
	_ = json.Unmarshal([]byte(raw), &req)
	var text []string
	for _, m := range req.Materials {
		text = append(text, m.Content)
	}
	return strings.Join(text, "\n")
}

func (srv *Server) handleKnowledgeArticleAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", 405)
		return
	}
	id := r.FormValue("article_id")
	article, err := srv.store.GetKnowledgeArticle(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	expected, e := strconv.Atoi(r.FormValue("expected_revision"))
	if e != nil || expected < 1 {
		http.Error(w, "缺少有效修订版本", 400)
		return
	}
	action := r.FormValue("action")
	switch action {
	case "feedback":
		err = srv.store.RecordKnowledgeFeedback(r.Context(), id, expected, r.FormValue("category"), r.FormValue("comment"))
	case "review", "revise":
		if !srv.cfg.PodAvailable() {
			err = fmt.Errorf("POD_* 配置不完整")
		} else {
			err = srv.store.QueueKnowledgeRevision(r.Context(), id, expected, r.FormValue("instructions"), srv.cfg.KnowledgeReviewModel(), action == "revise")
		}
	case "save":
		working, getErr := srv.store.GetKnowledgeRevision(r.Context(), id, expected)
		if getErr != nil {
			err = getErr
			break
		}
		var req provider.KnowledgeArticleRequest
		if err = json.Unmarshal([]byte(working.InputJSON), &req); err != nil {
			break
		}
		library, _, getErr := srv.store.BuildKnowledgeArticleRequest(r.Context(), article.ProfileID, article.Provider)
		if getErr != nil {
			err = getErr
			break
		}
		available := map[string]provider.KnowledgeMaterial{}
		for _, m := range req.Materials {
			available[m.ID] = m
		}
		for _, m := range library.Materials {
			available[m.ID] = m
		}
		if err = r.ParseForm(); err != nil {
			break
		}
		texts := r.Form["block_text"]
		if len(texts) < 2 || len(texts) > 60 {
			err = store.ErrInvalidEditorialState
			break
		}
		used := map[string]bool{}
		var blocks []provider.KnowledgeBlock
		for i, text := range texts {
			ids := r.Form[fmt.Sprintf("block_material_%d", i)]
			for _, mid := range ids {
				used[mid] = true
			}
			var quotes []provider.KnowledgeQuote
			if raw := r.FormValue(fmt.Sprintf("block_quotes_%d", i)); raw != "" {
				if json.Unmarshal([]byte(raw), &quotes) != nil {
					err = store.ErrInvalidEditorialState
					break
				}
			}
			blocks = append(blocks, provider.KnowledgeBlock{Text: text, Kind: r.FormValue(fmt.Sprintf("block_kind_%d", i)), MaterialIDs: ids, Quotes: quotes})
		}
		for _, mid := range r.Form["extra_material"] {
			used[mid] = true
		}
		req.Materials = nil
		for mid := range used {
			m, ok := available[mid]
			if !ok {
				err = store.ErrInvalidEditorialState
				break
			}
			req.Materials = append(req.Materials, m)
		}
		if err == nil {
			_, err = srv.store.SaveKnowledgeDraft(r.Context(), id, expected, r.FormValue("title"), blocks, req)
		}
	default:
		err = store.ErrInvalidEditorialState
	}
	if err != nil {
		code := 400
		if errors.Is(err, store.ErrConflict) {
			code = 409
		}
		if strings.Contains(r.Header.Get("Accept"), "application/json") {
			http.Error(w, err.Error(), code)
			return
		}
		w.WriteHeader(code)
		_ = srv.tmpl.Render(w, "knowledge_action_error.html", map[string]any{"Error": err.Error(), "Article": article, "DraftTitle": r.FormValue("title"), "DraftBlocks": r.Form["block_text"]})
		return
	}
	target := fmt.Sprintf("/knowledge-articles/%s?revision=%d", id, article.WorkingRevision)
	if action == "save" {
		fresh, _ := srv.store.GetKnowledgeArticle(r.Context(), id)
		target = fmt.Sprintf("/knowledge-articles/%s?revision=%d", id, fresh.WorkingRevision)
	}
	if strings.Contains(r.Header.Get("Accept"), "application/json") {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"redirect": target})
		return
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}
