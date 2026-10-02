package server

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
)

func TestUnderstandingHTTPRejectsMalformedCommandsWithoutSaving(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "understanding-boundaries@example.com", "password123")
	q, e := srv.store.CreateLearningQuestion(t.Context(), store.LearningQuestion{Body: "HTTP输入边界"})
	if e != nil {
		t.Fatal(e)
	}
	base := url.Values{"action": {"save"}, "question_id": {q.ID}, "question_revision": {"1"}, "head_revision": {"0"}, "request_key": {uuid.NewString()}, "owner_confirmed": {"yes"}, "answer": {"不能被无效请求保存"}}
	for _, tc := range []struct {
		name, key, value string
		code             int
	}{
		{"question revision", "question_revision", "bad", 400}, {"head revision", "head_revision", "bad", 400},
		{"unknown action", "action", "publish", 400}, {"invalid reference JSON", "reference", "{", 400},
		{"invalid policy", "model_data_policy", "unknown", 400}, {"missing question", "question_id", uuid.NewString(), 404},
		{"missing chosen snapshot", "action", "choose", 409},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := url.Values{}
			for k, vs := range base {
				v[k] = append([]string(nil), vs...)
			}
			v.Set(tc.key, tc.value)
			if tc.value == "choose" {
				v.Set("snapshot_id", uuid.NewString())
			}
			rec := postForm(t, srv, cookie, "/questions/understanding-action", v.Encode())
			if rec.Code != tc.code {
				t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
			}
		})
	}
	rec := postForm(t, srv, cookie, "/questions/understanding-action", "answer="+strings.Repeat("x", 129<<10))
	if rec.Code != 400 {
		t.Fatal(rec.Code)
	}
	h, e := srv.store.HistoryUnderstanding(t.Context(), q.ID, 0, 20)
	if e != nil || len(h) != 0 {
		t.Fatal(h, e)
	}
	if rec := doWithCookie(srv, cookie, "GET", "/questions/"+uuid.NewString()+"/understandings"); rec.Code != 404 {
		t.Fatal(rec.Code)
	}
	direct := httptest.NewRecorder()
	srv.handleQuestionUnderstandings(direct, httptest.NewRequest("POST", "/questions/"+q.ID+"/understandings", nil))
	if direct.Code != 405 {
		t.Fatal(direct.Code)
	}
}

func TestUnderstandingHTTPExactReferenceOptionsPaginationAndPurge(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "understanding-options@example.com", "password123")
	ep, noteID := seedKnowledgeLearning(t, srv)
	q, e := srv.store.CreateLearningQuestion(t.Context(), store.LearningQuestion{Body: "引用选择和历史分页"})
	if e != nil {
		t.Fatal(e)
	}
	for _, link := range []provider.LearningQuestionLink{{Kind: "note", ObjectID: noteID}, {Kind: "source", SourceType: "episode", SourceID: ep}} {
		q, e = srv.store.ChangeLearningQuestion(t.Context(), q.ID, q.Revision, store.LearningQuestionChange{Action: "link", Link: link})
		if e != nil {
			t.Fatal(e)
		}
	}
	n, e := srv.store.GetOwnerNote(t.Context(), noteID)
	if e != nil {
		t.Fatal(e)
	}
	ref, _ := json.Marshal(store.UnderstandingReference{Kind: "note", ObjectID: noteID, Version: n.Revision})
	values := url.Values{"action": {"save"}, "question_id": {q.ID}, "question_revision": {fmt.Sprint(q.Revision)}, "head_revision": {"0"}, "request_key": {uuid.NewString()}, "owner_confirmed": {"yes"}, "answer": {"精确引用保存"}, "reference": {string(ref)}}
	rec := postForm(t, srv, cookie, "/questions/understanding-action", values.Encode())
	if rec.Code != 303 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	for i := 1; i < 21; i++ {
		_, e = srv.store.SaveUnderstanding(t.Context(), store.SaveUnderstandingCommand{QuestionID: q.ID, QuestionRevision: q.Revision, HeadRevision: i, RequestKey: uuid.NewString(), Answer: fmt.Sprintf("历史答案-%d", i)})
		if e != nil {
			t.Fatal(e)
		}
	}
	path := "/questions/" + q.ID + "/understandings"
	page := doWithCookie(srv, cookie, "GET", path)
	if page.Code != 200 || !strings.Contains(page.Body.String(), "?before=2") || !strings.Contains(page.Body.String(), "导入此笔记") || !strings.Contains(page.Body.String(), ep) {
		t.Fatal(page.Code, page.Body.String())
	}
	older := doWithCookie(srv, cookie, "GET", path+"?before=2")
	if older.Code != 200 || !strings.Contains(older.Body.String(), "精确引用保存") || strings.Contains(older.Body.String(), "历史答案-20") {
		t.Fatal(older.Code, older.Body.String())
	}
	if _, e = srv.store.DB.Exec(`DELETE FROM episodes WHERE id=?`, ep); e != nil {
		t.Fatal(e)
	}
	page = doWithCookie(srv, cookie, "GET", path+"?before=2")
	if page.Code != 200 || !strings.Contains(page.Body.String(), "来源清理，参考正文移除") || !strings.Contains(page.Body.String(), "精确引用保存") {
		t.Fatal(page.Code, page.Body.String())
	}
}

