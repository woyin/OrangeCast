package server

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/woyin/orangecast/internal/store"
)

func (srv *Server) handleKnowledgeRerank(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", 405)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	if r.ParseForm() != nil || r.PostForm.Get("acknowledge") != "1" {
		http.Error(w, "请明确确认外发与可能计费", 400)
		return
	}
	if id := r.PostForm.Get("job_id"); id != "" {
		revision, _ := strconv.Atoi(r.PostForm.Get("expected_revision"))
		if err := srv.store.ResumeKnowledgeRerank(r.Context(), id, revision); err != nil {
			http.Error(w, "当前任务不能恢复；请核对修订、权限、运行控制与未知远端结果", 409)
			return
		}
		http.Redirect(w, r, "/automation/"+id, http.StatusSeeOther)
		return
	}
	p, err := srv.selector.Reranker()
	if err != nil {
		http.Error(w, "重排连接尚未配置", 409)
		return
	}
	// Search scope is frozen from the original search URL in the form action.
	q := knowledgeQuery(r)
	cfg, _ := srv.currentKnowledgeEmbeddingConfig(r.Context(), r.URL.Query().Get("embedding_config"))
	id := ""
	if cfg != nil {
		id = cfg.ID
	}
	job, _, err := srv.store.ReserveKnowledgeRerank(r.Context(), store.KnowledgeRetrieveQuery{Search: q, Purpose: store.RetrieveLocal, Semantic: r.URL.Query().Get("semantic") == "1", EmbeddingConfigID: id}, p.Config())
	if err != nil {
		http.Error(w, "无法准备重排；请检查候选范围、授权与版本", 409)
		return
	}
	http.Redirect(w, r, "/automation/"+job.ID, http.StatusSeeOther)
}
func (srv *Server) handleKnowledgeSearchFeedback(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		rows, err := srv.store.ExportKnowledgeSearchFeedback(r.Context())
		if err != nil {
			http.Error(w, "读取反馈失败", 500)
			return
		}
		raw, err := store.MarshalSearchFeedbackExport(rows)
		if err != nil {
			http.Error(w, "导出反馈失败", 500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition", `attachment; filename="search-feedback.json"`)
		w.Write(raw)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", 405)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if r.ParseForm() != nil {
		http.Error(w, "无效反馈", 400)
		return
	}
	rev, _ := strconv.Atoi(r.PostForm.Get("revision"))
	err := srv.store.RecordKnowledgeSearchFeedback(r.Context(), store.KnowledgeSearchFeedback{Query: r.PostForm.Get("query"), Key: r.PostForm.Get("key"), Revision: rev, Label: r.PostForm.Get("label"), Method: r.PostForm.Get("method")})
	if err != nil {
		http.Error(w, "反馈对象已失效或输入无效", 409)
		return
	}
	if r.Header.Get("Accept") != "application/json" {
		http.Redirect(w, r, "/search?"+r.URL.RawQuery, http.StatusSeeOther)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"saved": true})
}
