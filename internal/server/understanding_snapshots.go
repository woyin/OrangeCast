package server

import (
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/auth"
	"github.com/woyin/orangecast/internal/store"
	"net/http"
	"strconv"
	"strings"
)

func (srv *Server) registerUnderstandingRoutes(mux *http.ServeMux) {
	mux.Handle("/questions/understanding-action", http.HandlerFunc(srv.handleUnderstandingAction))
}
func (srv *Server) handleQuestionUnderstandings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "方法不允许", 405)
		return
	}
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/questions/"), "/understandings")
	q, e := srv.store.GetLearningQuestion(r.Context(), id)
	if e != nil {
		questionHTTPError(w, e)
		return
	}
	h, e := srv.store.UnderstandingHead(r.Context(), id)
	if e != nil {
		questionHTTPError(w, e)
		return
	}
	before, _ := strconv.Atoi(r.URL.Query().Get("before"))
	history, e := srv.store.HistoryUnderstanding(r.Context(), id, before, 20)
	if e != nil {
		questionHTTPError(w, e)
		return
	}
	var current *store.UnderstandingSnapshot
	if h.CurrentSnapshotID != "" {
		current, e = srv.store.GetUnderstandingSnapshot(r.Context(), h.CurrentSnapshotID)
		if e != nil {
			questionHTTPError(w, e)
			return
		}
	}
	next := 0
	if len(history) == 20 {
		next = history[len(history)-1].Version
	}
	refs, e := srv.store.ListLearningQuestionRelations(r.Context(), id)
	if e != nil {
		questionHTTPError(w, e)
		return
	}
	options := []store.UnderstandingReference{}
	for _, link := range refs {
		if link.Kind == "source" {
			v, e := srv.store.ReadCurrentSourceUnderstandingReference(r.Context(), link.SourceType, link.SourceID)
			if e == nil {
				options = append(options, v)
			}
			continue
		}
		v, e := srv.store.ReadUnderstandingReference(r.Context(), store.UnderstandingReference{Kind: link.Kind, ObjectID: link.ObjectID, Version: link.Version})
		if e == nil {
			options = append(options, v)
		}
	}
	data := map[string]any{"Question": q, "Head": h, "Current": current, "History": history, "ReferenceOptions": options, "NextBefore": next, "RequestKey": uuid.NewString(), "CSRF": auth.CSRFValue(r)}
	if e = srv.tmpl.Render(w, "question_understandings.html", data); e != nil {
		http.Error(w, "渲染理解快照失败", 500)
	}
}
func (srv *Server) handleUnderstandingAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", 405)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 128<<10)
	if e := r.ParseForm(); e != nil {
		http.Error(w, "表单过大或无效", 400)
		return
	}
	id := r.FormValue("question_id")
	qr, e := strconv.Atoi(r.FormValue("question_revision"))
	if e != nil {
		srv.understandingActionError(w, r, "问题修订无效", 400)
		return
	}
	hr, e := strconv.Atoi(r.FormValue("head_revision"))
	if e != nil {
		srv.understandingActionError(w, r, "理解头修订无效", 400)
		return
	}
	if r.FormValue("action") == "choose" {
		e = srv.store.ChooseCurrentUnderstanding(r.Context(), id, r.FormValue("snapshot_id"), qr, hr, r.FormValue("request_key"))
	} else if r.FormValue("action") == "save" {
		if r.FormValue("owner_confirmed") != "yes" {
			srv.understandingActionError(w, r, "请确认这是你编辑核对的个人理解", 400)
			return
		}
		var refs []store.UnderstandingReference
		for _, raw := range r.Form["reference"] {
			var v store.UnderstandingReference
			if json.Unmarshal([]byte(raw), &v) != nil {
				srv.understandingActionError(w, r, "引用无效", 400)
				return
			}
			refs = append(refs, v)
		}
		_, e = srv.store.SaveUnderstanding(r.Context(), store.SaveUnderstandingCommand{QuestionID: id, QuestionRevision: qr, HeadRevision: hr, ParentID: r.FormValue("parent_id"), RequestKey: r.FormValue("request_key"), Answer: r.FormValue("answer"), Uncertainty: r.FormValue("uncertainty"), NextStep: r.FormValue("next_step"), ModelDataPolicy: r.FormValue("model_data_policy"), ApprovedProviders: strings.Fields(r.FormValue("approved_providers")), References: refs})
	} else {
		srv.understandingActionError(w, r, "动作无效", 400)
		return
	}
	if e != nil {
		if strings.Contains(r.Header.Get("Accept"), "application/json") {
			questionHTTPError(w, e)
			return
		}
		code := http.StatusBadRequest
		if errors.Is(e, store.ErrConflict) {
			code = http.StatusConflict
		} else if errors.Is(e, store.ErrNotFound) {
			code = http.StatusNotFound
		}
		srv.understandingActionError(w, r, e.Error(), code)
		return
	}
	questionResult(w, r, "/questions/"+id+"/understandings")
}

// understandingActionError retains the submitted revision and command identity for native form recovery.
func (srv *Server) understandingActionError(w http.ResponseWriter, r *http.Request, message string, code int) {
	if strings.Contains(r.Header.Get("Accept"), "application/json") {
		http.Error(w, message, code)
		return
	}
	draft := map[string]string{}
	limits := map[string]int{"action": 40, "question_id": 300, "question_revision": 30, "head_revision": 30, "parent_id": 300, "request_key": 300, "snapshot_id": 300, "answer": 40000, "uncertainty": 10000, "next_step": 10000, "model_data_policy": 40, "approved_providers": 1000}
	omitted := false
	for field, limit := range limits {
		value := r.FormValue(field)
		if len(value) <= limit {
			draft[field] = value
		} else {
			omitted = true
		}
	}
	refs := []string{}
	for _, raw := range r.Form["reference"] {
		if len(refs) >= 100 || len(raw) > 80000 || !json.Valid([]byte(raw)) {
			omitted = true
			continue
		}
		refs = append(refs, raw)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	if err := srv.tmpl.Render(w, "understanding_recovery.html", map[string]any{"Draft": draft, "References": refs, "Message": message, "Omitted": omitted, "CSRF": auth.CSRFValue(r)}); err != nil {
		_, _ = w.Write([]byte("理解草稿恢复页面无法显示"))
	}
}
