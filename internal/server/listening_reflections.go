package server

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/woyin/orangecast/internal/store"
)

type reflectionCommand struct {
	ID               string                  `json:"id"`
	RequestKey       string                  `json:"request_key"`
	Action           string                  `json:"action"`
	ExpectedRevision int                     `json:"expected_revision"`
	Capture          store.ListeningCapture  `json:"capture"`
	QuestionID       string                  `json:"question_id"`
	QuestionRevision int                     `json:"question_revision"`
	Answers          store.ReflectionAnswers `json:"answers"`
}

func (srv *Server) handleListeningReflections(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		var result any
		var err error
		if id := r.URL.Query().Get("id"); id != "" {
			result, err = srv.store.GetListeningReflection(r.Context(), id)
		} else {
			result, err = srv.store.ListListeningReflections(r.Context())
		}
		if err != nil {
			questionHTTPError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(result)
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(405)
		return
	}
	var cmd reflectionCommand
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cmd); err != nil {
		http.Error(w, "整理请求无效或超过大小限制", 400)
		return
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		http.Error(w, "请求只允许一个命令", 400)
		return
	}
	var result *store.ListeningReflection
	var err error
	switch strings.TrimSpace(cmd.Action) {
	case "start":
		result, err = srv.store.StartListeningReflection(r.Context(), cmd.ID, cmd.Capture, cmd.QuestionID, cmd.QuestionRevision)
	case "edit", "cancel":
		result, err = srv.store.ChangeListeningReflection(r.Context(), cmd.ID, cmd.RequestKey, cmd.Action, cmd.ExpectedRevision, cmd.Answers)
	case "save":
		result, err = srv.store.SaveListeningReflection(r.Context(), cmd.ID, cmd.RequestKey, cmd.ExpectedRevision, cmd.Answers)
	default:
		http.Error(w, "未知整理操作", 400)
		return
	}
	if err != nil {
		questionHTTPError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}
