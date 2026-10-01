package server

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/woyin/orangecast/internal/auth"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/store"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func (srv *Server) handleListeningQueue(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}
	var err error
	if r.Method == http.MethodPost {
		var body struct {
			store.ListeningQueueChange
			ExpectedRevision *int64 `json:"expected_revision"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 65536))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&body) != nil || body.ExpectedRevision == nil {
			http.Error(w, "请求体或队列修订号非法", 400)
			return
		}
		var trailing any
		if decoder.Decode(&trailing) != io.EOF {
			http.Error(w, "请求体非法", 400)
			return
		}
		if body.Action == "play" {
			current, e := srv.store.GetListeningQueue(r.Context())
			if e != nil {
				http.Error(w, "读取队列失败", 500)
				return
			}
			srv.listeningQueueAvailability(r.Context(), current)
			for _, item := range current.Items {
				if item.ID == body.ItemID && !item.Available {
					http.Error(w, "无法播放："+item.Reason, 400)
					return
				}
			}
		}
		q, e := srv.store.ChangeListeningQueue(r.Context(), *body.ExpectedRevision, body.ListeningQueueChange)
		if e == nil {
			srv.listeningQueueAvailability(r.Context(), q)
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			json.NewEncoder(w).Encode(q)
			return
		}
		err = e
	} else {
		q, e := srv.store.GetListeningQueue(r.Context())
		if e == nil {
			srv.listeningQueueAvailability(r.Context(), q)
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			json.NewEncoder(w).Encode(q)
			return
		}
		err = e
	}
	code := 500
	if errors.Is(err, store.ErrConflict) {
		code = 409
	}
	if errors.Is(err, store.ErrNotFound) {
		code = 404
	}
	if errors.Is(err, store.ErrInvalidEditorialState) {
		code = 400
	}
	http.Error(w, "收听队列操作失败："+err.Error(), code)
}

func (srv *Server) handleListeningQueuePage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "方法不允许", 405)
		return
	}
	q, err := srv.store.GetListeningQueue(r.Context())
	if err != nil {
		http.Error(w, "读取收听队列失败", 500)
		return
	}
	srv.listeningQueueAvailability(r.Context(), q)
	if err := srv.tmpl.Render(w, "listening_queue.html", map[string]any{"Queue": q, "CSRF": auth.CSRFValue(r)}); err != nil {
		http.Error(w, "渲染收听队列失败", 500)
	}
}

func (srv *Server) listeningQueueAvailability(ctx context.Context, q *models.ListeningQueue) {
	for i := range q.Items {
		item := &q.Items[i]
		if !item.Available {
			continue
		}
		ea, err := srv.store.GetEvidenceAudio(ctx, item.SourceType, item.SourceID)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			item.Available = false
			item.Reason = "无法确认原音状态"
			continue
		}
		rel := filepath.Clean(ea.RelPath)
		if filepath.IsAbs(rel) || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			item.Available = false
			item.Reason = "原音路径不可用"
			continue
		}
		info, err := os.Stat(filepath.Join(srv.cfg.EvidenceDir, rel))
		if err != nil || !info.Mode().IsRegular() {
			item.Available = false
			item.Reason = "原音文件缺失"
		}
	}
}

// handleListeningSession reports current frozen identity without creating a queue entry.
func (srv *Server) handleListeningSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "方法不允许", 405)
		return
	}
	query := r.URL.Query()
	version := 0
	if raw := query.Get("plan_version"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil {
			http.Error(w, "清单版本非法", 400)
			return
		}
		version = value
	}
	item, err := srv.store.CheckListeningIdentity(r.Context(), models.ListeningQueueItem{SourceType: models.SourceType(query.Get("source_type")), SourceID: query.Get("source_id"), Mode: query.Get("mode"), PlanID: query.Get("plan_id"), PlanVersion: version, AudioSHA256: query.Get("audio_sha256")})
	if err != nil {
		code := 500
		if errors.Is(err, store.ErrInvalidEditorialState) {
			code = 400
		}
		http.Error(w, "检查播放身份失败", code)
		return
	}
	q := &models.ListeningQueue{Items: []models.ListeningQueueItem{item}}
	srv.listeningQueueAvailability(r.Context(), q)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(q.Items[0])
}
