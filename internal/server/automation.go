package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/woyin/orangecast/internal/auth"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
)

var runPrivateURL = regexp.MustCompile(`https?://[^\s<>"']+`)
var runPrivateAuth = regexp.MustCompile(`(?i)(bearer\s+|api[_-]?key[=:]\s*)[^\s,;]+`)

func runPublicError(value string) string {
	return runPrivateAuth.ReplaceAllString(runPrivateURL.ReplaceAllString(value, "[远端地址已隐藏]"), "[认证信息已隐藏]")
}

func runHref(v store.RunRecord) string {
	switch v.Lane {
	case "knowledge", "updates":
		return "/knowledge-articles/" + url.PathEscape(v.SourceID)
	case "review":
		return "/review"
	case "voice":
		return "/voice-notes"
	case "study":
		if v.SourceType == "question_study" {
			return "/questions/" + url.PathEscape(v.SourceID) + "/study"
		}
		return "/automation/" + url.PathEscape(v.ID)
	case "index":
		return "/search"
	case "export":
		return "/learning-exports"
	}
	return "/progress"
}

func (srv *Server) handleAutomation(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(405)
		return
	}
	offset, err := strconv.Atoi(r.URL.Query().Get("offset"))
	if r.URL.Query().Get("offset") == "" {
		offset = 0
		err = nil
	}
	if err != nil {
		http.Error(w, "分页参数无效", 400)
		return
	}
	runs, next, err := srv.store.ListRuns(r.Context(), r.URL.Query().Get("status"), r.URL.Query().Get("lane"), offset)
	if err != nil {
		http.Error(w, "无法读取任务列表", 400)
		return
	}
	for i := range runs {
		runs[i].Intent = runPublicError(runs[i].Intent)
		runs[i].Error = runPublicError(runs[i].Error)
		runs[i].BlockReason = runPublicError(runs[i].BlockReason)
	}
	if r.URL.Path == "/api/automation" {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(runs)
		return
	}
	cost, err := srv.store.GetRunCostSummary(r.Context())
	if err != nil {
		http.Error(w, "无法读取费用事实", 500)
		return
	}
	var controls []store.RunControl
	for _, lane := range store.LearningRunLanes() {
		c, e := srv.store.GetRunControl(r.Context(), "lane", lane)
		if e != nil {
			http.Error(w, "无法读取控制状态", 500)
			return
		}
		controls = append(controls, c)
	}
	article, _ := srv.store.GetKnowledgeArticleSettings(r.Context())
	updates, _ := srv.store.GetKnowledgeUpdateSettings(r.Context())
	review, _ := srv.store.GetLearningReviewSettings(r.Context())
	nextOffset := -1
	if next {
		nextOffset = offset + 50
	}
	srv.tmpl.Render(w, "automation.html", map[string]any{"CSRF": auth.CSRFValue(r), "Runs": runs, "Controls": controls, "Cost": cost, "NextOffset": nextOffset, "Offset": offset, "Status": r.URL.Query().Get("status"), "Lane": r.URL.Query().Get("lane"), "ArticleSettings": article, "UpdateSettings": updates, "ReviewSettings": review})
}

type automationMaterial struct {
	ID, Kind, Title, Body, SourceType, SourceID, Snapshot string
	Version                                               int
}

