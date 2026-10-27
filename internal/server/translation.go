// translation.go 对齐译文页面（Translation，CitedDerivative）。
// 原文与译文并列显示，逐 Segment 对齐；未翻译的片段保留原文并明确标注，
// 不用程序补译，也不把译文当作原话。生成是显式 POST 的持久任务。
package server

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"

	"github.com/woyin/orangecast/internal/auth"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
)

// translationSegmentView 一条对齐行的展示数据。
type translationSegmentView struct {
	Index        int
	SegmentID    string
	Start        float64
	End          float64
	Original     string
	Translated   string
	TranslatedOK bool
}

// handleSourceTranslation 展示/触发某来源的对齐译文。
// 路径：/sources/{type}/{id}/translation?lang=zh
func (srv *Server) handleSourceTranslation(w http.ResponseWriter, r *http.Request) {
	sourceType, sourceID, rest, ok := parseSourcePath(r)
	if !ok || len(rest) < 1 || rest[0] != "translation" {
		http.NotFound(w, r)
		return
	}
	if r.Method == http.MethodPost {
		srv.translateSource(w, r, sourceType, sourceID)
		return
	}
	lang := strings.TrimSpace(r.URL.Query().Get("lang"))

	transcript, err := srv.store.GetCurrentVersion(r.Context(), sourceType, sourceID, store.KindTranscript)
	if err != nil {
		http.Error(w, "该来源尚无转录稿，无法翻译", http.StatusNotFound)
		return
	}
	var payload provider.TranscriptPayload
	if err := json.Unmarshal([]byte(transcript.Payload), &payload); err != nil {
		http.Error(w, "转录数据损坏", http.StatusInternalServerError)
		return
	}
	languages, err := srv.store.ListTranslationLanguages(r.Context(), sourceType, sourceID)
	if err != nil {
		http.Error(w, "读取译文语言失败", http.StatusInternalServerError)
		return
	}
	if lang == "" {
		sort.Strings(languages)
	}
	var translations map[string]models.SegmentTranslation
	if lang != "" {
		translations, err = srv.store.ListSegmentTranslations(r.Context(), sourceType, sourceID, transcript.Version, lang)
		if err != nil {
			http.Error(w, "读取译文失败", http.StatusInternalServerError)
			return
		}
	}

	rows := make([]translationSegmentView, 0, len(payload.Segments))
	translated := 0
	for i, seg := range payload.Segments {
		row := translationSegmentView{Index: i + 1, SegmentID: seg.ID, Start: seg.Start, End: seg.End, Original: seg.Text}
		if t, ok := translations[seg.ID]; ok && strings.TrimSpace(t.Text) != "" {
			row.Translated = t.Text
			row.TranslatedOK = true
			translated++
		}
		rows = append(rows, row)
	}

	srv.tmpl.Render(w, "translation.html", map[string]any{
		"SourceType": string(sourceType), "SourceID": sourceID, "Language": lang,
		"Languages": languages, "Rows": rows,
		"Total": len(rows), "Translated": translated, "Missing": len(rows) - translated,
		"TranscriptVersion": transcript.Version, "CSRF": auth.CSRFValue(r),
		"Submitted": r.URL.Query().Get("submitted"),
	})
}

// handleMindMapPage 渲染结构导图容器；数据由只读 API 提供（GET 不触发模型调用）。
func (srv *Server) handleMindMapPage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}
	sourceType, sourceID, rest, ok := parseSourcePath(r)
	if !ok || len(rest) < 1 || rest[0] != "mindmap" {
		http.NotFound(w, r)
		return
	}
	srv.tmpl.Render(w, "mindmap.html", map[string]any{
		"SourceType": string(sourceType), "SourceID": sourceID, "CSRF": auth.CSRFValue(r),
	})
}

// translateSource 入队一次对齐翻译任务（幂等：同版本同语言不重复计费）。
func (srv *Server) translateSource(w http.ResponseWriter, r *http.Request, sourceType models.SourceType, sourceID string) {
	lang := strings.TrimSpace(r.FormValue("target_language"))
	if lang == "" {
		http.Error(w, "请选择或填写目标语言", http.StatusBadRequest)
		return
	}
	if _, err := srv.store.EnqueueTranslateJob(r.Context(), sourceType, sourceID, lang, r.FormValue("refresh")); err != nil {
		http.Error(w, "入队翻译失败："+err.Error(), http.StatusConflict)
		return
	}
	http.Redirect(w, r, "/sources/"+string(sourceType)+"/"+sourceID+"/translation?lang="+lang+"&submitted=1", http.StatusSeeOther)
}
