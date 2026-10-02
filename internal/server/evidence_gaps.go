package server

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/woyin/orangecast/internal/auth"
	"github.com/woyin/orangecast/internal/store"
)

// handleEvidenceGaps is a local, explicit-command view. GET only projects
// persisted limitations; finding candidates is Owner-triggered and never paid.
func (srv *Server) handleEvidenceGaps(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		http.Error(w, "方法不允许", 405)
		return
	}
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/questions/"), "/gaps")
	question, err := srv.store.GetLearningQuestion(r.Context(), id)
	if err != nil {
		questionHTTPError(w, err)
		return
	}

	if r.Method == http.MethodPost {
		r.Body = http.MaxBytesReader(w, r.Body, 32<<10)
		if err = r.ParseForm(); err != nil {
			http.Error(w, "表单过大或无效", 400)
			return
		}
	}
	semantic := r.FormValue("semantic") == "1"
	configID := r.FormValue("config_id")
	// Reading a configured route and cached vectors never starts embedding work.
	if semantic {
		cfg, e := srv.currentKnowledgeEmbeddingConfig(r.Context(), configID)
		if e != nil {
			configID = ""
		} else {
			configID = cfg.ID
		}
	}
	var candidates *store.EvidenceGapCandidates
	if r.Method == http.MethodGet && r.URL.Query().Get("gap") != "" {
		result, e := srv.store.FindEvidenceGapCandidates(r.Context(), "question", id, r.URL.Query().Get("gap"), r.URL.Query().Get("q"), semantic, configID)
		if e != nil {
			questionHTTPError(w, e)
			return
		}
		gaps, e := srv.store.ListEvidenceGaps(r.Context(), "question", id)
		if e != nil {
			questionHTTPError(w, e)
			return
		}
		for _, g := range gaps {
			if g.ID == r.URL.Query().Get("gap") && g.State == "expired" {
				result.Coverage = "原缺口的判断已过期；这是对当前本地索引重新运行的候选查询。旧判断不会改变，新增关系需当前有效范围。" + result.Coverage
			}
		}
		candidates = &result
	}
	if r.Method == http.MethodPost {
		rev, e := strconv.Atoi(r.FormValue("expected_revision"))
		if e != nil {
			questionHTTPError(w, store.ErrInvalidEditorialState)
			return
		}
		gapID := r.FormValue("gap_id")
		switch r.FormValue("action") {
		case "create":
			_, err = srv.store.CreateEvidenceGap(r.Context(), "question", id, rev, r.FormValue("kind"), "owner", r.FormValue("explanation"), "Owner记录的材料限制；未检验全库")
		case "find":
			if rev != question.Revision {
				err = store.ErrConflict
				break
			}
			var result store.EvidenceGapCandidates
			result, err = srv.store.FindEvidenceGapCandidates(r.Context(), "question", id, gapID, r.FormValue("query"), semantic, configID)
			candidates = &result
		case "confirm":
			if r.FormValue("retrieval_method") == "" {
				err = store.ErrInvalidEditorialState
				break
			}
			_, err = srv.store.ConfirmEvidenceGapCandidateWithRetrieval(r.Context(), id, gapID, r.FormValue("query"), r.FormValue("candidate_key"), rev, semantic, configID, r.FormValue("retrieval_method"))
		case "excerpt":
			if rev != question.Revision {
				err = store.ErrConflict
				break
			}
			queueRev, e := strconv.ParseInt(r.FormValue("queue_revision"), 10, 64)
			if e != nil {
				err = store.ErrInvalidEditorialState
				break
			}
			result, e := srv.store.FindEvidenceGapCandidates(r.Context(), "question", id, gapID, r.FormValue("query"), semantic, configID)
			if e != nil {
				err = e
				break
			}
			if r.FormValue("retrieval_method") == "" || r.FormValue("retrieval_method") != result.Method {
				err = store.ErrConflict
				break
			}
			found := false
			for _, c := range result.Candidates {
				if c.Key == r.FormValue("candidate_key") && c.Playable && c.SnapshotID != "" && c.SegmentID != "" {
					found = true
					ex, e := srv.store.CreateLearningExcerpt(r.Context(), c.SnapshotID, []string{c.SegmentID})
					if e != nil {
						err = e
						break
					}
					_, err = srv.store.ChangeListeningQueue(r.Context(), queueRev, store.ListeningQueueChange{Action: "add", SourceType: ex.SourceType, SourceID: ex.SourceID, Mode: "excerpt", ExcerptID: ex.ID})
					break
				}
			}
			if !found && err == nil {
				err = store.ErrInvalidEditorialState
			}
		case "disposition":
			gr, e := strconv.Atoi(r.FormValue("gap_revision"))
			if e != nil {
				err = store.ErrInvalidEditorialState
				break
			}
			_, err = srv.store.ChangeEvidenceGap(r.Context(), "question", id, gapID, rev, gr, r.FormValue("state"), r.FormValue("comment"))
		default:
			err = store.ErrInvalidEditorialState
		}
		if err != nil {
			if strings.Contains(r.Header.Get("Accept"), "application/json") {
				questionHTTPError(w, err)
				return
			}
			srv.renderEvidenceGapFailure(w, r, id, err)
			return
		}
		if candidates != nil && strings.Contains(r.Header.Get("Accept"), "application/json") {
			questionResult(w, r, r.URL.Path+"?gap="+url.QueryEscape(gapID)+"&q="+url.QueryEscape(r.FormValue("query"))+"&semantic="+r.FormValue("semantic")+"&config_id="+url.QueryEscape(configID))
			return
		}
		if candidates == nil {
			href := r.URL.Path
			if r.FormValue("action") == "confirm" {
				href += "?gap=" + url.QueryEscape(gapID) + "&q=" + url.QueryEscape(r.FormValue("query")) + "&semantic=" + r.FormValue("semantic") + "&config_id=" + url.QueryEscape(configID)
			}
			questionResult(w, r, href)
			return
		}
	}
	gaps, err := srv.store.ListEvidenceGaps(r.Context(), "question", id)
	if err != nil {
		questionHTTPError(w, err)
		return
	}
	queue, err := srv.store.GetListeningQueue(r.Context())
	if err != nil {
		questionHTTPError(w, err)
		return
	}
	data := map[string]any{"Semantic": semantic, "ConfigID": configID, "QueueRevision": queue.Revision, "Question": question, "Gaps": gaps, "Candidates": candidates, "GapID": r.FormValue("gap_id"), "Query": r.FormValue("query"), "CSRF": auth.CSRFValue(r)}
	if r.Method == http.MethodGet {
		data["GapID"] = r.URL.Query().Get("gap")
		data["Query"] = r.URL.Query().Get("q")
	}
	if err = srv.tmpl.Render(w, "evidence_gaps.html", data); err != nil {
		http.Error(w, "材料缺口渲染失败", 500)
	}
}

