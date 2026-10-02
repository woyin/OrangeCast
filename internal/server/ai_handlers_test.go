package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
)

// seedTranscript 写入一个带 2 个 segment 的 transcript 版本。
func seedTranscript(t *testing.T, srv *Server, sourceID string) {
	t.Helper()
	ctx := context.Background()
	job, _ := srv.store.EnqueueJob(ctx, models.SourceEpisode, sourceID, models.JobTranscribe)
	v, _ := srv.store.CreateArtifactVersion(ctx, models.SourceEpisode, sourceID, store.KindTranscript, "groq", "m", "1", job.ID,
		`{"segments":[{"id":"seg-0001","start":0,"end":5,"text":"通胀是物价上升"},{"id":"seg-0002","start":5,"end":10,"text":"购买力下降"}]}`)
	srv.store.SetCurrentVersion(ctx, models.SourceEpisode, sourceID, store.KindTranscript, v)
}

// postForm 发送一个带 CSRF 的 POST（模拟表单提交）。
func postForm(t *testing.T, srv *Server, cookie *http.Cookie, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	// GET 拿 CSRF
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.AddCookie(cookie)
	recCSRF := httptest.NewRecorder()
	srv.Router().ServeHTTP(recCSRF, req)
	csrf := ""
	for _, c := range recCSRF.Result().Cookies() {
		if c.Name == "cwp_csrf" {
			csrf = c.Value
		}
	}
	req2 := httptest.NewRequest(http.MethodPost, path, strings.NewReader("_csrf="+csrf+"&"+body))
	req2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req2.AddCookie(cookie)
	req2.AddCookie(&http.Cookie{Name: "cwp_csrf", Value: csrf})
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req2)
	return rec
}

// TestParaphraseHandler_ReturnsNarration (ADR-0018 R2)
// Paraphrase 成功：返回 AI 讲解 + reference，标注 generated。
func TestParaphraseHandler_ReturnsNarration(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "ph@example.com", "password123")
	ctx := context.Background()
	p, _ := srv.store.CreatePodcast(ctx, "https://f.xml", "Pod", "", "")
	srv.store.MergeEpisodes(ctx, p.ID, []models.Episode{{GUID: "g1", Title: "Ep", AudioURL: "https://a.mp3"}})
	eps, _ := srv.store.ListEpisodes(ctx, p.ID)
	sourceID := eps[0].ID
	seedTranscript(t, srv, sourceID)

	srv.bundleFor = fakeBundleFor(nil,
		&fakeParaphrase{result: &provider.ParaphraseResult{Text: "通胀就是钱越来越不值钱", ReferenceSegmentIDs: []string{"seg-0001"}}},
		nil, nil)

	rec := postForm(t, srv, cookie, "/api/paraphrase",
		"source_type=episode&source_id="+sourceID+"&segment_ids=[\"seg-0001\"]&question=解释一下")
	if rec.Code != http.StatusOK {
		t.Fatalf("Paraphrase 应 200，实际 %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "通胀就是钱越来越不值钱") {
		t.Errorf("应返回 AI 讲解，实际 %s", body)
	}
	if !strings.Contains(body, "AI 讲解") {
		t.Error("应标注 AI 讲解")
	}
	if !strings.Contains(body, "seg-0001") {
		t.Error("应返回 reference segment")
	}
}

// TestParaphraseHandler_NoSegmentRejected
func TestParaphraseHandler_NoSegmentRejected(t *testing.T) {
	srv := newTestServer(t)
	// 未认证也应在参数校验前报错？不，未认证会 401。需要 cookie。
	cookie := claimOwnerAndLogin(t, srv, "ph2@example.com", "password123")
	rec := postForm(t, srv, cookie, "/api/paraphrase", "source_type=episode&source_id=x&segment_ids=&question=q")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("无 segment 应 400，实际 %d", rec.Code)
	}
}

