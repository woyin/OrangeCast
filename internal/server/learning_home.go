package server

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/store"
)

func (srv *Server) handleLearningActions(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		actions, err := srv.store.LearningNextActions(r.Context(), srv.reviewNow())
		if err != nil {
			questionHTTPError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(actions)
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(405)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "表单无效", 400)
		return
	}
	revision, err := strconv.Atoi(r.FormValue("expected_revision"))
	if err != nil {
		http.Error(w, "修订无效", 400)
		return
	}
	err = srv.store.SaveLearningPreferences(r.Context(), store.LearningPreferences{CurrentQuestionID: r.FormValue("question_id"), ReflectionPrompt: r.FormValue("reflection_prompt") == "on", Revision: revision}, r.FormValue("request_key"))
	if err != nil {
		questionHTTPError(w, err)
		return
	}
	questionResult(w, r, "/dashboard")
}

func (srv *Server) learningHomeData(r *http.Request, data map[string]any) error {
	actions, err := srv.store.LearningNextActions(r.Context(), srv.reviewNow())
	if err != nil {
		return err
	}
	p, err := srv.store.GetLearningPreferences(r.Context())
	if err != nil {
		return err
	}
	questions, err := srv.store.ListLearningQuestions(r.Context(), "", "active")
	if err != nil {
		return err
	}
	data["NextActions"], data["LearningPreferences"], data["HomeQuestions"], data["HomeRequestKey"] = actions, p, questions, uuid.NewString()
	return nil
}
