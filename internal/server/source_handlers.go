// Package server 实现 CloudWisePod 的 HTTP 层：路由、handler、模板渲染与 REST API。
// 按职责拆分到多个文件（本文件：Source 详情、音频、版本、AI DJ、搜索）。
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/woyin/orangecast/internal/auth"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
)

func (srv *Server) handleSourceDetail(w http.ResponseWriter, r *http.Request) {
	sourceType, sourceID, rest, ok := parseSourcePath(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if len(rest) >= 1 && rest[0] == "download" {
		srv.handleDownloadMarkdown(w, r)
		return
	}
	if len(rest) >= 2 && rest[0] == "dj" && rest[1] == "plan" {
		srv.handleDJPlanGenerate(w, r)
		return
	}
	if len(rest) >= 1 && rest[0] == "dj" {
		srv.handleDJ(w, r)
		return
	}
	if len(rest) >= 2 && rest[0] == "versions" && rest[1] == "revert" {
		srv.handleRevertVersion(w, r)
		return
	}
	if len(rest) >= 1 && rest[0] == "versions" {
		srv.handleVersions(w, r)
		return
	}

	status, lastError := srv.sourceStatusAndError(r.Context(), sourceType, sourceID)
	audioURL, err := srv.sourceAudioURL(r.Context(), sourceType, sourceID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	title, summary, segments, card := srv.sourceDetailContent(r.Context(), sourceType, sourceID, status)

	data := map[string]any{
		"Title":      title,
		"Summary":    summary,
		"AudioURL":   audioURL,
		"Segments":   segments,
		"Card":       card,
		"Status":     string(status),
		"LastError":  lastError,
		"SourceType": string(sourceType),
		"SourceID":   sourceID,
		"CSRF":       auth.CSRFValue(r),
	}
	if policy, err := srv.store.GetSourcePolicy(r.Context(), sourceType, sourceID); err == nil {
		data["SourcePolicy"] = policy
	}
	if snap, err := srv.store.FreezeSourceSnapshot(r.Context(), sourceType, sourceID); err == nil {
		data["NoteSnapshotID"] = snap.ID
		data["NoteSnapshotVersion"] = snap.ContentVersion
	}
	if notes, err := srv.store.ListOwnerNotes(r.Context(), sourceType, sourceID); err == nil {
		data["OwnerNotes"] = notes
	}
	if constraints, err := srv.store.ListRightsConstraints(r.Context(), sourceType, sourceID); err == nil {
		data["RightsConstraints"] = constraints
	}
	if candidates, err := srv.store.ListMaterialCandidates(r.Context(), sourceType, sourceID); err == nil {
		data["MaterialCandidates"] = candidates
	}
	// U02/R22：该来源实际参与的精读与文章修订（按 (source_type, source_id) 与
	// claim_map 成员精确关联；修复旧代码把 sourceID 误当 KeyPoint ID 的 bug）。
	// 此查询是导航主数据：错误必须显式 500，不得静默空状态。
	usages, usageErr := srv.store.FindUsageBySource(r.Context(), sourceType, sourceID)
	if usageErr != nil {
		http.Error(w, "读取来源使用记录失败："+usageErr.Error(), http.StatusInternalServerError)
		return
	}
	if len(usages) > 0 {
		data["MaterialUsage"] = usages
	}
	// B09：分阶段进度（知识 / DJ 高光 / 解说），失败阶段带原因与重试入口。
	if stages, err := srv.store.SourceStageStatuses(r.Context(), sourceType, sourceID); err == nil {
		data["Stages"] = stages
	}
	// G07：本源的精读修订列表（生成/继续精读入口，持久身份不猜"最近一篇"）。
	if digests, err := srv.store.ListEpisodeDigestsForSource(r.Context(), sourceType, sourceID); err == nil {
		data["SourceDigests"] = digests
	}
	srv.tmpl.Render(w, "source_detail.html", data)
}

func (srv *Server) sourceAudioURL(ctx context.Context, sourceType models.SourceType, sourceID string) (string, error) {
	audioURL := "/api/audio/" + string(sourceType) + "/" + sourceID
	if _, err := srv.store.GetEvidenceAudio(ctx, sourceType, sourceID); err == nil {
		return audioURL, nil
	}
	if sourceType == models.SourceEpisode {
		episode, err := srv.store.GetEpisodeByID(ctx, sourceID)
		if err != nil {
			return "", err
		}
		return episode.AudioURL, nil
	}
	if _, err := srv.store.GetUploadByID(ctx, sourceID); err != nil {
		return "", err
	}
	return audioURL, nil
}

func (srv *Server) sourceDetailContent(ctx context.Context, sourceType models.SourceType, sourceID string, status models.EpisodeProcessingStatus) (string, string, []provider.Segment, map[string]any) {
	segments := []provider.Segment(nil)
	title, summary := titleForStatus(status), ""
	var card map[string]any
	if transcript, err := srv.store.GetCurrentVersion(ctx, sourceType, sourceID, store.KindTranscript); err == nil {
		var payload provider.TranscriptPayload
		if json.Unmarshal([]byte(transcript.Payload), &payload) == nil {
			segments = payload.Segments
		}
	}
	if knowledgeCard, err := srv.store.GetCurrentVersion(ctx, sourceType, sourceID, store.KindKnowledgeCard); err == nil {
		var payload provider.KnowledgeCard
		if json.Unmarshal([]byte(knowledgeCard.Payload), &payload) == nil {
			title, summary, card = payload.Title, payload.Summary.Text, cardView(payload, segments)
		}
	}
	return title, summary, segments, card
}

func (srv *Server) handleSourcePolicy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}
	sourceType := models.SourceType(strings.TrimSpace(r.FormValue("source_type")))
	sourceID := strings.TrimSpace(r.FormValue("source_id"))
	current, err := srv.store.GetSourcePolicy(r.Context(), sourceType, sourceID)
	if err != nil {
		http.Error(w, "读取素材策略失败："+err.Error(), http.StatusBadRequest)
		return
	}
	approved := strings.Split(r.FormValue("approved_providers"), ",")
	// production_use is legacy migration data, not a creative-use permission.
	policy := models.SourcePolicy{ProductionUse: current.ProductionUse, ModelDataPolicy: models.ModelDataPolicy(strings.TrimSpace(r.FormValue("model_data_policy"))), ApprovedProviders: approved, Archived: r.FormValue("archived") == "1"}
	if err := srv.store.UpdateSourcePolicy(r.Context(), sourceType, sourceID, policy); err != nil {
		http.Error(w, "更新素材策略失败："+err.Error(), http.StatusBadRequest)
		return
	}
	if sourceType == models.SourceDocument {
		http.Redirect(w, r, "/documents/"+sourceID, http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/sources/"+string(sourceType)+"/"+sourceID, http.StatusSeeOther)
}

// handleAudio 提供 Source 音频：优先 EvidenceAudio（ADR-0005）；
// upload 在证据生成前回退到原始落盘文件（仅该实例内可访问）。
func (srv *Server) handleAudio(w http.ResponseWriter, r *http.Request) {
	// /api/audio/{sourceType}/{sourceID}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/audio/"), "/")
	if len(parts) < 2 {
		http.NotFound(w, r)
		return
	}
	sourceType := models.SourceType(parts[0])
	sourceID := parts[1]

	// 优先证据音频
	if ev, err := srv.store.GetEvidenceAudio(r.Context(), sourceType, sourceID); err == nil {
		path := filepath.Join(srv.cfg.EvidenceDir, ev.RelPath)
		if _, serr := os.Stat(path); serr == nil {
			http.ServeFile(w, r, path)
			return
		}
	}
	// upload 回退：原始落盘文件（处理前预览）
	if sourceType == models.SourceUpload {
		if _, err := srv.store.GetUploadByID(r.Context(), sourceID); err == nil {
			path := filepath.Join(srv.cfg.TempDir, "uploads", sourceID)
			http.ServeFile(w, r, path)
			return
		}
	}
	http.NotFound(w, r)
}

// handleNarration serve 一个 Highlight 的当前 Narration wav（ADR-0019）。
// 路径：/api/narration/{sourceType}/{sourceID}/{highlightID}
// Narration 存于 NarrationDir（独立于 evidence），wav 格式。
func (srv *Server) handleNarration(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/narration/"), "/")
	if len(parts) < 3 {
		http.NotFound(w, r)
		return
	}
	sourceType := models.SourceType(parts[0])
	sourceID := parts[1]
	highlightID := parts[2]
	nar, err := srv.store.GetCurrentNarration(r.Context(), sourceType, sourceID, highlightID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	path := filepath.Join(srv.cfg.NarrationDir, nar.RelPath)
	if _, serr := os.Stat(path); serr != nil {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, path)
}

// handleVersions 查看一个 Source 的不可变版本历史（ADR-0011）。
func (srv *Server) handleVersions(w http.ResponseWriter, r *http.Request) {
	sourceType, sourceID, _, ok := parseSourcePath(r)
	if !ok {
		http.NotFound(w, r)
		return
	}

	title := sourceID
	if sourceType == models.SourceEpisode {
		if ep, err := srv.store.GetEpisodeByID(r.Context(), sourceID); err == nil {
			title = ep.Title
		} else {
			http.NotFound(w, r)
			return
		}
	} else {
		if up, err := srv.store.GetUploadByID(r.Context(), sourceID); err == nil {
			title = up.OriginalFilename
		} else {
			http.NotFound(w, r)
			return
		}
	}

	transcripts, _ := srv.store.ListArtifactVersions(r.Context(), sourceType, sourceID, store.KindTranscript)
	cards, _ := srv.store.ListArtifactVersions(r.Context(), sourceType, sourceID, store.KindKnowledgeCard)
	currentT, _ := srv.store.GetCurrentVersion(r.Context(), sourceType, sourceID, store.KindTranscript)
	currentC, _ := srv.store.GetCurrentVersion(r.Context(), sourceType, sourceID, store.KindKnowledgeCard)

	curT, curC := 0, 0
	if currentT != nil {
		curT = currentT.Version
	}
	if currentC != nil {
		curC = currentC.Version
	}
	srv.tmpl.Render(w, "versions.html", map[string]any{
		"SourceType": string(sourceType), "SourceID": sourceID, "Title": title,
		"Transcripts": transcripts, "Cards": cards,
		"CurrentTranscript": curT, "CurrentCard": curC,
		"CSRF": auth.CSRFValue(r),
	})
}

// handleRevertVersion 把 Source 当前版本指针回退到指定版本（ADR-0011 "可恢复"）。
func (srv *Server) handleRevertVersion(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}
	sourceType, sourceID, rest, ok := parseSourcePath(r)
	if !ok || len(rest) < 2 || rest[0] != "versions" || rest[1] != "revert" {
		http.NotFound(w, r)
		return
	}
	kindStr := r.FormValue("kind")
	version, err := strconv.Atoi(r.FormValue("version"))
	if err != nil {
		http.Error(w, "版本号无效", http.StatusBadRequest)
		return
	}
	kind := store.ArtifactKind(kindStr)
	if kind != store.KindTranscript && kind != store.KindKnowledgeCard {
		http.Error(w, "kind 无效", http.StatusBadRequest)
		return
	}
	if err := srv.store.SetCurrentVersion(r.Context(), sourceType, sourceID, kind, version); err != nil {
		http.Error(w, "回退失败：版本不存在", http.StatusNotFound)
		return
	}
	http.Redirect(w, r, "/sources/"+string(sourceType)+"/"+sourceID+"/versions", http.StatusSeeOther)
}

// handleDJ 渲染 DJ 播放清单页面（R09）：页面读取确切持久化 DJPlan 及其来源快照，
// 不用"当前高光"冒充清单；旧来源无清单时提供显式生成动作（POST），GET 不触发模型调用。
func (srv *Server) handleDJ(w http.ResponseWriter, r *http.Request) {
	sourceType, sourceID, rest, ok := parseSourcePath(r)
	if !ok || len(rest) < 1 || rest[0] != "dj" {
		http.NotFound(w, r)
		return
	}
	plan, err := srv.store.GetLatestDJPlanForSource(r.Context(), sourceType, sourceID)
	if errors.Is(err, store.ErrNotFound) {
		// 兼容状态：旧来源无清单（不冒充）。展示生成动作（不触发任何模型调用）。
		hv := 0
		if v, err := srv.store.GetCurrentVersion(r.Context(), sourceType, sourceID, store.KindHighlight); err == nil {
			hv = v.Version
		}
		srv.tmpl.Render(w, "dj.html", map[string]any{
			"SourceType": string(sourceType), "SourceID": sourceID,
			"Title": "AI DJ", "PlanExists": false, "HighlightVersion": hv,
			"CSRF": auth.CSRFValue(r),
		})
		return
	} else if err != nil {
		http.Error(w, "读取 DJ 清单失败", http.StatusInternalServerError)
		return
	}

	type djItemView struct {
		Kind         string // narration | evidence
		Position     int
		Text         string // 解说脚本或高光 Gist
		Start, End   float64
		NarrationURL string
		Segments     []string
		Reason       string
		HighlightID  string
	}
	narrations, _ := srv.store.ListCurrentNarrationsForSource(r.Context(), sourceType, sourceID)
	items := make([]djItemView, 0, len(plan.Items))
	for _, it := range plan.Items {
		v := djItemView{
			Kind: it.Kind, Position: it.Position, Reason: it.Reason,
			Start: it.Start, End: it.End, Segments: it.SegmentIDs, HighlightID: it.HighlightID,
		}
		switch it.Kind {
		case models.DJItemNarration:
			v.Text = it.ScriptText
			// 解说音频解析：优先计划脚本身份（plan:<id>:p<pos>），其次高光真实解说。
			pseudo := fmt.Sprintf("plan:%s:p%d", plan.ID, it.Position)
			if _, ok := narrations[pseudo]; ok {
				v.NarrationURL = "/api/narration/" + string(sourceType) + "/" + sourceID + "/" + pseudo
			} else if it.HighlightID != "" {
				if nar, ok := narrations[it.HighlightID]; ok && (it.NarrationID == "" || it.NarrationID == nar.ID) {
					v.NarrationURL = "/api/narration/" + string(sourceType) + "/" + sourceID + "/" + it.HighlightID
				}
			}
		case models.DJItemEvidence:
			v.Text = it.Reason
		}
		items = append(items, v)
	}

	// 页面标题与 Take Aways：来自当前卡片（展示性内容，不充当清单）。
	var card provider.KnowledgeCard
	if cv, err := srv.store.GetCurrentVersion(r.Context(), sourceType, sourceID, store.KindKnowledgeCard); err == nil {
		json.Unmarshal([]byte(cv.Payload), &card)
	}
	audioURL := "/api/audio/" + string(sourceType) + "/" + sourceID
	srv.tmpl.Render(w, "dj.html", map[string]any{
		"SourceType":  string(sourceType),
		"SourceID":    sourceID,
		"Title":       card.Title,
		"Items":       items,
		"KeyPoints":   card.KeyPoints,
		"AudioURL":    audioURL,
		"PlanExists":  true,
		"PlanID":      plan.ID,
		"PlanVersion": plan.Version,
		"PlanTotal":   plan.TotalSeconds,
		// D07：高光版本作为进度身份（恢复只在版本可映射时迁移位置）。
		"HighlightVersion": plan.HighlightVersion,
		"CSRF":             auth.CSRFValue(r),
	})
}

// handleDJPlanGenerate 为旧来源显式生成 DJ 清单任务（POST；GET 不触发模型调用）。
func (srv *Server) handleDJPlanGenerate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}
	sourceType, sourceID, rest, ok := parseSourcePath(r)
	if !ok || len(rest) < 2 || rest[0] != "dj" || rest[1] != "plan" {
		http.NotFound(w, r)
		return
	}
	hv, err := srv.store.GetCurrentVersion(r.Context(), sourceType, sourceID, store.KindHighlight)
	if err != nil {
		http.Error(w, "尚无高光片段，请先完成处理", http.StatusNotFound)
		return
	}
	if _, err := srv.store.EnqueueDJPlanJob(r.Context(), sourceType, sourceID, hv.Version, 0); err != nil {
		http.Error(w, "入队 DJ 清单任务失败："+err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/sources/"+string(sourceType)+"/"+sourceID+"/dj", http.StatusSeeOther)
}

func (srv *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "方法不允许", 405)
		return
	}
	q := knowledgeQuery(r)
	result, err := srv.store.SearchKnowledge(r.Context(), q)
	if err != nil {
		code := 500
		if errors.Is(err, store.ErrInvalidEditorialState) {
			code = 400
		}
		http.Error(w, "搜索失败："+err.Error(), code)
		return
	}
	sources, err := srv.store.ListKnowledgeSearchSources(r.Context())
	if err != nil {
		http.Error(w, "读取来源范围失败", 500)
		return
	}
	podcasts, err := srv.store.ListPodcasts(r.Context())
	if err != nil {
		http.Error(w, "读取节目范围失败", 500)
		return
	}
	views := knowledgeSearchViews(result.Hits)
	data := map[string]any{"Sources": sources, "Podcasts": podcasts, "Query": q.Text, "Filter": q, "Results": views, "Total": result.Total, "Page": result.Page, "PerPage": result.PerPage, "Previous": knowledgePageURL(r, result.Page-1), "Next": knowledgePageURL(r, result.Page+1), "HasPrevious": result.Page > 1, "HasNext": result.Page*result.PerPage < result.Total}
	if err := srv.tmpl.Render(w, "search.html", data); err != nil {
		http.Error(w, "渲染搜索失败", 500)
	}
}

