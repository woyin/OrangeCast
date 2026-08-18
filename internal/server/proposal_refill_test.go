package server

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
)

func TestProposalRefillHandlerAndNoopPool(t *testing.T) {
	srv := newTestServer(t)
	getRec := httptest.NewRecorder()
	srv.handleProposalRefill(getRec, httptest.NewRequest(http.MethodGet, "/workbench/proposals/refill", nil))
	if getRec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("proposal refill GET should be rejected: %d", getRec.Code)
	}
	postRec := httptest.NewRecorder()
	srv.handleProposalRefill(postRec, httptest.NewRequest(http.MethodPost, "/workbench/proposals/refill", strings.NewReader("profile_id=profile-1")))
	if postRec.Code != http.StatusSeeOther || !strings.Contains(postRec.Header().Get("Location"), "refill=scheduled") {
		t.Fatalf("proposal refill should redirect with a visible status: %d %q", postRec.Code, postRec.Header().Get("Location"))
	}
	profile, _ := srv.store.CreateEditorialProfile(t.Context(), models.EditorialProfile{Name: "已有池"})
	for i := 0; i < scoutBrainstormCount; i++ {
		if _, err := srv.store.CreateArticleProposal(t.Context(), models.ArticleProposal{EditorialProfileID: profile.ID, Title: fmt.Sprintf("已有提案 %d", i), CandidateKeyPoints: "[]"}); err != nil {
			t.Fatal(err)
		}
	}
	created, err := srv.refillProposalPool(t.Context(), profile.ID)
	if err != nil || created != 0 {
		t.Fatalf("full proposal pool should not call Scout: created=%d err=%v", created, err)
	}
}

func TestRefillProposalPoolCreatesExactlyTheMissingBatch(t *testing.T) {
	srv := newTestServer(t)
	podcast, _ := srv.store.CreatePodcast(t.Context(), "https://feed.example.com/refill.xml", "补货播客", "", "")
	srv.store.MergeEpisodes(t.Context(), podcast.ID, []models.Episode{{GUID: "refill-one", Title: "第一集", AudioURL: "https://cdn.example.com/1.mp3"}, {GUID: "refill-two", Title: "第二集", AudioURL: "https://cdn.example.com/2.mp3"}})
	episodes, _ := srv.store.ListEpisodes(t.Context(), podcast.ID)
	for _, episode := range episodes {
		if err := srv.store.IndexKeyPoints(t.Context(), models.SourceEpisode, episode.ID, episode.Title, 1, &provider.KnowledgeCard{KeyPoints: []provider.KeyPoint{{Content: episode.Title + "观点", Citations: []string{"seg-1"}}}}, []provider.Segment{{ID: "seg-1", End: 1}}); err != nil {
			t.Fatal(err)
		}
	}
	keyPoints, _, _ := srv.store.ListKeyPoints(t.Context(), 1, 10)
	profile, _ := srv.store.CreateEditorialProfile(t.Context(), models.EditorialProfile{Name: "补货品牌"})
	for _, episode := range episodes {
		if err := srv.store.GrantSourceScope(t.Context(), profile.ID, models.SourceEpisode, episode.ID); err != nil {
			t.Fatal(err)
		}
	}
	theme, err := srv.store.CreateTheme(t.Context(), models.Theme{EditorialProfileID: profile.ID, Name: "补货主题", Status: "confirmed"})
	if err != nil {
		t.Fatal(err)
	}
	for _, keyPoint := range keyPoints {
		if err := srv.store.AddKeyPointToTheme(t.Context(), theme.ID, keyPoint.ID, "supports"); err != nil {
			t.Fatal(err)
		}
	}
	batch := &batchScout{}
	srv.bundleFor = func(provider.TaskConfig) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{Scout: batch}, nil
	}
	created, err := srv.refillProposalPool(t.Context(), profile.ID)
	if err != nil || created != scoutBrainstormCount {
		t.Fatalf("refill should create five proposals: created=%d err=%v requests=%+v", created, err, batch.requests)
	}
	proposals, err := srv.store.ListArticleProposals(t.Context(), profile.ID)
	if err != nil || len(proposals) != scoutBrainstormCount {
		t.Fatalf("refill should persist five proposed candidates: proposals=%d err=%v", len(proposals), err)
	}
	if len(batch.requests) != 1 || batch.requests[0].ProposalCount != scoutBrainstormCount {
		t.Fatalf("refill should request the missing batch size: %+v", batch.requests)
	}
}

type batchScout struct {
	requests []provider.ScoutRequest
}

func (b *batchScout) Scout(_ context.Context, request provider.ScoutRequest) (*provider.ScoutResult, error) {
	b.requests = append(b.requests, request)
	materials := request.Themes[0].Materials
	proposals := make([]provider.ScoutProposal, 0, request.ProposalCount)
	for i := 0; i < request.ProposalCount; i++ {
		proposals = append(proposals, provider.ScoutProposal{Kind: "evergreen", Title: fmt.Sprintf("补货选题 %d", i+1), Thesis: "补货论点", Audience: "读者", Rationale: "补货测试", CandidateKeyPointIDs: []string{materials[0].KeyPointID, materials[1].KeyPointID}})
	}
	return &provider.ScoutResult{Proposals: proposals}, nil
}

func (b *batchScout) Name() string { return "batch-scout" }