// TestParaphraseHandler_ReferenceNotFound 验证参考片段不在当前转录稿中时 400。
func TestParaphraseHandler_ReferenceNotFound(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "ph3@example.com", "password123")
	ctx := context.Background()
	p, _ := srv.store.CreatePodcast(ctx, "https://f.xml", "Pod", "", "")
	srv.store.MergeEpisodes(ctx, p.ID, []models.Episode{{GUID: "g1", Title: "Ep", AudioURL: "https://a.mp3"}})
	eps, _ := srv.store.ListEpisodes(ctx, p.ID)
	sourceID := eps[0].ID
	seedTranscript(t, srv, sourceID)

	// 请求引用一个 transcript 中不存在的片段
	rec := postForm(t, srv, cookie, "/api/paraphrase",
		"source_type=episode&source_id="+sourceID+"&segment_ids=[\"seg-9999\"]&question=q")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("参考片段不存在应 400，实际 %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "参考片段在当前转录稿中不存在") {
		t.Errorf("应返回参考片段不存在错误，实际 %s", rec.Body.String())
	}
}

// TestParaphraseHandler_ProviderError 验证 Paraphrase Provider 报错时返回 500。
func TestParaphraseHandler_ProviderError(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "pherr@example.com", "password123")
	ctx := context.Background()
	p, _ := srv.store.CreatePodcast(ctx, "https://f.xml", "Pod", "", "")
	srv.store.MergeEpisodes(ctx, p.ID, []models.Episode{{GUID: "g1", Title: "Ep", AudioURL: "https://a.mp3"}})
	eps, _ := srv.store.ListEpisodes(ctx, p.ID)
	sourceID := eps[0].ID
	seedTranscript(t, srv, sourceID)

	srv.bundleFor = fakeBundleFor(nil, &fakeParaphrase{err: errors.New("paraphrase down")}, nil, nil)
	rec := postForm(t, srv, cookie, "/api/paraphrase",
		"source_type=episode&source_id="+sourceID+"&segment_ids=[\"seg-0001\"]&question=解释一下")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("Paraphrase 报错应 500，实际 %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "复述讲解失败") {
		t.Errorf("应返回复述失败错误，实际 %s", rec.Body.String())
	}
}

// TestParaphraseHandler_BundleForError 验证 bundleFor 失败时返回 500。
func TestParaphraseHandler_BundleForError(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "phbundle@example.com", "password123")
	ctx := context.Background()
	p, _ := srv.store.CreatePodcast(ctx, "https://f.xml", "Pod", "", "")
	srv.store.MergeEpisodes(ctx, p.ID, []models.Episode{{GUID: "g1", Title: "Ep", AudioURL: "https://a.mp3"}})
	eps, _ := srv.store.ListEpisodes(ctx, p.ID)
	sourceID := eps[0].ID
	seedTranscript(t, srv, sourceID)

	srv.bundleFor = func(tc provider.TaskConfig) (*provider.ProviderBundle, error) {
		return nil, errors.New("bundle failed")
	}
	rec := postForm(t, srv, cookie, "/api/paraphrase",
		"source_type=episode&source_id="+sourceID+"&segment_ids=[\"seg-0001\"]&question=解释一下")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("bundleFor 失败应 500，实际 %d: %s", rec.Code, rec.Body.String())
	}
}

func TestStudyChatHistory(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "schist@example.com", "password123")
	ctx := context.Background()

	sess, _ := srv.store.CreateStudySession(ctx, models.SourceEpisode, "ep-1", "会话")
	srv.store.AppendStudyMessage(ctx, sess.ID, "user", "通胀是什么", nil, false)
	srv.store.AppendStudyMessage(ctx, sess.ID, "assistant", "通胀是物价上涨", []string{"seg-0001"}, false)

	// 缺 session_id → 400
	req := httptest.NewRequest(http.MethodGet, "/api/study-chat/history", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("缺 session_id 应 400，实际 %d", rec.Code)
	}

	// 正常历史
	req = httptest.NewRequest(http.MethodGet, "/api/study-chat/history?session_id="+sess.ID, nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("历史接口应 200，实际 %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "通胀是物价上涨") {
		t.Errorf("应含 assistant 消息，实际 %s", body)
	}
	if !strings.Contains(body, "seg-0001") {
		t.Errorf("应含 reference_segment_ids，实际 %s", body)
	}
}

// TestStudyChatHistory_DBError 通过删除 study_messages 表（保留 sessions 使认证通过）
// 触发 handleStudyChatHistory 读历史错误分支，返回 500。
func TestStudyChatHistory_DBError(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "schist5@example.com", "password123")
	if _, err := srv.store.DB.Exec(`DROP TABLE study_messages`); err != nil {
		t.Fatalf("DROP TABLE study_messages: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/study-chat/history?session_id=any", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("study_messages 表缺失应 500，实际 %d", rec.Code)
	}
}