func (srv *Server) handleAutomationDetail(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(405)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/automation/")
	job, err := srv.store.GetJob(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	ex, err := srv.store.GetJobExecution(r.Context(), id)
	if err != nil {
		http.Error(w, "读取冻结事实失败", 500)
		return
	}
	ex.IntentID = runPublicError(ex.IntentID)
	// Use a typed projection, never raw input/checkpoint/config URLs.
	var in struct {
		Stage           string                           `json:"stage"`
		Request         provider.KnowledgeArticleRequest `json:"request"`
		DraftID         string                           `json:"draft_id"`
		PriceKnown      bool                             `json:"price_known"`
		CentsPerMinute  float64                          `json:"cents_per_minute"`
		DurationSeconds float64                          `json:"duration_seconds"`
	}
	recorded := json.Unmarshal([]byte(ex.InputSnapshotJSON), &in) == nil
	if in.Stage == "" {
		in.Stage = in.Request.Stage
	}
	c, err := srv.store.GetRunControl(r.Context(), "direction", string(job.SourceType)+":"+job.SourceID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	var rev, priority int
	var stop bool
	var lane string
	if err = srv.store.DB.QueryRowContext(r.Context(), `SELECT control_revision,priority,stop_requested,run_lane FROM processing_jobs WHERE id=?`, id).Scan(&rev, &priority, &stop, &lane); err != nil {
		http.Error(w, "读取控制失败", 500)
		return
	}
	var embeddingIn store.KnowledgeEmbeddingJobInput
	if job.JobType == "knowledge_embedding" && json.Unmarshal([]byte(ex.InputSnapshotJSON), &embeddingIn) == nil && embeddingIn.Version == store.KnowledgeEmbeddingJobVersion {
		in.Stage = embeddingIn.Kind
		in.Request.Estimate = embeddingIn.Estimate
	}
	recovery := runHref(store.RunRecord{ID: id, Lane: lane, SourceID: job.SourceID})
	var rerankIn store.KnowledgeRerankInput
	if job.JobType == models.JobKnowledgeRerank && json.Unmarshal([]byte(ex.InputSnapshotJSON), &rerankIn) == nil && rerankIn.Version == "knowledge-rerank-v1" {
		in.Stage = "candidate_rerank"
		in.Request.Estimate = rerankIn.Estimate
		q := rerankIn.Request.Search
		values := url.Values{"q": {q.Text}, "kind": {q.Kind}, "source_type": {q.SourceType}, "source_id": {q.SourceID}, "podcast_id": {q.PodcastID}, "theme": {q.Theme}, "from": {q.From}, "until": {q.Until}, "rerank": {"1"}, "embedding_config": {rerankIn.Request.EmbeddingConfigID}}
		if rerankIn.Request.Semantic {
			values.Set("semantic", "1")
		}
		recovery = "/search?" + values.Encode()
	}
	var studyIn store.QuestionStudyJobInput
	if job.JobType == models.JobQuestionStudy && json.Unmarshal([]byte(ex.InputSnapshotJSON), &studyIn) == nil && studyIn.Version == store.QuestionStudyTaskVersion {
		in.Stage = studyIn.Stage
		in.Request.Estimate = studyIn.Estimate
	}
	var materials []automationMaterial
	for _, m := range in.Request.Materials {
		materials = append(materials, automationMaterial{ID: m.ID, Kind: m.Kind, Title: m.SourceTitle, Body: m.Content, SourceType: m.SourceType, SourceID: m.SourceID, Snapshot: m.SnapshotID, Version: m.Version})
	}
	for _, material := range studyIn.Scope.Materials {
		materials = append(materials, automationMaterial{ID: material.Key, Kind: material.Kind, Title: material.Key, Body: material.Content, SourceType: material.SourceType, SourceID: material.SourceID, Snapshot: material.SnapshotID, Version: material.Revision})
	}
	for _, window := range embeddingIn.Windows {
		material := automationMaterial{ID: window.DocKey + ":" + strconv.Itoa(window.WindowNo), Kind: "embedding完整窗口", Title: window.DocKey, Body: window.Input, Snapshot: window.ContentHash, Version: window.Revision}
		var types, ids []string
		for _, ref := range window.Sources {
			types = append(types, ref.SourceType)
			ids = append(ids, ref.SourceID)
		}
		material.SourceType = strings.Join(types, ", ")
		material.SourceID = strings.Join(ids, ", ")
		materials = append(materials, material)
	}

	blockReason := ""
	if e := srv.store.CheckRunControl(r.Context(), id); e != nil {
		blockReason = e.Error()
	}
	if blockReason == "" && len(in.Request.Materials) > 0 {
		var profile string
		if lane == "knowledge" || lane == "updates" {
			a, e := srv.store.GetKnowledgeArticle(r.Context(), job.SourceID)
			if e == nil {
				profile = a.ProfileID
			} else {
				blockReason = e.Error()
			}
		} else if lane == "review" {
			b, e := srv.store.GetLearningReviewBatch(r.Context(), job.SourceID)
			if e == nil {
				profile = b.ProfileID
			} else {
				blockReason = e.Error()
			}
		}
		if profile != "" {
			if e := srv.store.CheckKnowledgeMaterials(r.Context(), profile, ex.ConfiguredProvider, in.Request.Materials); e != nil {
				blockReason = e.Error()
			}
		}
		if e := srv.store.CheckLearningQuestionExecution(r.Context(), in.Request.Question, job.Automated); e != nil {
			blockReason = e.Error()
		}
	}
	reservation, e := srv.store.GetJobBudgetReservation(r.Context(), id)
	if e != nil && !errors.Is(e, store.ErrNotFound) {
		http.Error(w, "读取预算事实失败", 500)
		return
	}
	if reservation != nil {
		reservation.Reason = runPublicError(reservation.Reason)
	}
	actionsOffset := 0
	if raw := r.URL.Query().Get("actions_offset"); raw != "" {
		var e error
		actionsOffset, e = strconv.Atoi(raw)
		if e != nil {
			http.Error(w, "分页参数无效", 400)
			return
		}
	}
	actions, actionsNext, e := srv.store.ListRunControlActions(r.Context(), id, c.Target, actionsOffset)
	if e != nil {
		http.Error(w, "读取动作失败", 400)
		return
	}
	usage, e := srv.store.ListRunUsage(r.Context(), id)
	if e != nil {
		http.Error(w, "读取用量失败", 500)
		return
	}
	paidOrigin := ""
	var paid struct {
		OriginJobID string `json:"origin_job_id"`
	}
	if embeddingIn.OriginJobID != "" {
		paid.OriginJobID = embeddingIn.OriginJobID
	} else {
		_ = json.Unmarshal([]byte(ex.CheckpointJSON), &paid)
	}
	if paid.OriginJobID != "" && paid.OriginJobID != id {
		if _, e := uuid.Parse(paid.OriginJobID); e == nil {
			paidOrigin = paid.OriginJobID
			originalUsage, e := srv.store.ListRunUsage(r.Context(), paidOrigin)
			if e != nil {
				http.Error(w, "读取原调用用量失败", 500)
				return
			}
			usage = originalUsage
		}
	}
	srv.tmpl.Render(w, "automation_detail.html", map[string]any{"CSRF": auth.CSRFValue(r), "Job": job, "Execution": ex, "Error": runPublicError(automationError(job.LastError)), "Direction": c, "Revision": rev, "Priority": priority, "Stopped": stop, "Lane": lane, "Stage": in.Stage, "Recorded": recorded, "Request": in.Request, "Materials": materials, "Duration": in.DurationSeconds, "Recovery": recovery, "Actions": actions, "ActionsNext": actionsNext, "NextActionsOffset": actionsOffset + 50, "Usage": usage, "BlockReason": runPublicError(blockReason), "Reservation": reservation, "AudioPriceKnown": in.PriceKnown, "AudioEstimate": in.DurationSeconds / 60 * in.CentsPerMinute, "PaidOrigin": paidOrigin})
}

func (srv *Server) handleAutomationAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(405)
		return
	}
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		if err := r.ParseMultipartForm(64 << 10); err != nil {
			http.Error(w, "表单无效", 400)
			return
		}
		defer r.MultipartForm.RemoveAll()
	} else if err := r.ParseForm(); err != nil {
		http.Error(w, "表单无效", 400)
		return
	}
	revision, e := strconv.Atoi(r.FormValue("expected_revision"))
	priority := 0
	if r.FormValue("priority") != "" {
		var pe error
		priority, pe = strconv.Atoi(r.FormValue("priority"))
		if pe != nil {
			http.Error(w, "优先级无效", 400)
			return
		}
	}
	if e != nil {
		http.Error(w, "修订无效", 400)
		return
	}
	err := srv.store.ChangeRunControl(r.Context(), r.FormValue("kind"), r.FormValue("target"), r.FormValue("action"), r.FormValue("reason"), r.FormValue("request_key"), revision, priority)
	if err != nil {
		code := 400
		if errors.Is(err, store.ErrConflict) {
			code = 409
		}
		if errors.Is(err, store.ErrNotFound) {
			code = 404
		}
		if strings.Contains(r.Header.Get("Accept"), "application/json") {
			http.Error(w, runPublicError(err.Error())+"；表单原值请核对后再提交", code)
		} else {
			fields := []struct{ Name, Value string }{}
			for _, name := range []string{"kind", "target", "action", "request_key", "expected_revision", "priority"} {
				fields = append(fields, struct{ Name, Value string }{name, r.FormValue(name)})
			}
			w.WriteHeader(code)
			if e := srv.tmpl.Render(w, "automation_error.html", map[string]any{"CSRF": auth.CSRFValue(r), "Error": runPublicError(err.Error()), "Reason": r.FormValue("reason"), "Fields": fields}); e != nil {
				_, _ = fmt.Fprint(w, "控制尚未确认，请核对任务")
			}
		}
		return
	}
	href := "/automation"
	if r.FormValue("kind") == "job" {
		href += "/" + url.PathEscape(r.FormValue("target"))
	}
	if strings.Contains(r.Header.Get("Accept"), "application/json") {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"href": href})
		return
	}
	http.Redirect(w, r, href, 303)
}

func automationError(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}
