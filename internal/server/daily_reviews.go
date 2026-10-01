package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/auth"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
)

func dailyReadError(w http.ResponseWriter, err error) {
	code := 500
	if errors.Is(err, store.ErrInvalidEditorialState) {
		code = 400
	}
	if errors.Is(err, store.ErrNotFound) {
		code = 404
	}
	if errors.Is(err, store.ErrConflict) {
		code = 409
	}
	http.Error(w, "读取回顾失败："+err.Error(), code)
}

func (srv *Server) reviewNow() time.Time {
	if srv.reviewClock != nil {
		return srv.reviewClock()
	}
	return time.Now()
}

type dailyReviewView struct {
	Item      store.ReviewSessionItem
	Links     []knowledgeMaterialLink
	History   []store.LearningReviewAnswer
	ActionKey string
}

func (srv *Server) handleDailyReview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "方法不允许", 405)
		return
	}
	ctx := r.Context()
	prefs, err := srv.store.GetLearningReviewSettings(ctx)
	if err != nil {
		dailyReadError(w, err)
		return
	}
	size, err := srv.store.GetReviewSessionSize(ctx)
	if err != nil {
		dailyReadError(w, err)
		return
	}
	due, err := srv.store.DueReviewCount(ctx, srv.reviewNow())
	if err != nil {
		dailyReadError(w, err)
		return
	}
	batches, answers, err := srv.store.ReviewActivityCounts(ctx)
	if err != nil {
		dailyReadError(w, err)
		return
	}
	var session *store.ReviewSession
	if id := r.URL.Query().Get("session"); id != "" {
		session, err = srv.store.GetReviewSession(ctx, id)
	} else {
		session, err = srv.store.ActiveReviewSession(ctx)
	}
	if err != nil {
		dailyReadError(w, err)
		return
	}
	data := map[string]any{"CSRF": auth.CSRFValue(r), "Timezone": prefs.Timezone, "Size": size, "Due": due, "GeneratedBatches": batches, "DailyAnswers": answers, "Session": session, "StartKey": uuid.NewString(), "EndKey": uuid.NewString()}
	if session != nil {
		items, e := srv.store.ListReviewSessionItems(ctx, session.ID)
		if e != nil {
			dailyReadError(w, e)
			return
		}
		var views []dailyReviewView
		for _, item := range items {
			v := dailyReviewView{Item: item, ActionKey: uuid.NewString()}
			if item.Revealed {
				var req provider.KnowledgeArticleRequest
				var ids []string
				if json.Unmarshal([]byte(item.InputJSON), &req) != nil || json.Unmarshal([]byte(item.MaterialIDsJSON), &ids) != nil {
					dailyReadError(w, store.ErrInvalidEditorialState)
					return
				}
				blocks := knowledgeArticleViews(req, []provider.KnowledgeBlock{{Kind: "synthesis", MaterialIDs: ids}})
				if len(blocks) > 0 {
					v.Links = blocks[0].Links
				}
				v.History, e = srv.store.LearningReviewAnswerHistory(ctx, item.ItemID)
				if e != nil {
					dailyReadError(w, e)
					return
				}
			}
			views = append(views, v)
		}
		data["Items"] = views
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if offset < 0 {
		offset = 0
	}
	status := r.URL.Query().Get("status")
	schedules, err := srv.store.ListReviewSchedules(ctx, status, offset)
	if err != nil {
		dailyReadError(w, err)
		return
	}
	next := -1
	if len(schedules) > 50 {
		schedules = schedules[:50]
		next = offset + 50
	}
	type scheduleView struct {
		store.ReviewSchedule
		ActionKey string
	}
	var scheduleViews []scheduleView
	for _, s := range schedules {
		scheduleViews = append(scheduleViews, scheduleView{s, uuid.NewString()})
	}
	data["Schedules"], data["NextOffset"], data["Status"] = scheduleViews, next, status
	historyOffset, _ := strconv.Atoi(r.URL.Query().Get("history_offset"))
	if historyOffset < 0 {
		historyOffset = 0
	}
	history, err := srv.store.ListReviewSessions(ctx, historyOffset)
	if err != nil {
		dailyReadError(w, err)
		return
	}
	historyNext := -1
	if len(history) > 50 {
		history = history[:50]
		historyNext = historyOffset + 50
	}
	data["History"], data["HistoryNext"] = history, historyNext
	questions, err := srv.store.ListLearningQuestions(ctx, "", "")
	if err != nil {
		dailyReadError(w, err)
		return
	}
	data["Questions"] = questions
	if err = srv.tmpl.Render(w, "daily_review.html", data); err != nil {
		http.Error(w, "渲染每日回顾失败", 500)
	}
}

