package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/store"
)

// seedSnapshotSource 认领登录 + 已转录单集 + 原音，返回 (srv, session, episodeID)。
func seedSnapshotSource(t *testing.T) (*Server, *http.Cookie, string) {
	t.Helper()
	srv := newTestServer(t)
	session := claimOwnerAndLogin(t, srv, "snap@t.local", "password123")
	podcast, err := srv.store.CreatePodcast(t.Context(), "https://feed.example.com/snapapi.xml", "快照播客", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.store.MergeEpisodes(t.Context(), podcast.ID, []models.Episode{{GUID: "snap-api-1", Title: "快照接口单集", AudioURL: "https://cdn.example.com/s.mp3"}}); err != nil {
		t.Fatal(err)
	}
	eps, err := srv.store.ListEpisodes(t.Context(), podcast.ID)
	if err != nil || len(eps) != 1 {
		t.Fatalf("单集 setup: %v", err)
	}
	epID := eps[0].ID
	job, err := srv.store.EnqueueJob(t.Context(), models.SourceEpisode, epID, models.JobTranscribe)
	if err != nil {
		t.Fatal(err)
	}
	payload := `{"language":"zh","text":"t","segments":[{"id":"seg-0001","start":3,"end":9,"text":"接口验收要点"}]}`
	version, err := srv.store.CreateArtifactVersion(t.Context(), models.SourceEpisode, epID, store.KindTranscript, "fake", "m", "1", job.ID, payload)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.store.MarkJobRunning(t.Context(), job.ID); err != nil {
		t.Fatal(err)
	}
	if err := srv.store.MarkJobSucceeded(t.Context(), job.ID); err != nil {
		t.Fatal(err)
	}
	if err := srv.store.UpdateEpisodeStatus(t.Context(), epID, models.StatusProcessed); err != nil {
		t.Fatal(err)
	}
	if err := srv.store.SetCurrentVersion(t.Context(), models.SourceEpisode, epID, store.KindTranscript, version); err != nil {
		t.Fatal(err)
	}
	if err := srv.store.UpsertEvidenceAudio(t.Context(), models.SourceEpisode, epID, "evidence/s.mp3", "mp3", 88, "sha-api"); err != nil {
		t.Fatal(err)
	}
	return srv, session, epID
}

// TestSourceSnapshotAPI_Audio 音频快照端点：Segment 带秒数 + 原音可回听。
func TestSourceSnapshotAPI_Audio(t *testing.T) {
	srv, session, epID := seedSnapshotSource(t)
	snap, err := srv.store.FreezeSourceSnapshot(t.Context(), models.SourceEpisode, epID)
	if err != nil {
		t.Fatal(err)
	}
	rec := doWithCookie(srv, session, http.MethodGet, "/api/source-snapshots/"+snap.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("应 200：%d %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{`"start":3`, `"end":9`, "接口验收要点", `"Status":"playable"`, "快照接口单集"} {
		if !strings.Contains(body, want) {
			t.Fatalf("音频快照缺少 %q：%s", want, body)
		}
	}
}

// TestSourceSnapshotAPI_Document 文档快照返回段落位置而非秒数。
func TestSourceSnapshotAPI_Document(t *testing.T) {
	srv, session, _ := seedSnapshotSource(t)
	doc, err := srv.store.CreatePastedDocument(t.Context(), "接口文档", "段落一。\n\n段落二。")
	if err != nil {
		t.Fatal(err)
	}
	snap, err := srv.store.FreezeSourceSnapshot(t.Context(), models.SourceDocument, doc.ID)
	if err != nil {
		t.Fatal(err)
	}
	rec := doWithCookie(srv, session, http.MethodGet, "/api/source-snapshots/"+snap.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("应 200：%d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"document_segments"`) || !strings.Contains(body, `"Position":2`) {
		t.Fatalf("文档快照应含段落位置: %s", body)
	}
	if strings.Contains(body, `"audio"`) {
		t.Fatalf("文档快照不应有原音身份: %s", body)
	}
	var parsed struct {
		AudioSegments []map[string]any `json:"audio_segments"`
	}
	if err := json.Unmarshal([]byte(body), &parsed); err != nil {
		t.Fatal(err)
	}
	if len(parsed.AudioSegments) != 0 {
		t.Fatalf("文档快照不应返回音频秒数段: %+v", parsed.AudioSegments)
	}
}

// TestSourceSnapshotAPI_Invalidation 未知快照 404；Purge 后 410 明确失效。
func TestSourceSnapshotAPI_Invalidation(t *testing.T) {
	srv, session, epID := seedSnapshotSource(t)
	snap, err := srv.store.FreezeSourceSnapshot(t.Context(), models.SourceEpisode, epID)
	if err != nil {
		t.Fatal(err)
	}
	if rec := doWithCookie(srv, session, http.MethodGet, "/api/source-snapshots/missing"); rec.Code != http.StatusNotFound {
		t.Fatalf("未知快照应 404：%d", rec.Code)
	}
	if err := srv.store.MarkSourceSnapshotsPurged(t.Context(), models.SourceEpisode, epID); err != nil {
		t.Fatal(err)
	}
	if rec := doWithCookie(srv, session, http.MethodGet, "/api/source-snapshots/"+snap.ID); rec.Code != http.StatusGone {
		t.Fatalf("Purge 后应 410 明确失效：%d", rec.Code)
	}
}

// TestSourceDetail_ShowsStagesAndRetry B09：单集页显示分阶段进度；
// 失败阶段带原因与重试按钮；GET 只读不入队。
func TestSourceDetail_ShowsStagesAndRetry(t *testing.T) {
	srv, session, epID := seedSnapshotSource(t)
	ctx := t.Context()
	// 卡片就绪
	job, _, err := srv.store.EnqueueJobIdempotent(ctx, store.JobIntentSpec{
		SourceType: models.SourceEpisode, SourceID: epID, JobType: models.JobAnalyze, IntentID: "an-api",
	})
	if err != nil {
		t.Fatal(err)
	}
	version, err := srv.store.CreateArtifactVersion(ctx, models.SourceEpisode, epID, store.KindKnowledgeCard, "fake", "m", "1", job.ID, `{"title":"T","summary":{"text":"S","citations":["seg-0001"]},"keyPoints":[],"chapters":[],"quotes":[],"tags":[]}`)
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.store.SetCurrentVersion(ctx, models.SourceEpisode, epID, store.KindKnowledgeCard, version); err != nil {
		t.Fatal(err)
	}
	// 高光失败（引擎/预算类原因）
	hl, _, err := srv.store.EnqueueJobIdempotent(ctx, store.JobIntentSpec{
		SourceType: models.SourceEpisode, SourceID: epID, JobType: models.JobHighlight,
		IntentID: "highlight:api:v1", InputSnapshotJSON: `{"transcript_version":1}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.store.MarkJobRunning(ctx, hl.ID); err != nil {
		t.Fatal(err)
	}
	if err := srv.store.MarkJobFailed(ctx, hl.ID, "预算检查拒绝任务: x"); err != nil {
		t.Fatal(err)
	}

	before, _ := srv.store.ListQueuedOrRunning(ctx)
	rec := doWithCookie(srv, session, http.MethodGet, "/sources/episode/"+epID)
	if rec.Code != http.StatusOK {
		t.Fatalf("单集页应 200: %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"处理进度", "知识", "DJ 高光", "AI 解说", "重试此阶段", "预算不足"} {
		if !strings.Contains(body, want) {
			t.Fatalf("单集页缺少 %q", want)
		}
	}
	after, _ := srv.store.ListQueuedOrRunning(ctx)
	if len(after) != len(before) {
		t.Fatalf("GET 不得入队任务: %d → %d", len(before), len(after))
	}
}

// TestRetryStageEndpoint_FrozenInputAndIdempotency 重试端点：带回冻结输入；双击幂等。
func TestRetryStageEndpoint_FrozenInputAndIdempotency(t *testing.T) {
	srv, session, epID := seedSnapshotSource(t)
	get := func() string {
		rec := doWithCookie(srv, session, http.MethodGet, "/dashboard")
		for _, c := range rec.Result().Cookies() {
			if c.Name == "cwp_csrf" {
				return c.Value
			}
		}
		return ""
	}
	csrf := get()
	post := func() *httptest.ResponseRecorder {
		form := url.Values{"_csrf": {csrf}, "source_type": {"episode"}, "source_id": {epID}, "stage": {"highlight"}}
		req := httptest.NewRequest(http.MethodPost, "/api/retry-stage", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(session)
		req.AddCookie(&http.Cookie{Name: "cwp_csrf", Value: csrf})
		rec := httptest.NewRecorder()
		srv.Router().ServeHTTP(rec, req)
		return rec
	}
	if rec := post(); rec.Code != http.StatusSeeOther {
		t.Fatalf("重试应 303: %d %s", rec.Code, rec.Body.String())
	}
	if rec := post(); rec.Code != http.StatusSeeOther {
		t.Fatalf("双击重试应幂等 303: %d", rec.Code)
	}
	jobs, _ := srv.store.ListQueuedOrRunning(t.Context())
	count := 0
	for _, j := range jobs {
		if j.JobType == models.JobHighlight {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("双击重试只应有一个活跃高光任务: %d", count)
	}
}
