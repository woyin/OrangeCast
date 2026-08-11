// Package server 实现 CloudWisePod 的 HTTP 层：路由、handler、模板渲染与 REST API。
// 按职责拆分到多个文件（本文件：设置页，含每个任务独立的 Provider/Model 配置）。
package server

import (
	"net/http"

	"github.com/woyin/orangecast/internal/auth"
	"github.com/woyin/orangecast/internal/models"
)

func (srv *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		// 每个任务独立配置 Provider + Model（ADR-0009 扩展）
		st := &models.Settings{}
		st.TranscriptionModel = strPtr(r.FormValue("transcription_model"))
		st.AnalysisModel = strPtr(r.FormValue("analysis_model"))
		st.HighlightModel = strPtr(r.FormValue("highlight_model"))
		st.QAModel = strPtr(r.FormValue("qa_model"))
		st.TranscriptionProvider = strPtr(r.FormValue("transcription_provider"))
		st.AnalysisProvider = strPtr(r.FormValue("analysis_provider"))
		st.HighlightProvider = strPtr(r.FormValue("highlight_provider"))
		st.QAProvider = strPtr(r.FormValue("qa_provider"))
		st.GroqAPIKey = strPtr(r.FormValue("groq_api_key"))
		st.GroqBaseURL = strPtr(r.FormValue("groq_base_url"))
		st.OpenAIAPIKey = strPtr(r.FormValue("openai_api_key"))
		st.OpenAIBaseURL = strPtr(r.FormValue("openai_base_url"))
		_ = srv.store.UpdateSettings(r.Context(), st)
		// 立即刷新 Selector 的 key/URL
		srv.selector.ApplySettingsFrom(st)
		http.Redirect(w, r, "/settings?saved=1", http.StatusSeeOther)
		return
	}
	st, _ := srv.store.GetSettings(r.Context())
	srv.tmpl.Render(w, "settings.html", map[string]any{
		"TranscriptionModel":    ptrStr(st.TranscriptionModel),
		"AnalysisModel":         ptrStr(st.AnalysisModel),
		"HighlightModel":        ptrStr(st.HighlightModel),
		"QAModel":               ptrStr(st.QAModel),
		"TranscriptionProvider": ptrStr(st.TranscriptionProvider),
		"AnalysisProvider":      ptrStr(st.AnalysisProvider),
		"HighlightProvider":     ptrStr(st.HighlightProvider),
		"QAProvider":            ptrStr(st.QAProvider),
		"HasOpenAI":             srv.selector.HasOpenAI(),
		"Saved":                 r.URL.Query().Get("saved") == "1",
		"CSRF":                  auth.CSRFValue(r),
	})
}
