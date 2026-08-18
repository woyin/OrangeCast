package server

import (
	"net/http"
	"strings"

	"github.com/woyin/orangecast/internal/auth"
	"github.com/woyin/orangecast/internal/store"
)

// handleAttentionQueue renders the two-lane owner attention queue.
func (srv *Server) handleAttentionQueue(w http.ResponseWriter, r *http.Request) {
	profiles, err := srv.store.ListEditorialProfiles(r.Context())
	if err != nil {
		http.Error(w, "加载编辑画像失败", 500)
		return
	}
	profileID := strings.TrimSpace(r.URL.Query().Get("profile"))
	if profileID == "" && len(profiles) > 0 {
		profileID = profiles[0].ID
	}
	items := []store.AttentionItem{}
	if profileID != "" {
		items, err = srv.store.AttentionQueue(r.Context(), profileID)
		if err != nil {
			http.Error(w, "加载注意力队列失败", 500)
			return
		}
	}
	learning, creation := []store.AttentionItem{}, []store.AttentionItem{}
	for _, item := range items {
		if item.Lane == "learning" {
			learning = append(learning, item)
		} else {
			creation = append(creation, item)
		}
	}
	if err := srv.tmpl.Render(w, "attention.html", map[string]any{"Profiles": profiles, "ProfileID": profileID, "Learning": learning, "Creation": creation, "CSRF": auth.CSRFValue(r)}); err != nil {
		http.Error(w, "渲染注意力队列失败", 500)
	}
}