func (srv *Server) dailyReviewError(w http.ResponseWriter, r *http.Request, err error) {
	code := 400
	if errors.Is(err, store.ErrConflict) {
		code = 409
	}
	if errors.Is(err, store.ErrNotFound) {
		code = 404
	}
	if strings.Contains(r.Header.Get("Accept"), "application/json") {
		http.Error(w, "未保存："+err.Error()+"；解释草稿仍保留，请核对最新版本。", code)
		return
	}
	r.ParseForm()
	type field struct{ Name, Value string }
	var fields []field
	for name, values := range r.PostForm {
		if name != "_csrf" && name != "answer" {
			for _, v := range values {
				fields = append(fields, field{name, v})
			}
		}
	}
	w.WriteHeader(code)
	_ = srv.tmpl.Render(w, "daily_review_error.html", map[string]any{"Error": err.Error(), "CSRF": auth.CSRFValue(r), "Fields": fields, "Answer": r.FormValue("answer"), "Session": r.FormValue("session")})
}

func (srv *Server) handleDailyReviewAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", 405)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	var parseErr error
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		parseErr = r.ParseMultipartForm(64 << 10)
		if r.MultipartForm != nil {
			defer r.MultipartForm.RemoveAll()
		}
	} else {
		parseErr = r.ParseForm()
	}
	if err := parseErr; err != nil {
		srv.dailyReviewError(w, r, err)
		return
	}
	ctx := r.Context()
	action := r.FormValue("action")
	key := r.FormValue("request_key")
	session := r.FormValue("session")
	target := "/review/daily"
	now := srv.reviewNow()
	var err error
	expected, e := strconv.Atoi(r.FormValue("expected_revision"))
	if e != nil && action != "start" && action != "size" && action != "timezone" {
		srv.dailyReviewError(w, r, e)
		return
	}
	switch action {
	case "start":
		size, e := srv.store.GetReviewSessionSize(ctx)
		if r.FormValue("size") != "" {
			size, e = strconv.Atoi(r.FormValue("size"))
		}
		if e != nil {
			err = e
			break
		}
		v, e := srv.store.StartReviewSession(ctx, key, size, now)
		err = e
		if e == nil {
			target += "?session=" + v.ID
		}
	case "size":
		size, e := strconv.Atoi(r.FormValue("size"))
		if e != nil {
			err = e
		} else {
			err = srv.store.SetReviewSessionSize(ctx, size)
		}
	case "timezone":
		prefs, e := srv.store.GetLearningReviewSettings(ctx)
		if e != nil {
			err = e
		} else {
			prefs.Timezone = r.FormValue("timezone")
			err = srv.store.SetLearningReviewSettings(ctx, prefs)
		}
	case "session_end":
		err = srv.store.EndReviewSession(ctx, session, key, expected, now)
		target += "?session=" + url.QueryEscape(session)
	case "answer", "reveal", "later":
		err = srv.store.AnswerReviewSession(ctx, session, r.FormValue("item"), action, r.FormValue("answer"), r.FormValue("assessment"), key, expected, now)
		target += "?session=" + url.QueryEscape(session)
	case "save_note":
		items, e := srv.store.ListReviewSessionItems(ctx, session)
		if e != nil {
			err = e
			break
		}
		err = store.ErrConflict
		for _, item := range items {
			if item.ItemID == r.FormValue("item") && item.State == "answered" && item.Revision == expected {
				_, err = srv.store.SaveLearningReviewAnswerNote(ctx, item.ItemID, item.ItemRevision)
				break
			}
		}
		target += "?session=" + url.QueryEscape(session)
	case "schedule_pause", "schedule_resume", "schedule_end", "schedule_due", "schedule_later", "schedule_question":
		qr, _ := strconv.Atoi(r.FormValue("question_revision"))
		c := store.ReviewScheduleChange{Action: strings.TrimPrefix(action, "schedule_"), LocalDue: r.FormValue("local_due"), QuestionID: r.FormValue("question_id"), QuestionRevision: qr}
		// The selector carries only a stable question identity plus its displayed revision.
		if c.Action == "question" && strings.Contains(c.QuestionID, ":") {
			parts := strings.SplitN(c.QuestionID, ":", 2)
			c.QuestionID = parts[0]
			c.QuestionRevision, _ = strconv.Atoi(parts[1])
		}
		err = srv.store.ChangeReviewSchedule(ctx, r.FormValue("item"), key, expected, c, now)
	default:
		err = fmt.Errorf("未知回顾操作")
	}
	if err != nil {
		srv.dailyReviewError(w, r, err)
		return
	}
	if strings.Contains(r.Header.Get("Accept"), "application/json") {
		voiceJSON(w, map[string]string{"href": target})
		return
	}
	http.Redirect(w, r, target, 303)
}
