package server

import (
	"context"
	"encoding/json"
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
type knowledgeMaterialLink struct{ Label, Href string }

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
				href = sourceHref(models.SourceType(m.SourceType), m.SourceID, m.Position)
			} else {
				href = sourceHref(models.SourceType(m.SourceType), m.SourceID, 0)
			}
			v.Links = append(v.Links, knowledgeMaterialLink{Label: title, Href: href})
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
	var req provider.KnowledgeArticleRequest
	var blocks []provider.KnowledgeBlock
	var issues []string
	var topics []provider.KnowledgeTopic
	if json.Unmarshal([]byte(article.InputJSON), &req) != nil || json.Unmarshal([]byte(article.BlocksJSON), &blocks) != nil || json.Unmarshal([]byte(article.IssuesJSON), &issues) != nil || json.Unmarshal([]byte(article.TopicsJSON), &topics) != nil {
		http.Error(w, "文章数据损坏", 500)
		return
	}
	views := knowledgeArticleViews(req, blocks)
	if download {
		if article.Status != "ready" {
			http.Error(w, "文章尚未通过审校，暂不能下载", 409)
			return
		}
		// Archived/removed/changed sources invalidate deliverability; snapshots do
		// not bypass a revoked source policy or changed personal note.
		selected := map[string]bool{}
		for _, b := range blocks {
			for _, id := range b.MaterialIDs {
				selected[id] = true
			}
		}
		var used []provider.KnowledgeMaterial
		for _, m := range req.Materials {
			if selected[m.ID] {
				used = append(used, m)
			}
		}
		if err := srv.store.CheckKnowledgeMaterials(r.Context(), article.ProfileID, article.Provider, used); err != nil {
			http.Error(w, "来源或笔记已变化，需重新生成："+err.Error(), 409)
			return
		}
		var text strings.Builder
		text.WriteString("# " + article.Title + "\n\n")
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
	if err := srv.tmpl.Render(w, "knowledge_article.html", map[string]any{"Article": article, "Blocks": views, "Issues": issues, "Topics": topics, "CSRF": auth.CSRFValue(r)}); err != nil {
		http.Error(w, "渲染文章失败", 500)
	}
}
