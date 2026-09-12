// Package server：EpisodeDigest 单集精读文 HTTP 层（ADR-0023）。
// T1 手动触发：Source 详情页按钮 → /api/digest 入队；列表批量 → /api/digest/batch 逐集隔离（D1）。
// 草稿页：/digest/{id} 展示块级正文 + ⑥b 检索落源侧栏（确认/剔除）+ 事实缺口 + 渠道改写。
package server

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/woyin/orangecast/internal/auth"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/store"
)

// handleDigestEnqueue 单集"生成精读文"按钮：入队 episode_digest job（T1 手动触发即单次授权）。
func (srv *Server) handleDigestEnqueue(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}
	sourceType := models.SourceType(r.FormValue("source_type"))
	sourceID := r.FormValue("source_id")
	job, err := srv.store.EnqueueDigestJob(r.Context(), sourceType, sourceID)
	if err != nil {
		http.Error(w, "入队失败", http.StatusInternalServerError)
		return
	}
	if job == nil {
		http.Error(w, "该内容已在处理中或尚未完成转录", http.StatusConflict)
		return
	}
	http.Redirect(w, r, "/sources/"+string(sourceType)+"/"+sourceID, http.StatusSeeOther)
}

// handleDigestBatch 列表批量：逐集独立入队（D1），失败/跳过单集可见。
func (srv *Server) handleDigestBatch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "表单解析失败", http.StatusBadRequest)
		return
	}
	sourceType := models.SourceType(r.FormValue("source_type"))
	podcastID := r.FormValue("podcast_id")
	enqueued, skipped := 0, 0
	for _, sid := range r.Form["source_id"] {
		job, err := srv.store.EnqueueDigestJob(r.Context(), sourceType, sid)
		if err != nil || job == nil {
			skipped++
			continue
		}
		enqueued++
	}
	http.Redirect(w, r, fmt.Sprintf("/podcasts/%s?enqueued=%d&skipped=%d", podcastID, enqueued, skipped), http.StatusSeeOther)
}

// handleDigestDetail 草稿页：当前修订正文（块级）+ ⑥b 侧栏 + 缺口 + 渠道版本。
func (srv *Server) handleDigestDetail(w http.ResponseWriter, r *http.Request) {
	if strings.HasSuffix(r.URL.Path, "/markdown") {
		srv.handleDigestMarkdown(w, r)
		return
	}
	if strings.HasSuffix(r.URL.Path, "/edit") {
		srv.handleDigestEdit(w, r)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/digest/")
	if id == "" || strings.Contains(id, "/") {
		http.NotFound(w, r)
		return
	}
	d, err := srv.store.GetEpisodeDigest(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	blocks, err := srv.store.ListDigestBlocks(r.Context(), d.ID)
	if err != nil {
		http.Error(w, "加载内容块失败", http.StatusInternalServerError)
		return
	}
	searchSources, _ := srv.store.ListDigestSearchSources(r.Context(), d.ID)
	gaps, _ := srv.store.ListDigestFactGaps(r.Context(), d.ID)
	rewrite, _ := srv.store.GetDigestRewrite(r.Context(), d.ID, models.DigestChannelXiaohongshu)

	if err := srv.tmpl.Render(w, "digest.html", map[string]any{
		"Digest": d, "Blocks": blocks, "SearchSources": searchSources, "FactGaps": gaps,
		"Rewrite": rewrite, "CSRF": auth.CSRFValue(r),
	}); err != nil {
		http.Error(w, "渲染失败", http.StatusInternalServerError)
	}
}

// handleDigestSourceStatus ⑥b：确认或剔除一条检索落源。
// 剔除联动删除引用该 Document 的 cited_fact 块（ADR-0023 §3）。
func (srv *Server) handleDigestSearchSourceStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}
	id := r.FormValue("source_row_id")
	status := r.FormValue("status")
	if status != "confirmed" && status != "rejected" {
		http.Error(w, "状态只能是 confirmed 或 rejected", http.StatusBadRequest)
		return
	}
	// 查侧栏行拿 digest/target（handler 层直查，避免 store 加专用读取）
	var digestID, documentID string
	if err := srv.store.DB.QueryRowContext(r.Context(),
		`SELECT digest_id, document_id FROM digest_search_sources WHERE id=?`, id).
		Scan(&digestID, &documentID); err != nil {
		http.Error(w, "落源行不存在", http.StatusNotFound)
		return
	}
	if err := srv.store.SetDigestSearchSourceStatus(r.Context(), id, status); err != nil {
		http.Error(w, "更新失败", http.StatusInternalServerError)
		return
	}
	if status == "rejected" {
		// G03：剔除来源派生一份移除相应事实块的新修订；旧版本仍可审计。
		newID, err := srv.deriveRevisionWithoutSource(r.Context(), digestID, documentID)
		if err != nil {
			http.Error(w, "剔除派生修订失败："+err.Error(), http.StatusInternalServerError)
			return
		}
		if newID != "" {
			digestID = newID
		}
	}
	http.Redirect(w, r, "/digest/"+digestID, http.StatusSeeOther)
}

