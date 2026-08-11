// Package server 实现 CloudWisePod 的 HTTP 层：路由、handler、模板渲染与 REST API。
// 按职责拆分到多个文件（本文件：显式 Purge）。
package server

import (
	"net/http"

	"github.com/woyin/orangecast/internal/models"
)

// handlePurge 显式 Purge（ADR-0012）：完整、不可逆地删除 Source 及其全部证据与处理历史。
func (srv *Server) handlePurge(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}
	sourceType := models.SourceType(r.FormValue("source_type"))
	sourceID := r.FormValue("source_id")
	if err := srv.worker.PurgeSource(r.Context(), sourceType, sourceID); err != nil {
		http.Error(w, "Purge 失败", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/uploads", http.StatusSeeOther)
}