func TestEvidenceQA_Handler(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "evidenceqa@example.com", "password123")
	ctx := context.Background()
	p, _ := srv.store.CreatePodcast(ctx, "https://f.xml", "Pod", "", "")
	srv.store.MergeEpisodes(ctx, p.ID, []models.Episode{{GUID: "g1", Title: "Ep", AudioURL: "https://a.mp3"}})
	eps, _ := srv.store.ListEpisodes(ctx, p.ID)
	sourceID := eps[0].ID
	seedTranscript(t, srv, sourceID)

	// 无引用 → 拒答 422
	srv.bundleFor = fakeBundleFor(&fakeQA{result: &provider.QAResult{Answer: "好像是", Sources: nil}}, nil, nil, nil)
	rec := postForm(t, srv, cookie, "/api/evidence-qa",
		"source_type=episode&source_id="+sourceID+"&question=通胀是啥")
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("无引用应 422，实际 %d: %s", rec.Code, rec.Body.String())
	}

	// 有引用 → 200
	srv.bundleFor = fakeBundleFor(&fakeQA{result: &provider.QAResult{
		Answer:  "通胀是物价上涨",
		Sources: []provider.Source{{SegmentID: "seg-0001", Content: "通胀是物价总水平上升", Start: 0, End: 5}},
	}}, nil, nil, nil)
	rec = postForm(t, srv, cookie, "/api/evidence-qa",
		"source_type=episode&source_id="+sourceID+"&question=通胀是啥")
	if rec.Code != http.StatusOK {
		t.Fatalf("有引用应 200，实际 %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "通胀是物价上涨") {
		t.Errorf("应返回答案，实际 %s", rec.Body.String())
	}
}

// TestEvidenceQA_NonPost405 验证非 POST 请求返回 405。
func TestEvidenceQA_NonPost405(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "eqa405@example.com", "password123")
	rec := doWithCookie(srv, cookie, http.MethodGet, "/api/evidence-qa")
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("非 POST 应 405，实际 %d", rec.Code)
	}
}

// TestEvidenceQA_MissingTranscript 验证无转录稿时 EvidenceQA 返回 404（loadTranscriptJSON 拒绝）。
func TestEvidenceQA_MissingTranscript(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "eqamiss@example.com", "password123")
	ctx := context.Background()
	p, _ := srv.store.CreatePodcast(ctx, "https://f.xml", "Pod", "", "")
	srv.store.MergeEpisodes(ctx, p.ID, []models.Episode{{GUID: "g1", Title: "Ep", AudioURL: "https://a.mp3"}})
	eps, _ := srv.store.ListEpisodes(ctx, p.ID)
	// 不 seedTranscript → 无转录稿
	rec := postForm(t, srv, cookie, "/api/evidence-qa",
		"source_type=episode&source_id="+eps[0].ID+"&question=通胀是啥")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("无转录稿应 404，实际 %d: %s", rec.Code, rec.Body.String())
	}
}

// TestEvidenceQA_AnswerError 验证 QA Provider 报错时返回 500。
func TestEvidenceQA_AnswerError(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "eqaerr@example.com", "password123")
	ctx := context.Background()
	p, _ := srv.store.CreatePodcast(ctx, "https://f.xml", "Pod", "", "")
	srv.store.MergeEpisodes(ctx, p.ID, []models.Episode{{GUID: "g1", Title: "Ep", AudioURL: "https://a.mp3"}})
	eps, _ := srv.store.ListEpisodes(ctx, p.ID)
	sourceID := eps[0].ID
	seedTranscript(t, srv, sourceID)

	srv.bundleFor = fakeBundleFor(&fakeQA{err: errors.New("qa down")}, nil, nil, nil)
	rec := postForm(t, srv, cookie, "/api/evidence-qa",
		"source_type=episode&source_id="+sourceID+"&question=通胀是啥")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("QA 报错应 500，实际 %d: %s", rec.Code, rec.Body.String())
	}
}

