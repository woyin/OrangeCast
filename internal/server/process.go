// Package server 实现 CloudWisePod 的 HTTP 层：路由、handler、模板渲染与 REST API。
// 按职责拆分到多个文件（本文件：处理入队与批量入队）。
package server

import (
	"fmt"
	"net/http"

	"github.com/woyin/orangecast/internal/models"
)

func (srv *Server) handleProcess(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}
	sourceType := models.SourceType(r.FormValue("source_type"))
	sourceID := r.FormValue("source_id")
	job, err := srv.store.EnqueueJob(r.Context(), sourceType, sourceID, models.JobTranscribe)
	if err != nil {
		http.Error(w, "入队失败", http.StatusInternalServerError)
		return
	}
	_ = job // SQLite 驱动 worker 循环会领取并处理（ADR-0006）
	http.Redirect(w, r, "/sources/"+string(sourceType)+"/"+sourceID, http.StatusSeeOther)
}

// handleProcessBatch 批量入队（便利入口，不持久化批次）。
// 部分成功：能入的入，状态不对的跳过；返回 enqueued/skipped 计数。
func (srv *Server) handleProcessBatch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "表单解析失败", http.StatusBadRequest)
		return
	}
	sourceType := models.SourceType(r.FormValue("source_type"))
	sourceIDs := r.Form["source_id"]
	podcastID := r.FormValue("podcast_id")

	enqueued := 0
	skipped := 0
	for _, sid := range sourceIDs {
		job, err := srv.store.EnqueueJob(r.Context(), sourceType, sid, models.JobTranscribe)
		if err != nil {
			skipped++
			continue
		}
		if job != nil {
			enqueued++
		} else {
			skipped++
		}
	}
	http.Redirect(w, r, fmt.Sprintf("/podcasts/%s?enqueued=%d&skipped=%d", podcastID, enqueued, skipped), http.StatusSeeOther)
}
