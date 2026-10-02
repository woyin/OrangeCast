package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/woyin/orangecast/internal/auth"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
)

type questionStudyStatusView struct {
	store.QuestionStudyTurn
	JobID    string
	Revision int
	Retry    bool
}

type questionStudyClaimView struct {
	Kind, Text string
	References []questionStudyReferenceView
}
type questionStudyReferenceView struct{ Label, Href string }
type questionStudyAnswerView struct {
	Ordinal int
	Input   string
	Claims  []questionStudyClaimView
}

func (srv *Server) handleQuestionStudy(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/questions/"), "/study")
	question, err := srv.store.GetLearningQuestion(r.Context(), id)
	if err != nil {
		questionHTTPError(w, err)
		return
	}
	if r.Method == http.MethodPost {
		if err = r.ParseForm(); err != nil {
			http.Error(w, "表单无效", 400)
			return
		}
		action, key, sessionID := r.FormValue("action"), r.FormValue("request_key"), r.FormValue("session_id")
		if action == "start" {
			session, e := srv.store.StartQuestionStudySession(r.Context(), id, key)
			if e != nil {
				srv.questionStudyCommandError(w, r, e)
				return
			}
			questionResult(w, r, r.URL.Path+"?session="+url.QueryEscape(session.ID))
			return
		}
		session, e := srv.store.GetQuestionStudySession(r.Context(), sessionID)
		if e != nil || session.QuestionID != id {
			srv.questionStudyCommandError(w, r, store.ErrNotFound)
			return
		}
		revision, e := strconv.Atoi(r.FormValue("expected_revision"))
		if e != nil {
			srv.questionStudyCommandError(w, r, store.ErrInvalidEditorialState)
			return
		}
		switch action {
		case "ask":
			if r.FormValue("scope_confirmed") != "yes" {
				srv.questionStudyCommandError(w, r, errors.New("请明确确认本轮使用的已关联范围"))
				return
			}
			generation, review := srv.cfg.QuestionStudyModels()
			client, e := srv.selector.QuestionStudy(generation, review)
			if e != nil {
				srv.questionStudyCommandError(w, r, errors.New("问题对话模型连接未配置或无效"))
				return
			}
			_, _, _, err = srv.store.SubmitQuestionStudyTurn(r.Context(), session.ID, revision, r.FormValue("input"), key, r.Form["material_key"], client.Config())
		case "retry":
			job, e := srv.store.GetJob(r.Context(), r.FormValue("job_id"))
			if e != nil || string(job.JobType) != "question_study" || job.SourceID != id {
				srv.questionStudyCommandError(w, r, store.ErrNotFound)
				return
			}
			ex, e := srv.store.GetJobExecution(r.Context(), job.ID)
			if e != nil {
				srv.questionStudyCommandError(w, r, e)
				return
			}
			var in store.QuestionStudyJobInput
			if json.Unmarshal([]byte(ex.InputSnapshotJSON), &in) != nil {
				srv.questionStudyCommandError(w, r, store.ErrInvalidEditorialState)
				return
			}
			turn, e := srv.store.GetQuestionStudyTurn(r.Context(), in.TurnID)
			if e != nil || turn.SessionID != session.ID {
				srv.questionStudyCommandError(w, r, store.ErrNotFound)
				return
			}
			_, _, err = srv.store.RetryQuestionStudyGeneration(r.Context(), job.ID, key, revision, r.FormValue("allow_unknown") == "yes")
		default:
			err = store.ErrInvalidEditorialState
		}
		if err != nil {
			srv.questionStudyCommandError(w, r, err)
			return
		}
		questionResult(w, r, r.URL.Path+"?session="+url.QueryEscape(session.ID))
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "方法不允许", 405)
		return
	}
	sessions, err := srv.store.ListQuestionStudySessions(r.Context(), id)
	if err != nil {
		questionHTTPError(w, err)
		return
	}
	data := map[string]any{"Question": question, "Sessions": sessions, "CSRF": auth.CSRFValue(r)}
	sessionID := r.URL.Query().Get("session")
	if sessionID != "" {
		session, e := srv.store.GetQuestionStudySession(r.Context(), sessionID)
		if e != nil || session.QuestionID != id {
			questionHTTPError(w, store.ErrNotFound)
			return
		}
		history, e := srv.store.QuestionStudyHistory(r.Context(), sessionID)
		if e != nil {
			questionHTTPError(w, e)
			return
		}
		statuses, e := srv.store.QuestionStudyTurnStatuses(r.Context(), sessionID)
		if e != nil {
			questionHTTPError(w, e)
			return
		}
		statusViews := []questionStudyStatusView{}
		for _, turn := range statuses {
			v := questionStudyStatusView{QuestionStudyTurn: turn, JobID: turn.GenerationJobID}
			if turn.CheckJobID != "" {
				v.JobID = turn.CheckJobID
			}
			var status string
			if v.JobID != "" {
				if e = srv.store.DB.QueryRowContext(r.Context(), `SELECT control_revision,status FROM processing_jobs WHERE id=?`, v.JobID).Scan(&v.Revision, &status); e != nil {
					questionHTTPError(w, e)
					return
				}
				v.Retry = status == "failed"
			}
			statusViews = append(statusViews, v)
		}
		data["Session"], data["Statuses"] = session, statusViews
		frozenQuestion, e := srv.store.FreezeLearningQuestion(r.Context(), id, false)
		if e == nil {
			materials, e := srv.store.Retrieve(r.Context(), store.KnowledgeRetrieveQuery{Purpose: store.RetrieveExternal, Search: store.KnowledgeSearchQuery{Question: frozenQuestion, SendProvider: "pod", MetadataOnly: true, PerPage: 20}})
			if e != nil {
				questionHTTPError(w, e)
				return
			}
			data["Materials"] = materials.Hits
		}
		if strings.Contains(r.Header.Get("Accept"), "application/json") {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"revision": session.Revision, "turns": statuses})
			return
		}
		answers := []questionStudyAnswerView{}
		for _, turn := range history {
			var answer provider.QuestionStudyAnswer
			var scope provider.QuestionStudyScope
			// Resolve citations from the original scope, never the current transcript.
			full, e := srv.store.GetQuestionStudyTurn(r.Context(), turn.ID)
			if e != nil || json.Unmarshal([]byte(turn.AcceptedJSON), &answer) != nil || json.Unmarshal([]byte(full.FrozenJSON), &scope) != nil {
				continue
			}
			v := questionStudyAnswerView{Ordinal: turn.Ordinal, Input: turn.OwnerInput}
			for _, group := range []struct {
				label  string
				claims []provider.QuestionStudyClaim
			}{{"来源观点", answer.SourceClaims}, {"AI解释", answer.AIExplanations}, {"来源共识", answer.Consensus}, {"来源分歧", answer.Disagreements}} {
				for _, claim := range group.claims {
					c := questionStudyClaimView{Kind: group.label, Text: claim.Text}
					for _, ref := range claim.References {
						for _, m := range scope.Materials {
							if m.Key != ref.MaterialKey || m.Revision != ref.Revision {
								continue
							}
							for _, segmentID := range ref.SegmentIDs {
								for _, segment := range m.Segments {
									if segment.SegmentID != segmentID || m.SnapshotID == "" {
										continue
									}
									href := "/evidence/" + url.PathEscape(m.SnapshotID)
									if m.SourceType == "document" {
										href += "?position=" + strconv.FormatFloat(segment.Position, 'f', -1, 64)
									} else {
										href += "?t=" + strconv.FormatFloat(segment.Position, 'f', -1, 64) + "#" + url.PathEscape(segmentID)
									}
									c.References = append(c.References, questionStudyReferenceView{Label: segmentID, Href: href})
								}
							}
						}
					}
					v.Claims = append(v.Claims, c)
				}
			}
			for _, ref := range answer.OwnerUnderstanding {
				for _, m := range scope.Materials {
					if m.Key == ref.MaterialKey && m.Revision == ref.Revision {
						v.Claims = append(v.Claims, questionStudyClaimView{Kind: "你的理解", Text: m.Content})
					}
				}
			}
			answers = append(answers, v)
		}
		data["Answers"] = answers
	}
	if err = srv.tmpl.Render(w, "question_study.html", data); err != nil && !errors.Is(err, http.ErrHandlerTimeout) {
		http.Error(w, "渲染问题对话失败", 500)
	}
}

func (srv *Server) questionStudyCommandError(w http.ResponseWriter, r *http.Request, err error) {
	if strings.Contains(r.Header.Get("Accept"), "application/json") {
		questionHTTPError(w, err)
		return
	}
	code := 400
	if errors.Is(err, store.ErrConflict) {
		code = 409
	} else if errors.Is(err, store.ErrNotFound) {
		code = 404
	}
	fields := []struct{ Name, Value string }{}
	for _, name := range []string{"action", "session_id", "expected_revision", "request_key", "job_id", "scope_confirmed", "allow_unknown", "material_key"} {
		for _, value := range r.Form[name] {
			fields = append(fields, struct{ Name, Value string }{name, value})
		}
	}
	w.WriteHeader(code)
	_ = srv.tmpl.Render(w, "question_study_error.html", map[string]any{"CSRF": auth.CSRFValue(r), "Href": r.URL.Path, "Error": err.Error(), "Input": r.FormValue("input"), "Fields": fields})
}
