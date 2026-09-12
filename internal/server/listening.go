// Package server：听播进度 API（D07 / ADR-0024 §3）。
// GET 只读（用于"继续听"恢复提示，恢复后等用户播放）；POST 保存进度
// （seq 单调，多标签/倒序请求不会用旧状态覆盖新状态）。节流由客户端负责。
package server

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/woyin/orangecast/internal/models"
)

func (srv *Server) handleListeningProgress(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		sourceType := models.SourceType(r.URL.Query().Get("source_type"))
		sourceID := r.URL.Query().Get("source_id")
		p, err := srv.store.GetListeningProgress(r.Context(), sourceType, sourceID)
		if err != nil {
			// 无记录：返回空对象而非错误（首次收听不是故障）。
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			_, _ = w.Write([]byte(`{}`))
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(p)
	case http.MethodPost:
		var body struct {
			SourceType   string  `json:"source_type"`
			SourceID     string  `json:"source_id"`
			PlanID       string  `json:"plan_id"`
			PlanVersion  int     `json:"plan_version"`
			ItemPosition int     `json:"item_position"`
			HighlightID  string  `json:"highlight_id"`
			Offset       float64 `json:"item_offset_seconds"`
			Speed        float64 `json:"speed"`
			Seq          int64   `json:"seq"`
		}
		data, err := io.ReadAll(io.LimitReader(r.Body, 1<<16))
		if err != nil || json.Unmarshal(data, &body) != nil || body.SourceID == "" {
			http.Error(w, "请求体非法", http.StatusBadRequest)
			return
		}
		p := &models.ListeningProgress{
			SourceType: models.SourceType(body.SourceType), SourceID: body.SourceID,
			PlanID: body.PlanID, PlanVersion: body.PlanVersion,
			ItemPosition: body.ItemPosition, HighlightID: body.HighlightID,
			ItemOffsetSeconds: body.Offset, Speed: body.Speed, Seq: body.Seq,
		}
		if err := srv.store.SaveListeningProgress(r.Context(), p); err != nil {
			http.Error(w, "保存听播进度失败："+err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(map[string]any{"saved": true, "seq": p.Seq})
	default:
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
	}
}
