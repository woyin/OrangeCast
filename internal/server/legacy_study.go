package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/store"
)

func (srv *Server) legacyStudyError(w http.ResponseWriter, err error) {
	code := 500
	message := "学习对话任务处理失败"
	if errors.Is(err, store.ErrNotFound) {
		code = 404
		message = "会话、任务或来源已不存在"
	} else if errors.Is(err, store.ErrConflict) {
		code = 409
		message = "会话版本、范围或控制状态已变化，请核对原任务；没有自动重发"
	} else if errors.Is(err, store.ErrInvalidEditorialState) {
		code = 400
		message = "学习对话命令或材料无效"
	}
	writeJSON(w, code, map[string]any{"error": message})
}
func (srv *Server) handleLegacyStudyStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "方法不允许", 405)
		return
	}
	var turn *store.LegacyStudyTurn
	var job *models.ProcessingJob
	var err error
	if key := r.URL.Query().Get("request_key"); key != "" {
		turn, job, err = srv.store.LegacyStudyRequest(r.Context(), key)
	} else {
		turn, err = srv.store.GetLegacyStudyTurn(r.Context(), r.URL.Query().Get("turn_id"))
		if err == nil {
			jobID := turn.GenerationJobID
			if turn.CheckJobID != "" {
				jobID = turn.CheckJobID
			}
			job, err = srv.store.GetJob(r.Context(), jobID)
		}
	}
	if err != nil {
		srv.legacyStudyError(w, err)
		return
	}
	srv.writeLegacyStudyStatus(w, r, turn, job, 200)
}
func (srv *Server) writeLegacyStudyStatus(w http.ResponseWriter, r *http.Request, turn *store.LegacyStudyTurn, job *models.ProcessingJob, code int) {
	// Request lookup's original job may already have handed off to the check task.
	if turn.CheckJobID != "" && turn.CheckJobID != job.ID {
		if current, err := srv.store.GetJob(r.Context(), turn.CheckJobID); err == nil {
			job = current
		}
	}
	var revision int
	err := srv.store.DB.QueryRowContext(r.Context(), `SELECT control_revision FROM processing_jobs WHERE id=?`, job.ID).Scan(&revision)
	if err != nil {
		srv.legacyStudyError(w, err)
		return
	}
	response := map[string]any{"session_id": turn.SessionID, "turn_id": turn.ID, "revision": turn.Revision, "state": turn.State, "job_id": job.ID, "job_status": job.Status, "job_revision": revision, "generated": false, "scope_feedback": turn.Feedback, "retry": job.Status == models.StatusFailed, "result_unknown": turn.State == "unknown"}
	if turn.Answer != nil && !turn.Answer.Suppressed && turn.State == "accepted" {
		response["generated"] = true
		response["answer"] = turn.Answer.Content
		response["references"] = turn.Answer.ReferenceSegmentIDs
		response["ai_note"] = "AI 讲解·非原文（参考，不可逐字核验）"
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, code, response)
}
func (srv *Server) handleLegacyStudyRetry(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", 405)
		return
	}
	revision, err := strconv.Atoi(r.FormValue("job_revision"))
	if err != nil {
		srv.legacyStudyError(w, store.ErrInvalidEditorialState)
		return
	}
	job, _, err := srv.store.RetryLegacyStudy(r.Context(), r.FormValue("job_id"), r.FormValue("request_key"), revision, r.FormValue("allow_unknown") == "1")
	if err != nil {
		srv.legacyStudyError(w, err)
		return
	}
	execution, err := srv.store.GetJobExecution(r.Context(), job.ID)
	var in store.LegacyStudyJobInput
	if err != nil || json.Unmarshal([]byte(execution.InputSnapshotJSON), &in) != nil {
		srv.legacyStudyError(w, store.ErrConflict)
		return
	}
	turn, err := srv.store.GetLegacyStudyTurn(r.Context(), in.TurnID)
	if err != nil {
		srv.legacyStudyError(w, err)
		return
	}
	srv.writeLegacyStudyStatus(w, r, turn, job, 202)
}
