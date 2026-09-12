package server

import (
	"net/http"

	"github.com/woyin/orangecast/internal/auth"
)

// handleDigestList 展示所有已生成的精读文列表（每 Source 最新修订）。
func (srv *Server) handleDigestList(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/digests" {
		http.NotFound(w, r)
		return
	}
	list, err := srv.store.ListEpisodeDigests(r.Context())
	if err != nil {
		http.Error(w, "加载精读文列表失败", http.StatusInternalServerError)
		return
	}
	if err := srv.tmpl.Render(w, "digests.html", map[string]any{
		"Digests": list,
		"CSRF":    auth.CSRFValue(r),
	}); err != nil {
		http.Error(w, "渲染列表失败", http.StatusInternalServerError)
	}
}