// handleSourceSnapshot 按快照 ID 定位引用的只读端点（B01 / ADR-0024 §4）。
// 音频快照返回带起止秒数的 Segment 与原音可回听性；文档快照返回段落位置（无秒数）。
// 已 Purge / 版本不存在返回 410，明确失效而不是静默解析当前版本。
func (srv *Server) handleSourceSnapshot(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/source-snapshots/")
	if id == "" || strings.Contains(id, "/") {
		http.NotFound(w, r)
		return
	}
	snap, segments, docSegments, err := srv.store.SnapshotContent(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if errors.Is(err, store.ErrSnapshotInvalidated) {
		http.Error(w, "来源快照已失效", http.StatusGone)
		return
	}
	if err != nil {
		http.Error(w, "读取快照失败", http.StatusInternalServerError)
		return
	}
	payload := map[string]any{
		"snapshot":          snap,
		"audio_segments":    segments,
		"document_segments": docSegments,
	}
	if snap.Kind == models.SnapshotKindAudio {
		identity, err := srv.store.SnapshotAudioIdentity(r.Context(), id)
		if err != nil {
			http.Error(w, "读取原音身份失败", http.StatusInternalServerError)
			return
		}
		payload["audio"] = identity
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		http.Error(w, "编码失败", http.StatusInternalServerError)
	}
}
