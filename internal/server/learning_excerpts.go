package server

import (
	"encoding/json"
	"errors"
	"github.com/woyin/orangecast/internal/store"
	"io"
	"net/http"
)

// Explicit creation freezes a real interval; reads never create queue entries.
func (srv *Server) handleLearningExcerpts(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", 405)
		return
	}
	var body struct {
		SnapshotID string   `json:"snapshot_id"`
		SegmentIDs []string `json:"segment_ids"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32768))
	dec.DisallowUnknownFields()
	if dec.Decode(&body) != nil {
		http.Error(w, "区间请求非法", 400)
		return
	}
	var tail any
	if dec.Decode(&tail) != io.EOF {
		http.Error(w, "请求体非法", 400)
		return
	}
	excerpt, err := srv.store.CreateLearningExcerpt(r.Context(), body.SnapshotID, body.SegmentIDs)
	if err != nil {
		status := 500
		if errors.Is(err, store.ErrInvalidEditorialState) {
			status = 400
		}
		if errors.Is(err, store.ErrNotFound) {
			status = 404
		}
		if errors.Is(err, store.ErrConflict) || errors.Is(err, store.ErrSnapshotInvalidated) {
			status = 409
		}
		http.Error(w, "区间不可创建："+err.Error(), status)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(excerpt)
}
