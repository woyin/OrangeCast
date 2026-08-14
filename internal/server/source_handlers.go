// Package server 实现 CloudWisePod 的 HTTP 层：路由、handler、模板渲染与 REST API。
// 按职责拆分到多个文件（本文件：Source 详情、音频、版本、AI DJ、搜索）。
package server

import (
	"context"
	"encoding/json"
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
	approved := strings.Split(r.FormValue("approved_providers"), ",")
	policy := models.SourcePolicy{ProductionUse: strings.TrimSpace(r.FormValue("production_use")), ModelDataPolicy: models.ModelDataPolicy(strings.TrimSpace(r.FormValue("model_data_policy"))), ApprovedProviders: approved, Archived: r.FormValue("archived") == "1"}
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

// handleDJ 渲染 DJ 播放清单页面。
func (srv *Server) handleDJ(w http.ResponseWriter, r *http.Request) {
	sourceType, sourceID, rest, ok := parseSourcePath(r)
	if !ok || len(rest) < 1 || rest[0] != "dj" {
		http.NotFound(w, r)
		return
	}

	// 读取当前 Highlight 版本
	hv, err := srv.store.GetCurrentVersion(r.Context(), sourceType, sourceID, store.KindHighlight)
	if err != nil {
		http.Error(w, "尚无高光片段，请先完成处理", http.StatusNotFound)
		return
	}
	var hs provider.HighlightSet
	if err := json.Unmarshal([]byte(hv.Payload), &hs); err != nil {
		http.Error(w, "高光数据损坏", http.StatusInternalServerError)
		return
	}

	// 读取当前 Transcript（用于解析 Citation 时间范围）
	tv, err := srv.store.GetCurrentVersion(r.Context(), sourceType, sourceID, store.KindTranscript)
	if err != nil {
		http.Error(w, "尚无转录稿", http.StatusNotFound)
		return
	}
	var tp provider.TranscriptPayload
	if err := json.Unmarshal([]byte(tv.Payload), &tp); err != nil {
		http.Error(w, "转录数据损坏", http.StatusInternalServerError)
		return
	}

	// 读取 KnowledgeCard（结尾 Take Aways = KeyPoints）
	var card provider.KnowledgeCard
	if cv, err := srv.store.GetCurrentVersion(r.Context(), sourceType, sourceID, store.KindKnowledgeCard); err == nil {
		json.Unmarshal([]byte(cv.Payload), &card)
	}

	// 构造播放清单：每个 Highlight 解析时间范围
	type highlightView struct {
		HighlightID  string
		Gist         string
		Start        float64
		End          float64
		Citations    []string
		NarrationURL string // 当前 Narration wav 的 URL；空=未生成（前端跳过+显示标记）
	}
	narrations, _ := srv.store.ListCurrentNarrationsForSource(r.Context(), sourceType, sourceID)
	var highlights []highlightView
	for _, h := range hs.Highlights {
		start, end, ok := provider.ResolveCitationSpan(h.Citations, tp.Segments)
		if !ok {
			continue
		}
		hv := highlightView{
			HighlightID: h.ID, Gist: h.Gist, Start: start, End: end, Citations: h.Citations,
		}
		if nar, ok := narrations[h.ID]; ok {
			hv.NarrationURL = "/api/narration/" + string(sourceType) + "/" + sourceID + "/" + h.ID
			_ = nar
		}
		highlights = append(highlights, hv)
	}

	audioURL := "/api/audio/" + string(sourceType) + "/" + sourceID
	srv.tmpl.Render(w, "dj.html", map[string]any{
		"SourceType": string(sourceType),
		"SourceID":   sourceID,
		"Title":      card.Title,
		"Highlights": highlights,
		"KeyPoints":  card.KeyPoints,
		"AudioURL":   audioURL,
		"CSRF":       auth.CSRFValue(r),
	})
}

func (srv *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	var hits []store.SearchHit
	var documents []*models.Document
	if q != "" {
		hits, _ = srv.store.SearchSource(r.Context(), q)
		documents, _ = srv.store.SearchDocuments(r.Context(), q, 20)
	}
	srv.tmpl.Render(w, "search.html", map[string]any{"Query": q, "Hits": hits, "Documents": documents})
}
