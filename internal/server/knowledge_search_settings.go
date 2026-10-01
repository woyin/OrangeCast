package server

import (
	"net/http"

	"github.com/woyin/orangecast/internal/auth"
	"github.com/woyin/orangecast/internal/store"
)

// Settings is a read-only projection. A measured connection remains disabled;
// preflight itself requires an explicit persistent command.
func (srv *Server) handleKnowledgeSearchSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	data := map[string]any{"CSRF": auth.CSRFValue(r), "Capacity": store.KnowledgeEmbeddingCapacity, "MaxDimensions": 2048}
	client, err := srv.selector.Embedding()
	if err == nil {
		route := client.Config()
		data["Connection"] = route
		rows, e := srv.store.DB.QueryContext(r.Context(), `SELECT id FROM knowledge_embedding_configs WHERE connection_id=? AND model=? AND (?=0 OR dimensions=?) ORDER BY updated_at DESC LIMIT 8`, route.ConnectionID, route.Model, route.Dimensions, route.Dimensions)
		if e != nil {
			http.Error(w, "读取搜索配置失败", 500)
			return
		}
		var ids []string
		for rows.Next() {
			var id string
			if e = rows.Scan(&id); e != nil {
				break
			}
			ids = append(ids, id)
		}
		if e == nil {
			e = rows.Err()
		}
		rows.Close()
		if e != nil {
			http.Error(w, "读取搜索配置失败", 500)
			return
		}
		var statuses []*store.EmbeddingIndexStatus
		for _, id := range ids {
			status, e := srv.store.KnowledgeEmbeddingStatus(r.Context(), id)
			if e != nil {
				http.Error(w, "读取索引状态失败", 500)
				return
			}
			statuses = append(statuses, status)
		}
		data["Statuses"] = statuses
	}
	if err = srv.tmpl.Render(w, "knowledge_search_settings.html", data); err != nil {
		http.Error(w, "渲染搜索设置失败", 500)
	}
}