// TestEvidenceQA_BundleForError 验证 bundleFor 失败时返回 500。
func TestEvidenceQA_BundleForError(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "eqabundle@example.com", "password123")
	ctx := context.Background()
	p, _ := srv.store.CreatePodcast(ctx, "https://f.xml", "Pod", "", "")
	srv.store.MergeEpisodes(ctx, p.ID, []models.Episode{{GUID: "g1", Title: "Ep", AudioURL: "https://a.mp3"}})
	eps, _ := srv.store.ListEpisodes(ctx, p.ID)
	sourceID := eps[0].ID
	seedTranscript(t, srv, sourceID)

	srv.bundleFor = func(tc provider.TaskConfig) (*provider.ProviderBundle, error) {
		return nil, errors.New("bundle failed")
	}
	rec := postForm(t, srv, cookie, "/api/evidence-qa",
		"source_type=episode&source_id="+sourceID+"&question=通胀是啥")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("bundleFor 失败应 500，实际 %d: %s", rec.Code, rec.Body.String())
	}
}

// TestTaskConfigFrom 验证 taskConfigFrom 从 settings 指针构建 TaskConfig：
// Provider 空指针回退 "groq"，显式 Provider 保留，Model 空指针得空串。
func TestTaskConfigFrom(t *testing.T) {
	// 显式 Provider + Model
	providerName := "openai"
	modelName := "gpt-4o"
	tc := taskConfigFrom(&providerName, &modelName)
	if tc.Provider != "openai" || tc.Model != "gpt-4o" {
		t.Fatalf("显式配置应保留，实际 %+v", tc)
	}

	// Provider 空指针 → 回退 groq
	tc = taskConfigFrom(nil, nil)
	if tc.Provider != "groq" {
		t.Fatalf("空 Provider 应回退 groq，实际 %q", tc.Provider)
	}
	if tc.Model != "" {
		t.Fatalf("空 Model 应得空串，实际 %q", tc.Model)
	}

	// Provider 为空字符串 → 也回退 groq
	empty := ""
	tc = taskConfigFrom(&empty, &empty)
	if tc.Provider != "groq" {
		t.Fatalf("空串 Provider 应回退 groq，实际 %q", tc.Provider)
	}
}

func TestEditorialTaskConfig_OverridesAndFallsBack(t *testing.T) {
	analysisProvider, analysisModel := "groq", "llama-3.3-70b-versatile"
	writerProvider, writerModel := "openai", "gpt-5"
	settings := &models.Settings{
		AnalysisProvider: &analysisProvider, AnalysisModel: &analysisModel,
		WriterProvider: &writerProvider, WriterModel: &writerModel,
	}
	if got := editorialTaskConfig(settings, editorialRoleWriter); got.Provider != writerProvider || got.Model != writerModel {
		t.Fatalf("Writer 应使用独立覆盖，实际 %+v", got)
	}
	if got := editorialTaskConfig(settings, editorialRoleEvidence); got.Provider != analysisProvider || got.Model != analysisModel {
		t.Fatalf("未配置的角色应回退分析配置，实际 %+v", got)
	}
	settings.WriterModel = nil
	if got := editorialTaskConfig(settings, editorialRoleWriter); got.Provider != writerProvider || got.Model != analysisModel {
		t.Fatalf("角色应支持仅覆盖 Provider，实际 %+v", got)
	}
}