// deriveRevisionWithoutSource 从当前修订派生移除指定落源 Document 事实块的新修订，
// 返回新修订 ID（重复提交且已派生过时返回空串）。
func (srv *Server) deriveRevisionWithoutSource(ctx context.Context, digestID, documentID string) (string, error) {
	base, err := srv.store.GetEpisodeDigest(ctx, digestID)
	if err != nil {
		return "", err
	}
	blocks, err := srv.store.ListDigestBlocks(ctx, digestID)
	if err != nil {
		return "", err
	}
	kept := make([]models.DigestBlock, 0, len(blocks))
	removed := false
	for _, b := range blocks {
		if b.Type == models.DigestBlockCitedFact && b.TargetSourceID == documentID {
			removed = true
			continue // 移除引用该来源的事实块
		}
		kept = append(kept, *b)
	}
	// 重复提交守卫：同一父修订 + 同一剔除原因已派生过 → 不再重复修订。
	var existing int
	if err := srv.store.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM episode_digests WHERE parent_digest_id=? AND reason=?`,
		digestID, "剔除来源 "+documentID).Scan(&existing); err == nil && existing > 0 {
		return "", nil
	}
	reason := "剔除来源 " + documentID
	if !removed {
		reason = "剔除来源（无关联事实块）" + documentID
	}
	created, err := srv.store.CreateDigestRevision(ctx, store.DigestRevisionInput{
		Base: base, Reason: reason, Blocks: kept,
	})
	if err != nil {
		return "", err
	}
	return created.ID, nil
}

// handleDigestEdit 调整精读正文（G03）：只允许编辑转述/AI 展开块文本或改标题；
// 笔记块与引用事实块不可在此改写。表单携带 base_version，过期编辑返回冲突；
// 新修订由 CreateDigestRevision 校验块身份与引用约束，粘贴文本无法变成引用事实。
func (srv *Server) handleDigestEdit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}
	digestID := strings.TrimPrefix(r.URL.Path, "/digest/")
	digestID = strings.TrimSuffix(digestID, "/edit")
	base, err := srv.store.GetEpisodeDigest(r.Context(), digestID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	baseVersion, _ := strconv.Atoi(r.FormValue("base_version"))
	blocks, err := srv.store.ListDigestBlocks(r.Context(), digestID)
	if err != nil {
		http.Error(w, "加载内容块失败", http.StatusInternalServerError)
		return
	}
	blockID := r.FormValue("block_id")
	newText := strings.TrimSpace(r.FormValue("text"))
	newTitle := strings.TrimSpace(r.FormValue("title"))
	if newTitle == "" {
		newTitle = base.Title
	}
	kept := make([]models.DigestBlock, 0, len(blocks))
	edited := false
	for _, b := range blocks {
		if b.ID == blockID {
			if b.Type != models.DigestBlockParaphrase && b.Type != models.DigestBlockAIExpansion {
				http.Error(w, "该块类型不可编辑（笔记回笔记编辑；引用事实受证据约束）", http.StatusBadRequest)
				return
			}
			b.Text = newText
			edited = true
		}
		kept = append(kept, *b)
	}
	if blockID != "" && !edited {
		http.Error(w, "内容块不存在", http.StatusBadRequest)
		return
	}
	if _, err := srv.store.CreateDigestRevision(r.Context(), store.DigestRevisionInput{
		Base: base, BaseVersionClaimed: baseVersion, Reason: "Owner 编辑", NewTitle: newTitle, Blocks: kept,
	}); err != nil {
		if err == store.ErrDigestRevisionConflict {
			http.Error(w, "修订已被其他操作更新（版本冲突），请刷新后重试", http.StatusConflict)
			return
		}
		http.Error(w, "保存修订失败："+err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/digest/"+digestID, http.StatusSeeOther)
}

// handleDigestRewriteRetry 渠道改写重试（失败不阻塞长文版；Owner 在页面手动重试）。
func (srv *Server) handleDigestRewriteRetry(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}
	digestID := r.FormValue("digest_id")
	d, err := srv.store.GetEpisodeDigest(r.Context(), digestID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	job, err := srv.store.EnqueueDigestJob(r.Context(), d.SourceType, d.SourceID)
	if err != nil || job == nil {
		// 不重跑长文（已有修订是资产）；提示稍后再试
		http.Error(w, "暂时无法重试，请稍后再试", http.StatusConflict)
		return
	}
	http.Redirect(w, r, "/digest/"+digestID, http.StatusSeeOther)
}
