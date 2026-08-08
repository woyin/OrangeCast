package server

import (
	"context"
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

	srv.bundleFor = fakeBundleFor(
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

// TestStudyChat_ScopeTethered (ADR-0018 R3 硬约束一)
// 模型返回无 reference → handler 给出 out_of_scope 反馈，不生成。
func TestStudyChat_ScopeTethered(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "sc@example.com", "password123")
	ctx := context.Background()
	p, _ := srv.store.CreatePodcast(ctx, "https://f.xml", "Pod", "", "")
	srv.store.MergeEpisodes(ctx, p.ID, []models.Episode{{GUID: "g1", Title: "Ep", AudioURL: "https://a.mp3"}})
	eps, _ := srv.store.ListEpisodes(ctx, p.ID)
	sourceID := eps[0].ID
	seedTranscript(t, srv, sourceID)

	srv.bundleFor = fakeBundleFor(nil,
		&fakeStudyChat{result: &provider.StudyChatResult{ScopeFeedback: "超出本集范围"}},
		nil)

	rec := postForm(t, srv, cookie, "/api/study-chat", "source_type=episode&source_id="+sourceID+"&question=明年利率？")
	if rec.Code != http.StatusOK {
		t.Fatalf("应 200（含 scope 反馈），实际 %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "out_of_scope") {
		t.Errorf("应标注 out_of_scope，实际 %s", body)
	}
	if !strings.Contains(body, "超出本集范围") {
		t.Errorf("应含 scope 反馈文案，实际 %s", body)
	}
}

// TestStudyChat_ReferenceRejected (ADR-0018 R3 硬约束二)
// ReferenceCheck 判定不相关 → 回答被抑制，给可见反馈。
func TestStudyChat_ReferenceRejected(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "sc2@example.com", "password123")
	ctx := context.Background()
	p, _ := srv.store.CreatePodcast(ctx, "https://f.xml", "Pod", "", "")
	srv.store.MergeEpisodes(ctx, p.ID, []models.Episode{{GUID: "g1", Title: "Ep", AudioURL: "https://a.mp3"}})
	eps, _ := srv.store.ListEpisodes(ctx, p.ID)
	sourceID := eps[0].ID
	seedTranscript(t, srv, sourceID)

	srv.bundleFor = fakeBundleFor(nil,
		&fakeStudyChat{result: &provider.StudyChatResult{Answer: &provider.StudyChatMessage{
			Role: "assistant", Content: "跑题回答", ReferenceSegmentIDs: []string{"seg-0001"},
		}}},
		&fakeRefChecker{result: provider.ReferenceCheckResult{Related: false, Reason: "主题漂移"}})

	rec := postForm(t, srv, cookie, "/api/study-chat", "source_type=episode&source_id="+sourceID+"&question=q")
	if rec.Code != http.StatusOK {
		t.Fatalf("应 200，实际 %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "reference_rejected") {
		t.Errorf("应标注 reference_rejected，实际 %s", body)
	}
	if strings.Contains(body, "跑题回答") {
		t.Error("被抑制的回答不应呈现给 Owner")
	}
}

// TestStudyChat_ValidAnswerReturned (ADR-0018 R3)
// 通过两条硬约束 → 回答正常返回。
func TestStudyChat_ValidAnswerReturned(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "sc3@example.com", "password123")
	ctx := context.Background()
	p, _ := srv.store.CreatePodcast(ctx, "https://f.xml", "Pod", "", "")
	srv.store.MergeEpisodes(ctx, p.ID, []models.Episode{{GUID: "g1", Title: "Ep", AudioURL: "https://a.mp3"}})
	eps, _ := srv.store.ListEpisodes(ctx, p.ID)
	sourceID := eps[0].ID
	seedTranscript(t, srv, sourceID)

	srv.bundleFor = fakeBundleFor(nil,
		&fakeStudyChat{result: &provider.StudyChatResult{Answer: &provider.StudyChatMessage{
			Role: "assistant", Content: "通胀就是货币购买力下降", ReferenceSegmentIDs: []string{"seg-0001"}},
		}},
		&fakeRefChecker{result: provider.ReferenceCheckResult{Related: true, Reason: "主题扎根"}})

	rec := postForm(t, srv, cookie, "/api/study-chat", "source_type=episode&source_id="+sourceID+"&question=解释通胀")
	if rec.Code != http.StatusOK {
		t.Fatalf("应 200，实际 %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "通胀就是货币购买力下降") {
		t.Errorf("应返回回答，实际 %s", body)
	}
	if !strings.Contains(body, "generated") {
		t.Error("应标注 generated")
	}
}
