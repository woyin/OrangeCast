package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
)

func TestKnowledgeWritingPurposeFormsRetainInvalidInput(t *testing.T) {
	srv := newTestServer(t)
	session := claimOwnerAndLogin(t, srv, "writing@example.com", "password123")
	rec := postForm(t, srv, session, "/knowledge-articles/settings", "daily_limit=2&debounce_minutes=0&audience=Owner&style=clear&writing_mode=practice&preview_writing_plan=on")
	if rec.Code != 303 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	prefs, e := srv.store.GetKnowledgeArticleSettings(t.Context())
	if e != nil || prefs.WritingMode != "practice" || !prefs.PreviewWritingPlan {
		t.Fatal(prefs, e)
	}
	rec = postForm(t, srv, session, "/knowledge-articles/settings", "daily_limit=2&debounce_minutes=0&audience=Owner&style=legacy")
	if rec.Code != 303 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	prefs, e = srv.store.GetKnowledgeArticleSettings(t.Context())
	if e != nil || prefs.WritingMode != "practice" || !prefs.PreviewWritingPlan {
		t.Fatal("legacy form reset new preferences", prefs, e)
	}
	rec = postForm(t, srv, session, "/knowledge-articles/settings", "daily_limit=invalid&debounce_minutes=0&audience=draft-reader&style=draft-style&writing_mode=comparison&preview_writing_plan=on")
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), `value="comparison" selected`) || !strings.Contains(rec.Body.String(), "draft-style") {
		t.Fatal(rec.Code, rec.Body.String())
	}
	req := httptest.NewRequest("GET", "/knowledge-articles", nil)
	req.AddCookie(session)
	rec = httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "先预览并确认写作计划") {
		t.Fatal(rec.Code, rec.Body.String())
	}
}

func TestKnowledgeWritingPlanHTTPReadEditConflictAndConfirm(t *testing.T) {
	srv := newTestServer(t)
	session := claimOwnerAndLogin(t, srv, "plan@example.com", "password123")
	seedKnowledgeLearning(t, srv)
	srv.cfg.PodBaseURL, srv.cfg.PodAPIKey, srv.cfg.PodModel = "https://example.test/v1", "test", "fixture"
	profile, e := srv.store.EnsureDefaultEditorialProfile(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	a, _, e := srv.enqueueKnowledgeArticle(t.Context(), profile.ID, false)
	if e != nil {
		t.Fatal(e)
	}
	var frozen provider.KnowledgeArticleRequest
	if e = json.Unmarshal([]byte(a.InputJSON), &frozen); e != nil {
		t.Fatal(e)
	}
	frozen.Topic = &provider.KnowledgeTopic{Title: "计划标题", Question: "如何区分来源？", Thesis: "明确身份边界", Outline: "原计划", MaterialIDs: []string{frozen.Materials[0].ID, frozen.Materials[1].ID}}
	frozen.Stage = "write"
	body, _ := json.Marshal(frozen)
	sum := sha256.Sum256(body)
	initialHash := hex.EncodeToString(sum[:])
	if _, e = srv.store.DB.ExecContext(t.Context(), `INSERT INTO knowledge_writing_plans(article_id,parent_revision,revision,hash,request_json,provider,model) VALUES(?,0,1,?,?,'pod','fixture')`, a.ID, initialHash, string(body)); e != nil {
		t.Fatal(e)
	}
	if _, e = srv.store.DB.ExecContext(t.Context(), `UPDATE knowledge_articles SET status='awaiting_plan' WHERE id=?`, a.ID); e != nil {
		t.Fatal(e)
	}
	req := httptest.NewRequest("GET", "/knowledge-articles/"+a.ID, nil)
	req.AddCookie(session)
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "原计划") || !strings.Contains(rec.Body.String(), "等待确认") {
		t.Fatal(rec.Code, rec.Body.String())
	}
	post := func(action, hash, revision, outline string) *httptest.ResponseRecorder {
		return postForm(t, srv, session, "/knowledge-articles/action", url.Values{"article_id": {a.ID}, "action": {action}, "plan_hash": {hash}, "expected_revision": {revision}, "outline": {outline}}.Encode())
	}
	rec = post("plan_confirm", initialHash, "1", "未保存草稿")
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "未保存草稿") {
		t.Fatal(rec.Code, rec.Body.String())
	}
	rec = post("plan_save", initialHash, "1", "新计划")
	if rec.Code != 303 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Location") != "/knowledge-articles/"+a.ID {
		t.Fatal("plan redirect must not select nonexistent body v0", rec.Header().Get("Location"))
	}
	plan, e := srv.store.GetKnowledgeWritingPlan(t.Context(), a.ID)
	if e != nil || plan.Request.Topic.Outline != "新计划" {
		t.Fatal(plan, e)
	}
	rec = post("plan_confirm", initialHash, "1", "新计划")
	if rec.Code != 409 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	rec = post("plan_confirm", plan.Hash, "2", "新计划")
	if rec.Code != 303 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	plan, e = srv.store.GetKnowledgeWritingPlan(t.Context(), a.ID)
	if e != nil || plan.State != "confirmed" || plan.JobID == "" {
		t.Fatal(plan, e)
	}
	execution, e := srv.store.GetJobExecution(t.Context(), plan.JobID)
	if e != nil || execution.RemoteCallStarted {
		t.Fatal("HTTP must not call model", execution, e)
	}
}

func TestKnowledgeWritingPlanBrowserDraftGuard(t *testing.T) {
	node, e := exec.LookPath("node")
	if e != nil {
		t.Skip("node unavailable")
	}
	cmd := exec.Command(node, "testdata/knowledge-writing-ui-test.js")
	cmd.Env = os.Environ()
	if out, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("%v: %s", e, out)
	}
}

func TestKnowledgeWritingPurposeHistoricalViewKeepsSelectedRules(t *testing.T) {
	srv := newTestServer(t)
	session := claimOwnerAndLogin(t, srv, "historical-writing@example.com", "password123")
	seedKnowledgeLearning(t, srv)
	srv.cfg.PodBaseURL, srv.cfg.PodAPIKey, srv.cfg.PodModel = "https://fixture.invalid/v1", "fixture", "fixture"
	srv.worker.WithBundleResolver(func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{KnowledgeArticle: &knowledgeStageFake{}}, nil
	})
	profile, err := srv.store.EnsureDefaultEditorialProfile(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	article, _, err := srv.enqueueKnowledgeArticle(t.Context(), profile.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		if err = srv.worker.ProcessOne(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	article, err = srv.store.GetKnowledgeArticle(t.Context(), article.ID)
	if err != nil || article.PassedRevision != 1 {
		t.Fatal(article, err)
	}
	if err = srv.store.QueueKnowledgePurposeRevision(t.Context(), article.ID, 1, "practice", "", "fixture"); err != nil {
		t.Fatal(err)
	}
	response := doWithCookie(srv, session, "GET", "/knowledge-articles/"+article.ID+"?revision=1")
	body := response.Body.String()
	if response.Code != 200 || !strings.Contains(body, "冻结文章用途：默认综合") || !strings.Contains(body, `value="practice" selected`) || !strings.Contains(body, "当前阅读 v1") {
		t.Fatal("historical request aliased current purpose", response.Code, body)
	}
}