// SSR errors preserve Owner prose in the rendered form, including stale CAS.
func (srv *Server) renderEvidenceGapFailure(w http.ResponseWriter, r *http.Request, id string, cause error) {
	q, err := srv.store.GetLearningQuestion(r.Context(), id)
	if err != nil {
		questionHTTPError(w, err)
		return
	}
	gaps, err := srv.store.ListEvidenceGaps(r.Context(), "question", id)
	if err != nil {
		questionHTTPError(w, err)
		return
	}
	queue, err := srv.store.GetListeningQueue(r.Context())
	if err != nil {
		questionHTTPError(w, err)
		return
	}
	code := http.StatusBadRequest
	if errors.Is(cause, store.ErrConflict) {
		code = http.StatusConflict
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)
	data := map[string]any{"Question": q, "Gaps": gaps, "QueueRevision": queue.Revision, "CSRF": auth.CSRFValue(r), "Error": "操作未保存；请核对当前范围。" + cause.Error(), "DraftExplanation": r.FormValue("explanation"), "DraftKind": r.FormValue("kind"), "DraftGapID": r.FormValue("gap_id"), "DraftComment": r.FormValue("comment"), "DraftState": r.FormValue("state"), "DraftQuery": r.FormValue("query")}
	_ = srv.tmpl.Render(w, "evidence_gaps.html", data)
}
