package store

import (
	"errors"
	"strings"
	"testing"

	"github.com/woyin/orangecast/internal/provider"
)

func TestWritingPurposeLegacyRecallKeepsOldProtocolAndBoundsHistory(t *testing.T) {
	s, article, req, _ := readyKnowledgeRevisionFixture(t)
	ctx := t.Context()
	req.PromptVersion = "knowledge-article-v3"
	req.WritingPurpose = nil
	req.StageConfigs = nil
	req.Estimate = nil
	req.Topic = &provider.KnowledgeTopic{Question: strings.Repeat("理解来源", 80), Thesis: "保留个人理解边界", MaterialIDs: []string{req.Materials[0].ID, "not-present"}}
	req.Materials = req.Materials[:1]
	req.History = []provider.KnowledgeTopic{{ArticleID: article.ID, Title: "不可重复的旧方向", Question: "旧问题"}, {ArticleID: article.ID, Title: "不可重复的旧方向", Question: "旧问题"}, {Title: strings.Repeat("过长", 5000)}}
	updated, err := s.RecallKnowledgeMaterials(ctx, article.ProfileID, "pod", req, *req.Topic)
	if err != nil {
		t.Fatal(err)
	}
	if updated.PromptVersion != req.PromptVersion || updated.WritingPurpose != nil || updated.StageConfigs != nil || updated.Estimate != nil {
		t.Fatal("legacy contract upgraded")
	}
	if len(updated.Materials) < 1 || len(updated.Topic.MaterialIDs) != 1 || updated.Topic.MaterialIDs[0] != req.Materials[0].ID {
		t.Fatal("stale material identifiers retained", updated.Topic)
	}
	seen := map[string]bool{}
	for _, history := range updated.History {
		key := history.ArticleID + history.Title + history.Question
		if seen[key] || len(history.Title) > 10000 {
			t.Fatal("unbounded/duplicate history")
		}
		seen[key] = true
	}
	req.ScopeJSON = "{malformed"
	if _, err = s.RecallKnowledgeMaterials(ctx, article.ProfileID, "pod", req, *req.Topic); !errors.Is(err, ErrInvalidEditorialState) {
		t.Fatal("invalid frozen scope accepted", err)
	}
}

func TestWritingPurposeDiscoveryRespectsExplicitSourceThemeDateAndPodcast(t *testing.T) {
	s, profile, source, _ := knowledgeStoreFixture(t)
	ctx := t.Context()
	req, _, err := s.BuildKnowledgeArticleRequest(ctx, profile, "pod")
	if err != nil {
		t.Fatal(err)
	}
	episode, err := s.GetEpisodeByID(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	material := req.Materials[0]
	cases := []struct {
		name  string
		scope KnowledgeScope
		want  bool
	}{
		{"source", KnowledgeScope{SourceType: "episode", SourceID: source}, true},
		{"other-source", KnowledgeScope{SourceID: "different"}, false},
		{"other-type", KnowledgeScope{SourceType: "document"}, false},
		{"podcast", KnowledgeScope{PodcastID: episode.PodcastID}, true},
		{"other-podcast", KnowledgeScope{PodcastID: "different"}, false},
		{"theme", KnowledgeScope{Theme: material.Content}, true},
		{"other-theme", KnowledgeScope{Theme: "从未出现的主题"}, false},
		{"within-date", KnowledgeScope{From: "2000-01-01", Until: "2100-01-01"}, true},
		{"before-date", KnowledgeScope{From: "2100-01-01"}, false},
		{"after-date", KnowledgeScope{Until: "2000-01-01"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ok, err := s.scopeAllows(ctx, tc.scope, material)
			if err != nil || ok != tc.want {
				t.Fatal(ok, err)
			}
		})
	}
	document := material
	document.SourceType = "document"
	if ok, err := s.scopeAllows(ctx, KnowledgeScope{PodcastID: episode.PodcastID}, document); err != nil || ok {
		t.Fatal("non-audio material in podcast scope", ok, err)
	}
	// An explicit list is still intersected with its source/theme/date scope.
	preview := true
	frozen, _, _, err := s.BuildKnowledgeDiscoveryRequest(ctx, profile, "pod", KnowledgeScope{MaterialIDs: []string{material.ID, material.ID, "missing"}, SourceType: "episode", SourceID: source, PodcastID: episode.PodcastID, From: "2000-01-01", Until: "2100-01-01", WritingMode: "practice", PreviewWritingPlan: &preview}, false)
	if err != nil || frozen.WritingPurpose == nil || frozen.WritingPurpose.Mode != "practice" || !frozen.PreviewWritingPlan {
		t.Fatal(frozen, err)
	}
	for _, m := range frozen.Materials {
		if m.SourceID != source {
			t.Fatal("outside-source material admitted")
		}
	}
	if _, _, _, err = s.BuildKnowledgeDiscoveryRequest(ctx, profile, "pod", KnowledgeScope{MaterialIDs: make([]string, 21)}, false); !errors.Is(err, ErrInvalidEditorialState) {
		t.Fatal("unbounded scope accepted", err)
	}
	if _, _, _, err = s.BuildKnowledgeDiscoveryRequest(ctx, profile, "pod", KnowledgeScope{HistoryCursor: -1}, false); !errors.Is(err, ErrInvalidEditorialState) {
		t.Fatal("negative history cursor accepted", err)
	}
}
