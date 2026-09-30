package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/store"
)

func (srv *Server) handleRightsConstraint(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}
	sourceType := models.SourceType(strings.TrimSpace(r.FormValue("source_type")))
	sourceID := strings.TrimSpace(r.FormValue("source_id"))
	if err := srv.store.UpsertRightsConstraint(r.Context(), sourceType, sourceID, strings.TrimSpace(r.FormValue("constraint_kind")), strings.TrimSpace(r.FormValue("details")), r.FormValue("active") == "on"); err != nil {
		http.Error(w, "保存 RightsConstraint 失败："+err.Error(), http.StatusBadRequest)
		return
	}
	if sourceType == models.SourceDocument {
		http.Redirect(w, r, "/documents/"+sourceID, http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/sources/"+string(sourceType)+"/"+sourceID, http.StatusSeeOther)
}

// handleOwnerNote preserves drafts on AJAX errors and validates note identities server-side.
func (srv *Server) handleOwnerNote(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}
	sourceType := models.SourceType(strings.TrimSpace(r.FormValue("source_type")))
	sourceID := strings.TrimSpace(r.FormValue("source_id"))
	var note *models.OwnerNote
	var err error
	noteID := strings.TrimSpace(r.FormValue("note_id"))
	if noteID != "" {
		expected, convErr := strconv.Atoi(r.FormValue("expected_revision"))
		if convErr != nil || expected < 1 {
			http.Error(w, "缺少有效的笔记版本号（请刷新页面后重试）", http.StatusBadRequest)
			return
		}
		current, getErr := srv.store.GetOwnerNote(r.Context(), noteID)
		if getErr != nil {
			err = getErr
		} else if current.SourceID != sourceID || current.SourceType != string(sourceType) {
			err = store.ErrInvalidEditorialState
		} else if r.FormValue("action") == "delete" {
			err = srv.store.DeleteOwnerNote(r.Context(), noteID, expected)
		} else {
			note, err = srv.store.UpdateOwnerNote(r.Context(), noteID, r.FormValue("content"), r.FormValue("citations_json"), r.FormValue("references_json"), expected)
		}
	} else if r.FormValue("action") == "delete" {
		err = store.ErrInvalidEditorialState
	} else {
		note, err = srv.store.CreateOwnerNote(r.Context(), models.OwnerNote{SourceType: string(sourceType), SourceID: sourceID, Kind: r.FormValue("kind"), Content: r.FormValue("content"), CitationsJSON: r.FormValue("citations_json"), ReferencesJSON: r.FormValue("references_json"), AnchorJSON: r.FormValue("anchor_json")})
	}
	if err != nil {
		code := http.StatusBadRequest
		message := "保存笔记失败，草稿和原位置仍保留：" + err.Error()
		if errors.Is(err, store.ErrConflict) {
			code = http.StatusConflict
			message = "笔记已被其他编辑更新或已被精读引用（版本冲突）；草稿仍保留，请基于最新版本重试。"
		}
		if errors.Is(err, store.ErrNotFound) {
			code = http.StatusNotFound
		}
		http.Error(w, message, code)
		return
	}
	if strings.Contains(r.Header.Get("Accept"), "application/json") {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"saved": true, "note": note})
		return
	}
	target := "/sources/" + string(sourceType) + "/" + sourceID
	if sourceType == models.SourceDocument {
		target = "/documents/" + sourceID
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func (srv *Server) handleNoteHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/notes/"), "/")
	if len(parts) != 2 || parts[1] != "history" {
		http.NotFound(w, r)
		return
	}
	revisions, err := srv.store.ListOwnerNoteRevisions(r.Context(), parts[0])
	if err != nil {
		http.Error(w, "读取历史失败", http.StatusInternalServerError)
		return
	}
	if len(revisions) == 0 {
		http.NotFound(w, r)
		return
	}
	usages, err := srv.store.KnowledgeMaterialUsages(r.Context(), "", "", parts[0])
	if err != nil {
		http.Error(w, "读取采用位置失败", 500)
		return
	}
	srv.tmpl.Render(w, "note_history.html", map[string]any{"Revisions": revisions, "Usages": usages})
}

func (srv *Server) handleFrozenEvidence(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/evidence/")
	if id == "" || strings.Contains(id, "/") {
		http.NotFound(w, r)
		return
	}
	snap, segments, docs, err := srv.store.SnapshotContent(r.Context(), id)
	if err != nil {
		code := http.StatusInternalServerError
		if errors.Is(err, store.ErrNotFound) {
			code = http.StatusNotFound
		}
		if errors.Is(err, store.ErrSnapshotInvalidated) {
			code = http.StatusGone
		}
		http.Error(w, "冻结依据不可用", code)
		return
	}
	data := map[string]any{"Snapshot": snap, "Segments": segments, "DocumentSegments": docs}
	if snap.Kind == models.SnapshotKindAudio {
		audio, err := srv.store.SnapshotAudioIdentity(r.Context(), id)
		if err != nil {
			http.Error(w, "读取原音身份失败", http.StatusInternalServerError)
			return
		}
		data["Audio"] = audio
		if audio.Status == models.AudioPlayable {
			data["AudioURL"] = "/api/audio/" + string(snap.SourceType) + "/" + snap.SourceID
		}
	}
	srv.tmpl.Render(w, "evidence.html", data)
}
