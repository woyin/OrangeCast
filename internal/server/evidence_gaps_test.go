package server

import (
	"github.com/woyin/orangecast/internal/store"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestEvidenceGapHTTPReadOnlyExplicitFindAndCAS(t *testing.T) {
	srv := newTestServer(t)
	q, err := srv.store.CreateLearningQuestion(t.Context(), store.LearningQuestion{Body: "主动回忆"})
	if err != nil {
		t.Fatal(err)
	}
	path := "/questions/" + q.ID + "/gaps"
	get := httptest.NewRecorder()
	srv.handleEvidenceGaps(get, httptest.NewRequest("GET", path, nil))
	if get.Code != 200 || !strings.Contains(get.Body.String(), "没有记录缺口") || get.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(get.Code, get.Body.String())
	}
	command := func(v url.Values) *httptest.ResponseRecorder {
		v.Set("expected_revision", strconv.Itoa(q.Revision))
		r := httptest.NewRequest("POST", path, strings.NewReader(v.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		srv.handleEvidenceGaps(w, r)
		return w
	}
	if w := command(url.Values{"action": {"create"}, "kind": {"practice"}, "explanation": {"主动回忆"}}); w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	gaps, err := srv.store.ListEvidenceGaps(t.Context(), "question", q.ID)
	if err != nil || len(gaps) != 1 {
		t.Fatal(gaps, err)
	}
	w := command(url.Values{"action": {"find"}, "gap_id": {gaps[0].ID}, "query": {"主动回忆"}})
	if w.Code != 200 || !strings.Contains(w.Body.String(), "未检验全库") || !strings.Contains(w.Body.String(), "未命中候选") {
		t.Fatal(w.Code, w.Body.String())
	}
	w = command(url.Values{"action": {"disposition"}, "gap_id": {gaps[0].ID}, "gap_revision": {"0"}, "state": {"ignored"}, "comment": {"SSR冲突保留的私有判断"}})
	if w.Code != 409 || !strings.Contains(w.Body.String(), "SSR冲突保留的私有判断") {
		t.Fatal(w.Code, w.Body.String())
	}
	var jobs int
	srv.store.DB.QueryRow(`SELECT count(*) FROM processing_jobs`).Scan(&jobs)
	if jobs != 0 {
		t.Fatal(jobs)
	}
}

func TestEvidenceGapDraftBehavior(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal("Node required for actual gap draft behavior")
	}
	if out, err := exec.Command(node, filepath.Join("testdata", "evidence-gaps.cjs")).CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
}

func TestEvidenceGapActualRouterPrivacyAndCSRFFallback(t *testing.T) {
	srv := newTestServer(t)
	ctx := t.Context()
	q, err := srv.store.CreateLearningQuestion(ctx, store.LearningQuestion{Body: "范围入口"})
	if err != nil {
		t.Fatal(err)
	}
	path := "/questions/" + q.ID + "/gaps"
	if w := doWithCookie(srv, nil, "GET", path); w.Code != 303 {
		t.Fatal("private gap route leaked", w.Code, w.Body.String())
	}
	cookie := claimOwnerAndLogin(t, srv, "gap-owner@example.com", "password123")
	if w := doWithCookie(srv, cookie, "GET", path); w.Code != 200 || !strings.Contains(w.Body.String(), "材料缺口与补听") {
		t.Fatal(w.Code, w.Body.String())
	}
	r := httptest.NewRequest("POST", path, strings.NewReader("action=create&expected_revision=1&kind=example&explanation=保留文字"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.Router().ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("missing CSRF accepted", w.Code, w.Body.String())
	}
	w = postForm(t, srv, cookie, path, url.Values{"action": {"create"}, "expected_revision": {"1"}, "kind": {"example"}, "explanation": {"明确Owner缺口"}}.Encode())
	if w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = doWithCookie(srv, cookie, "GET", path)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "明确Owner缺口") {
		t.Fatal(w.Code, w.Body.String())
	}
	var jobs int
	srv.store.DB.QueryRow(`SELECT count(*) FROM processing_jobs`).Scan(&jobs)
	if jobs != 0 {
		t.Fatal(jobs)
	}
}

func TestEvidenceGapHTTPUnderstandingAndSemanticFallback(t *testing.T) {
	srv := newTestServer(t)
	ctx := t.Context()
	q, err := srv.store.CreateLearningQuestion(ctx, store.LearningQuestion{Body: "集成理解"})
	if err != nil {
		t.Fatal(err)
	}
	u, err := srv.store.SaveUnderstanding(ctx, store.SaveUnderstandingCommand{QuestionID: q.ID, QuestionRevision: q.Revision, RequestKey: "a8a74e5b-809e-4e42-8486-819ef7ead40a", Answer: "自主实践条件"})
	if err != nil {
		t.Fatal(err)
	}
	if err = srv.store.ChooseCurrentUnderstanding(ctx, q.ID, u.ID, q.Revision, 1, "71829288-4422-4569-8da3-10c7688d0730"); err != nil {
		t.Fatal(err)
	}
	gap, err := srv.store.CreateEvidenceGap(ctx, "question", q.ID, q.Revision, "practice", "owner", "自主实践条件", "")
	if err != nil {
		t.Fatal(err)
	}
	path := "/questions/" + q.ID + "/gaps"
	v := url.Values{"action": {"find"}, "gap_id": {gap.ID}, "query": {"自主实践条件"}, "expected_revision": {strconv.Itoa(q.Revision)}, "semantic": {"1"}, "config_id": {"00000000-0000-0000-0000-000000000000"}}
	post := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", path, strings.NewReader(v.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		srv.handleEvidenceGaps(w, r)
		return w
	}
	w := post()
	if w.Code != 200 || !strings.Contains(w.Body.String(), "本次使用FTS") || !strings.Contains(w.Body.String(), "retrieval_method") || !strings.Contains(w.Body.String(), "understanding:"+u.ID) {
		t.Fatal(w.Code, w.Body.String())
	}
	v.Set("action", "confirm")
	v.Set("candidate_key", "understanding:"+u.ID)
	v.Set("retrieval_method", "hybrid")
	if w = post(); w.Code != 409 {
		t.Fatal(w.Code, w.Body.String())
	}
	v.Set("retrieval_method", "fts")
	if w = post(); w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	var jobs int
	srv.store.DB.QueryRowContext(ctx, `SELECT count(*) FROM processing_jobs`).Scan(&jobs)
	if jobs != 0 {
		t.Fatal(jobs)
	}
}