// TestLoadTranscriptJSON 验证 loadTranscriptJSON 的三种分支：
// 正常解析、无转录稿（404）、载荷损坏（500）。
func TestLoadTranscriptJSON(t *testing.T) {
	srv := newTestServer(t)
	ctx := context.Background()

	// 无转录稿 → 404
	rec := httptest.NewRecorder()
	_, ok := srv.loadTranscriptJSON(rec, ctx, models.SourceEpisode, "missing")
	if ok {
		t.Fatal("缺转录稿应返回 false")
	}
	if rec.Code != http.StatusNotFound {
		t.Fatalf("缺转录稿应 404，实际 %d", rec.Code)
	}

	// 建一个 episode 作为 source
	p, _ := srv.store.CreatePodcast(ctx, "https://f.xml", "Pod", "", "")
	srv.store.MergeEpisodes(ctx, p.ID, []models.Episode{{GUID: "g1", Title: "Ep", AudioURL: "https://a.mp3"}})
	eps, _ := srv.store.ListEpisodes(ctx, p.ID)
	sourceID := eps[0].ID

	// 载荷损坏 → 500
	job, _ := srv.store.EnqueueJob(ctx, models.SourceEpisode, sourceID, models.JobTranscribe)
	v1, err := srv.store.CreateArtifactVersion(ctx, models.SourceEpisode, sourceID, store.KindTranscript, "groq", "m", "1", job.ID, `{bad json`)
	if err != nil {
		t.Fatalf("创建版本失败: %v", err)
	}
	if err := srv.store.SetCurrentVersion(ctx, models.SourceEpisode, sourceID, store.KindTranscript, v1); err != nil {
		t.Fatalf("设置当前版本失败: %v", err)
	}
	rec = httptest.NewRecorder()
	_, ok = srv.loadTranscriptJSON(rec, ctx, models.SourceEpisode, sourceID)
	if ok {
		t.Fatal("载荷损坏应返回 false")
	}
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("载荷损坏应 500，实际 %d", rec.Code)
	}

	// 正常解析 → true
	v2, err := srv.store.CreateArtifactVersion(ctx, models.SourceEpisode, sourceID, store.KindTranscript, "groq", "m", "2", job.ID,
		`{"segments":[{"id":"seg-0001","start":0,"end":5,"text":"通胀是物价上升"}]}`)
	if err != nil {
		t.Fatalf("创建版本失败: %v", err)
	}
	if err := srv.store.SetCurrentVersion(ctx, models.SourceEpisode, sourceID, store.KindTranscript, v2); err != nil {
		t.Fatalf("设置当前版本失败: %v", err)
	}
	rec = httptest.NewRecorder()
	tp, ok := srv.loadTranscriptJSON(rec, ctx, models.SourceEpisode, sourceID)
	if !ok {
		t.Fatal("正常载荷应返回 true")
	}
	if len(tp.Segments) != 1 || tp.Segments[0].ID != "seg-0001" {
		t.Fatalf("应解析出 1 个 segment，实际 %d 个", len(tp.Segments))
	}
}

func TestParaphraseHandler_PersistError(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "phpersist@example.com", "password123")
	ctx := context.Background()
	p, _ := srv.store.CreatePodcast(ctx, "https://f.xml", "Pod", "", "")
	srv.store.MergeEpisodes(ctx, p.ID, []models.Episode{{GUID: "g1", Title: "Ep", AudioURL: "https://a.mp3"}})
	eps, _ := srv.store.ListEpisodes(ctx, p.ID)
	sourceID := eps[0].ID
	seedTranscript(t, srv, sourceID)
	// 删除 paraphrases 表 → CreateParaphrase 写入失败
	if _, err := srv.store.DB.Exec(`DROP TABLE paraphrases`); err != nil {
		t.Fatalf("DROP TABLE paraphrases: %v", err)
	}

	srv.bundleFor = fakeBundleFor(nil,
		&fakeParaphrase{result: &provider.ParaphraseResult{Text: "讲解", ReferenceSegmentIDs: []string{"seg-0001"}}},
		nil, nil)
	rec := postForm(t, srv, cookie, "/api/paraphrase",
		"source_type=episode&source_id="+sourceID+"&segment_ids=[\"seg-0001\"]&question=解释一下")
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("持久化失败应 500，实际 %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "持久化复述讲解失败") {
		t.Errorf("应提示持久化复述讲解失败，实际 %s", rec.Body.String())
	}
}

func TestParaphraseHandler_MissingTranscript(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "phmt@example.com", "password123")
	ctx := context.Background()
	p, _ := srv.store.CreatePodcast(ctx, "https://f.xml", "Pod", "", "")
	srv.store.MergeEpisodes(ctx, p.ID, []models.Episode{{GUID: "g1", Title: "Ep", AudioURL: "https://a.mp3"}})
	eps, _ := srv.store.ListEpisodes(ctx, p.ID)
	// 不 seedTranscript → 无转录稿
	rec := postForm(t, srv, cookie, "/api/paraphrase",
		"source_type=episode&source_id="+eps[0].ID+"&segment_ids=[\"seg-0001\"]&question=解释一下")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("无转录稿应 404，实际 %d: %s", rec.Code, rec.Body.String())
	}
}