// Incomplete restores must produce an error response rather than an empty successful editor.
func TestUnderstandingHTTPIncompleteRestoreNeverShowsSuccessfulEditor(t *testing.T) {
	for _, table := range []string{"understanding_heads", "understanding_snapshots", "learning_question_links"} {
		t.Run(table, func(t *testing.T) {
			srv := newTestServer(t)
			q, e := srv.store.CreateLearningQuestion(t.Context(), store.LearningQuestion{Body: "不完整恢复"})
			if e != nil {
				t.Fatal(e)
			}
			if _, e = srv.store.DB.Exec("ALTER TABLE " + table + " RENAME TO unavailable_" + table); e != nil {
				t.Fatal(e)
			}
			rec := httptest.NewRecorder()
			srv.handleQuestionUnderstandings(rec, httptest.NewRequest("GET", "/questions/"+q.ID+"/understandings", nil))
			if rec.Code != 400 || strings.Contains(rec.Body.String(), "保存不可变的新版本") {
				t.Fatal(rec.Code, rec.Body.String())
			}
		})
	}
	t.Run("missing current snapshot", func(t *testing.T) {
		srv := newTestServer(t)
		q, e := srv.store.CreateLearningQuestion(t.Context(), store.LearningQuestion{Body: "缺失当前版本"})
		if e != nil {
			t.Fatal(e)
		}
		v, e := srv.store.SaveUnderstanding(t.Context(), store.SaveUnderstandingCommand{QuestionID: q.ID, QuestionRevision: q.Revision, RequestKey: uuid.NewString(), Answer: "仍在历史中"})
		if e != nil {
			t.Fatal(e)
		}
		if e = srv.store.ChooseCurrentUnderstanding(t.Context(), q.ID, v.ID, q.Revision, 1, uuid.NewString()); e != nil {
			t.Fatal(e)
		}
		if _, e = srv.store.DB.Exec(`UPDATE understanding_heads SET current_snapshot_id=? WHERE question_id=?`, uuid.NewString(), q.ID); e != nil {
			t.Fatal(e)
		}
		rec := httptest.NewRecorder()
		srv.handleQuestionUnderstandings(rec, httptest.NewRequest("GET", "/questions/"+q.ID+"/understandings", nil))
		if rec.Code != 404 {
			t.Fatal(rec.Code, rec.Body.String())
		}
	})
	t.Run("unavailable page template", func(t *testing.T) {
		srv := newTestServer(t)
		q, e := srv.store.CreateLearningQuestion(t.Context(), store.LearningQuestion{Body: "缺失页面"})
		if e != nil {
			t.Fatal(e)
		}
		delete(srv.tmpl.pages, "question_understandings.html")
		rec := httptest.NewRecorder()
		srv.handleQuestionUnderstandings(rec, httptest.NewRequest("GET", "/questions/"+q.ID+"/understandings", nil))
		if rec.Code != 500 || !strings.Contains(rec.Body.String(), "渲染理解快照失败") {
			t.Fatal(rec.Code, rec.Body.String())
		}
	})
	t.Run("body exceeds handler limit", func(t *testing.T) {
		srv := newTestServer(t)
		req := httptest.NewRequest("POST", "/questions/understanding-action", strings.NewReader("answer="+strings.Repeat("x", 129<<10)))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		srv.handleUnderstandingAction(rec, req)
		if rec.Code != 400 || !strings.Contains(rec.Body.String(), "表单过大或无效") {
			t.Fatal(rec.Code, rec.Body.String())
		}
	})
}
