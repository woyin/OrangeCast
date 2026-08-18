package store

import (
	"context"
	"errors"
	"testing"

	"github.com/woyin/orangecast/internal/models"
)

func TestEditorialProductionLifecycle(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedUser(t, s, "owner@example.com")
	podcast, err := s.CreatePodcast(ctx, "https://feed.example.com/rss", "Tech Pod", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.MergeEpisodes(ctx, podcast.ID, []models.Episode{{GUID: "episode-1", Title: "Episode", AudioURL: "https://cdn.example.com/ep.mp3"}}); err != nil {
		t.Fatal(err)
	}
	episodes, err := s.ListEpisodes(ctx, podcast.ID)
	if err != nil {
		t.Fatal(err)
	}

	profile, err := s.CreateEditorialProfile(ctx, models.EditorialProfile{Name: "技术周刊", SourceAttribution: "standard"})
	if err != nil {
		t.Fatal(err)
	}
	profiles, err := s.ListEditorialProfiles(ctx)
	if err != nil || len(profiles) != 1 || profiles[0].ID != profile.ID {
		t.Fatalf("profiles should list created profile: profiles=%+v err=%v", profiles, err)
	}
	if err := s.GrantSourceScope(ctx, profile.ID, models.SourceEpisode, episodes[0].ID); err != nil {
		t.Fatal(err)
	}
	scopes, err := s.ListScopedSources(ctx, profile.ID)
	if err != nil || len(scopes) != 1 || scopes[0].SourceID != episodes[0].ID {
		t.Fatalf("scope should list explicit source: scopes=%+v err=%v", scopes, err)
	}
	inScope, err := s.IsSourceInScope(ctx, profile.ID, models.SourceEpisode, episodes[0].ID)
	if err != nil || !inScope {
		t.Fatalf("source should be explicitly in scope: inScope=%v err=%v", inScope, err)
	}
	if err := s.SetSourceProductionPolicy(ctx, models.SourceEpisode, episodes[0].ID, "internal", models.ModelDataLocalOnly); err != nil {
		t.Fatal(err)
	}
	if policy, err := s.GetSourcePolicy(ctx, models.SourceEpisode, episodes[0].ID); err != nil || policy.ProductionUse != "internal" || policy.ModelDataPolicy != models.ModelDataLocalOnly {
		t.Fatalf("source policy should remain readable as migration data: policy=%+v err=%v", policy, err)
	}
	if usable, err := s.CanUseSourceForPublication(ctx, profile.ID, models.SourceEpisode, episodes[0].ID); err != nil || !usable {
		t.Fatalf("Owner-added active source must remain creatively usable: usable=%v err=%v", usable, err)
	}
	if external, err := s.CanSendSourceToExternalProvider(ctx, models.SourceEpisode, episodes[0].ID); err != nil || external {
		t.Fatalf("local-only source must not be sent externally: external=%v err=%v", external, err)
	}
	if err := s.SetSourceProductionPolicy(ctx, models.SourceEpisode, episodes[0].ID, "public", models.ModelDataExternalAllowed); err != nil {
		t.Fatal(err)
	}
	if usable, err := s.CanUseSourceForPublication(ctx, profile.ID, models.SourceEpisode, episodes[0].ID); err != nil || !usable {
		t.Fatalf("active source must be creatively usable without scope authorization: usable=%v err=%v", usable, err)
	}
	if external, err := s.CanSendSourceToExternalProvider(ctx, models.SourceEpisode, episodes[0].ID); err != nil || !external {
		t.Fatalf("external-allowed source should be eligible: external=%v err=%v", external, err)
	}

	proposal, err := s.CreateArticleProposal(ctx, models.ArticleProposal{
		EditorialProfileID: profile.ID,
		Kind:               "evergreen",
		Title:              "AI 编程的审查成本",
		Thesis:             "效率收益会转化为审查负担。",
		CandidateKeyPoints: `["kp-1","kp-2"]`,
	})
	if err != nil {
		t.Fatal(err)
	}
	proposals, err := s.ListArticleProposals(ctx, profile.ID)
	if err != nil || len(proposals) != 1 || proposals[0].ID != proposal.ID {
		t.Fatalf("proposals should list created proposal: proposals=%+v err=%v", proposals, err)
	}
	if _, err := s.CreateArticleBrief(ctx, models.ArticleBrief{ProposalID: proposal.ID}); !errors.Is(err, ErrInvalidEditorialState) {
		t.Fatalf("brief must require accepted proposal, got %v", err)
	}
	if err := s.SetArticleProposalStatus(ctx, proposal.ID, "accepted"); err != nil {
		t.Fatal(err)
	}
	brief, err := s.CreateArticleBrief(ctx, models.ArticleBrief{
		ProposalID:   proposal.ID,
		Thesis:       proposal.Thesis,
		Outline:      "# 先说结论",
		MaterialPlan: `["kp-1","kp-2"]`,
		ConflictPlan: `[]`,
	})
	if err != nil {
		t.Fatal(err)
	}
	briefs, err := s.ListArticleBriefs(ctx, profile.ID)
	if err != nil || len(briefs) != 1 || briefs[0].ID != brief.ID {
		t.Fatalf("briefs should list created brief: briefs=%+v err=%v", briefs, err)
	}
	if _, err := s.CreateArticleDraft(ctx, brief.ID, "草稿"); !errors.Is(err, ErrInvalidEditorialState) {
		t.Fatalf("draft must require confirmed brief, got %v", err)
	}
	if err := s.ConfirmArticleBrief(ctx, brief.ID); err != nil {
		t.Fatal(err)
	}
	draft, err := s.CreateArticleDraft(ctx, brief.ID, "AI 编程的审查成本")
	if err != nil {
		t.Fatal(err)
	}
	drafts, err := s.ListArticleDrafts(ctx, profile.ID)
	if err != nil || len(drafts) != 1 || drafts[0].ID != draft.ID {
		t.Fatalf("drafts should list created draft: drafts=%+v err=%v", drafts, err)
	}

	first, err := s.CreateArticleRevision(ctx, models.ArticleRevision{
		DraftID: draft.ID, Title: draft.Title, Markdown: "第一版", Origin: "writer",
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.Version != 1 {
		t.Fatalf("first revision version=%d, want 1", first.Version)
	}
	mapRow, err := s.CreateEvidenceMap(ctx, models.EvidenceMap{
		RevisionID: first.ID, Kind: models.EvidenceParaphrased, Excerpt: "效率收益", KeyPointIDs: `["kp-1"]`,
	})
	if err != nil || mapRow.ID == "" {
		t.Fatalf("create evidence map: row=%+v err=%v", mapRow, err)
	}
	maps, err := s.ListEvidenceMaps(ctx, first.ID)
	if err != nil || len(maps) != 1 || maps[0].ID != mapRow.ID {
		t.Fatalf("evidence maps should list exact revision mappings: maps=%+v err=%v", maps, err)
	}
	if ready, err := s.IsRevisionReadyForPublication(ctx, first.ID); err != nil || ready {
		t.Fatalf("unreviewed revision must not be ready: ready=%v err=%v", ready, err)
	}
	reviewerProvider, reviewerModel := "test-reviewer", "test-model"
	if _, err := s.CreateArticleReview(ctx, models.ArticleReview{RevisionID: first.ID, Kind: "evidence", Status: "failed", IssuesJSON: `["missing attribution"]`, Provider: &reviewerProvider, Model: &reviewerModel}); err != nil {
		t.Fatal(err)
	}
	if ready, err := s.IsRevisionReadyForPublication(ctx, first.ID); err != nil || ready {
		t.Fatalf("failed evidence review must block publication: ready=%v err=%v", ready, err)
	}
	second, err := s.CreateArticleRevision(ctx, models.ArticleRevision{
		DraftID: draft.ID, Title: draft.Title, Markdown: "第二版", Origin: "owner",
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.Version != 2 {
		t.Fatalf("second revision version=%d, want 2", second.Version)
	}
	if _, err := s.CreateArticleReview(ctx, models.ArticleReview{RevisionID: second.ID, Kind: "style", Status: "advisory"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateArticleReview(ctx, models.ArticleReview{RevisionID: second.ID, Kind: "evidence", Status: "passed", Provider: &reviewerProvider, Model: &reviewerModel}); err != nil {
		t.Fatal(err)
	}
	reviews, err := s.ListArticleReviews(ctx, second.ID)
	if err != nil || len(reviews) != 2 || reviews[0].RevisionID != second.ID || reviews[1].RevisionID != second.ID {
		t.Fatalf("reviews should be revision-scoped and newest first: reviews=%+v err=%v", reviews, err)
	}
	if ready, err := s.IsRevisionReadyForPublication(ctx, second.ID); err != nil || !ready {
		t.Fatalf("trusted evidence plus style review should unlock publication: ready=%v err=%v", ready, err)
	}
	gotDraft, err := s.GetArticleDraft(ctx, draft.ID)
	if err != nil || gotDraft.CurrentRevisionID == nil || *gotDraft.CurrentRevisionID != second.ID || gotDraft.Status != "ready" {
		t.Fatalf("current revision/status mismatch: draft=%+v err=%v", gotDraft, err)
	}
	revisions, err := s.ListArticleRevisions(ctx, draft.ID)
	if err != nil || len(revisions) != 2 || revisions[0].ID != second.ID {
		t.Fatalf("revision history should be immutable and newest first: revisions=%+v err=%v", revisions, err)
	}
	feedback, err := s.CreateEditorialFeedback(ctx, models.EditorialFeedback{
		EditorialProfileID: profile.ID, EntityType: "article_proposal", EntityID: proposal.ID, Action: "accepted", DetailsJSON: `{"changed":false}`,
	})
	if err != nil || feedback.ID == "" {
		t.Fatalf("feedback should persist: feedback=%+v err=%v", feedback, err)
	}
}

func TestEditorialScopeAndPolicyRejectInvalidInput(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, err := s.CreateEditorialProfile(ctx, models.EditorialProfile{Name: ""}); !errors.Is(err, ErrInvalidEditorialState) {
		t.Fatalf("empty profile name should be rejected: %v", err)
	}
	if err := s.GrantSourceScope(ctx, "profile", models.SourceType("unknown"), "source"); !errors.Is(err, ErrInvalidEditorialState) {
		t.Fatalf("unknown source type should be rejected: %v", err)
	}
	if _, err := s.CreateEvidenceMap(ctx, models.EvidenceMap{RevisionID: "revision", Kind: models.EvidenceSynthesized, KeyPointIDs: `[]`}); !errors.Is(err, ErrInvalidEditorialState) {
		t.Fatalf("synthesized expression without evidence should be rejected: %v", err)
	}
}

func TestListArticleBriefsReturnsQueryError(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.DB.Exec(`DROP TABLE article_briefs`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ListArticleBriefs(context.Background(), "profile"); err == nil {
		t.Fatal("missing article_briefs table should return query error")
	}
}

func TestListEvidenceMapsReturnsQueryError(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.DB.Exec(`DROP TABLE evidence_maps`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ListEvidenceMaps(context.Background(), "revision"); err == nil {
		t.Fatal("missing evidence_maps table should return query error")
	}
}

func TestListEvidenceMapsReturnsScanError(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.DB.Exec(`DROP TABLE evidence_maps`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`CREATE TABLE evidence_maps (id TEXT, revision_id TEXT, kind TEXT, excerpt TEXT, keypoint_ids_json TEXT, created_at TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`INSERT INTO evidence_maps (revision_id, kind, excerpt, keypoint_ids_json) VALUES ('r', 'quoted', 'x', '[]')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ListEvidenceMaps(context.Background(), "r"); err == nil {
		t.Fatal("NULL id should return scan error")
	}
}

func TestArchiveSourceIsReversible(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedUser(t, s, "owner@example.com")
	podcast, _ := s.CreatePodcast(ctx, "https://feed.example.com/rss", "Tech Pod", "", "")
	s.MergeEpisodes(ctx, podcast.ID, []models.Episode{{GUID: "episode-1", Title: "Episode", AudioURL: "https://cdn.example.com/ep.mp3"}})
	episodes, _ := s.ListEpisodes(ctx, podcast.ID)
	if err := s.ArchiveSource(ctx, models.SourceEpisode, episodes[0].ID, true); err != nil {
		t.Fatal(err)
	}
	var archivedAt *string
	if err := s.DB.QueryRowContext(ctx, `SELECT archived_at FROM episodes WHERE id=?`, episodes[0].ID).Scan(&archivedAt); err != nil || archivedAt == nil {
		t.Fatalf("archive should persist a timestamp: value=%v err=%v", archivedAt, err)
	}
	if err := s.ArchiveSource(ctx, models.SourceEpisode, episodes[0].ID, false); err != nil {
		t.Fatal(err)
	}
	if err := s.DB.QueryRowContext(ctx, `SELECT archived_at FROM episodes WHERE id=?`, episodes[0].ID).Scan(&archivedAt); err != nil || archivedAt != nil {
		t.Fatalf("unarchive should clear timestamp: value=%v err=%v", archivedAt, err)
	}
}

func TestSourcePolicyGuardsArchivedAndUnapprovedSources(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedUser(t, s, "owner@example.com")
	podcast, _ := s.CreatePodcast(ctx, "https://feed.example.com/rss", "Tech Pod", "", "")
	s.MergeEpisodes(ctx, podcast.ID, []models.Episode{{GUID: "episode-1", Title: "Episode", AudioURL: "https://cdn.example.com/ep.mp3"}})
	episodes, _ := s.ListEpisodes(ctx, podcast.ID)
	profile, _ := s.CreateEditorialProfile(ctx, models.EditorialProfile{Name: "品牌"})
	if err := s.GrantSourceScope(ctx, profile.ID, models.SourceEpisode, episodes[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := s.ArchiveSource(ctx, models.SourceEpisode, episodes[0].ID, true); err != nil {
		t.Fatal(err)
	}
	if usable, err := s.CanUseSourceForPublication(ctx, profile.ID, models.SourceEpisode, episodes[0].ID); err != nil || usable {
		t.Fatalf("archived source must not be usable: usable=%v err=%v", usable, err)
	}
	if err := s.ArchiveSource(ctx, models.SourceEpisode, episodes[0].ID, false); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSourceProductionPolicy(ctx, models.SourceEpisode, episodes[0].ID, "public", models.ModelDataApprovedProvidersOnly); err != nil {
		t.Fatal(err)
	}
	if allowed, err := s.CanSendSourceToExternalProvider(ctx, models.SourceEpisode, episodes[0].ID); err != nil || allowed {
		t.Fatalf("approved-provider policy needs a later allowlist and must not default external: allowed=%v err=%v", allowed, err)
	}
	if err := s.SetSourceProductionPolicy(ctx, models.SourceType("invalid"), episodes[0].ID, "public", models.ModelDataExternalAllowed); !errors.Is(err, ErrInvalidEditorialState) {
		t.Fatalf("invalid source type should be rejected: %v", err)
	}
	upload, err := s.CreateUpload(ctx, "private.mp3", "audio/mpeg", 100)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetSourceProductionPolicy(ctx, models.SourceUpload, upload.ID, "public", models.ModelDataExternalAllowed); err != nil {
		t.Fatalf("upload policy should use uploads table: %v", err)
	}
	if allowed, err := s.CanSendSourceToExternalProvider(ctx, models.SourceUpload, upload.ID); err != nil || !allowed {
		t.Fatalf("upload external policy should be readable: allowed=%v err=%v", allowed, err)
	}
}

func TestEditorialStoreRejectsMissingObjectsAndInvalidTransitions(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedUser(t, s, "owner@example.com")

	if _, err := s.GetEditorialProfile(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing profile should be ErrNotFound: %v", err)
	}
	profile, err := s.CreateEditorialProfile(ctx, models.EditorialProfile{Name: "品牌", SourceAttribution: "light"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateEditorialProfile(ctx, models.EditorialProfile{Name: "品牌"}); err == nil {
		t.Fatal("duplicate profile name should fail")
	}
	if err := s.GrantSourceScope(ctx, profile.ID, models.SourceEpisode, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing source cannot be scoped: %v", err)
	}
	if _, err := s.CanSendSourceToExternalProvider(ctx, models.SourceEpisode, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing source policy should be ErrNotFound: %v", err)
	}
	if err := s.SetSourceProductionPolicy(ctx, models.SourceEpisode, "missing", "public", models.ModelDataExternalAllowed); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing source policy update should be ErrNotFound: %v", err)
	}
	if err := s.ArchiveSource(ctx, models.SourceEpisode, "missing", true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing archive source should be ErrNotFound: %v", err)
	}

	if _, err := s.GetArticleProposal(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing proposal should be ErrNotFound: %v", err)
	}
	if err := s.SetArticleProposalStatus(ctx, "missing", "accepted"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing proposal status should be ErrNotFound: %v", err)
	}
	if err := s.SetArticleProposalStatus(ctx, "missing", "wrong"); !errors.Is(err, ErrInvalidEditorialState) {
		t.Fatalf("invalid proposal status should be rejected: %v", err)
	}
	if _, err := s.CreateArticleProposal(ctx, models.ArticleProposal{EditorialProfileID: profile.ID, Kind: "wrong", Title: "x"}); !errors.Is(err, ErrInvalidEditorialState) {
		t.Fatalf("invalid proposal kind should be rejected: %v", err)
	}
	if _, err := s.CreateArticleProposal(ctx, models.ArticleProposal{EditorialProfileID: profile.ID, Title: "x", CandidateKeyPoints: "not-json"}); !errors.Is(err, ErrInvalidEditorialState) {
		t.Fatalf("invalid proposal JSON should be rejected: %v", err)
	}
	proposal, err := s.CreateArticleProposal(ctx, models.ArticleProposal{EditorialProfileID: profile.ID, Title: "合法提案"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetArticleProposalStatus(ctx, proposal.ID, "accepted"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateArticleBrief(ctx, models.ArticleBrief{ProposalID: proposal.ID, MaterialPlan: "not-json"}); !errors.Is(err, ErrInvalidEditorialState) {
		t.Fatalf("invalid brief JSON should be rejected: %v", err)
	}
	brief, err := s.CreateArticleBrief(ctx, models.ArticleBrief{ProposalID: proposal.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetArticleBrief(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing brief should be ErrNotFound: %v", err)
	}
	if _, err := s.CreateArticleDraft(ctx, "missing", "draft"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing brief cannot create draft: %v", err)
	}
	if err := s.ConfirmArticleBrief(ctx, "missing"); !errors.Is(err, ErrInvalidEditorialState) {
		t.Fatalf("missing brief confirmation should be rejected: %v", err)
	}
	if err := s.ConfirmArticleBrief(ctx, brief.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.ConfirmArticleBrief(ctx, brief.ID); !errors.Is(err, ErrInvalidEditorialState) {
		t.Fatalf("brief cannot be confirmed twice: %v", err)
	}
	draft, err := s.CreateArticleDraft(ctx, brief.ID, "draft")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetArticleDraft(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing draft should be ErrNotFound: %v", err)
	}
	if _, err := s.CreateArticleRevision(ctx, models.ArticleRevision{DraftID: draft.ID, Markdown: "", Origin: "writer"}); !errors.Is(err, ErrInvalidEditorialState) {
		t.Fatalf("empty markdown should be rejected: %v", err)
	}
	if _, err := s.CreateArticleRevision(ctx, models.ArticleRevision{DraftID: "missing", Markdown: "x", Origin: "writer"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing draft revision should be ErrNotFound: %v", err)
	}
	revision, err := s.CreateArticleRevision(ctx, models.ArticleRevision{DraftID: draft.ID, Markdown: "body", Origin: "writer"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetArticleRevision(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing revision should be ErrNotFound: %v", err)
	}
	if _, err := s.CreateEvidenceMap(ctx, models.EvidenceMap{RevisionID: revision.ID, Kind: models.EvidenceRhetorical, KeyPointIDs: "not-json"}); !errors.Is(err, ErrInvalidEditorialState) {
		t.Fatalf("invalid map JSON should be rejected: %v", err)
	}
	if _, err := s.CreateEvidenceMap(ctx, models.EvidenceMap{RevisionID: revision.ID, Kind: models.EvidenceRhetorical}); err != nil {
		t.Fatalf("rhetorical text may be evidence-free: %v", err)
	}
	if _, err := s.CreateArticleReview(ctx, models.ArticleReview{RevisionID: revision.ID, Kind: "wrong", Status: "passed"}); !errors.Is(err, ErrInvalidEditorialState) {
		t.Fatalf("invalid review kind should be rejected: %v", err)
	}
	if _, err := s.CreateEditorialFeedback(ctx, models.EditorialFeedback{EditorialProfileID: profile.ID, EntityType: "", EntityID: "x", Action: "accepted"}); !errors.Is(err, ErrInvalidEditorialState) {
		t.Fatalf("invalid feedback should be rejected: %v", err)
	}
	if err := s.RevokeSourceScope(ctx, profile.ID, models.SourceEpisode, "missing"); err != nil {
		t.Fatalf("revoking an absent scope should be idempotent: %v", err)
	}
}

func TestEditorialStoreQueryErrorsSurface(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name  string
		table string
		call  func(*Store) error
	}{
		{"profiles", "editorial_profiles", func(s *Store) error { _, err := s.ListEditorialProfiles(ctx); return err }},
		{"proposals", "article_proposals", func(s *Store) error { _, err := s.ListArticleProposals(ctx, "profile"); return err }},
		{"drafts", "article_drafts", func(s *Store) error { _, err := s.ListArticleDrafts(ctx, "profile"); return err }},
		{"revisions", "article_revisions", func(s *Store) error { _, err := s.ListArticleRevisions(ctx, "draft"); return err }},
		{"reviews", "article_reviews", func(s *Store) error { _, err := s.ListArticleReviews(ctx, "revision"); return err }},
		{"theme_create", "themes", func(s *Store) error {
			profile, err := s.CreateEditorialProfile(ctx, models.EditorialProfile{Name: "profile"})
			if err != nil {
				return err
			}
			_, err = s.CreateTheme(ctx, models.Theme{EditorialProfileID: profile.ID, Name: "主题"})
			return err
		}},
		{"scopes", "editorial_source_scopes", func(s *Store) error { _, err := s.ListScopedSources(ctx, "profile"); return err }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			s := newTestStore(t)
			if _, err := s.DB.ExecContext(ctx, `DROP TABLE `+test.table); err != nil {
				t.Fatal(err)
			}
			if err := test.call(s); err == nil {
				t.Fatalf("missing %s table should surface a query error", test.table)
			}
		})
	}
}
