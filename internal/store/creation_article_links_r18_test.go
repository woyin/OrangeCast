package store

import (
	"context"
	"sync"
	"testing"

	"github.com/woyin/orangecast/internal/models"
)

func TestCreationArticleLinkR18_ExactAtomicAndIdempotent(t *testing.T) {
	ctx := context.Background()
	s, brief, _, _ := makeConfirmFixture(t)
	link, err := s.ConfirmCreationBriefVersionAndEnsureLink(ctx, brief.ID, brief.CurrentVersion, "v2")
	if err != nil {
		t.Fatal(err)
	}
	if link.CreationBriefVersion != brief.CurrentVersion {
		t.Fatalf("exact version=%d", link.CreationBriefVersion)
	}
	ap, err := s.GetArticleProposal(ctx, link.ArticleProposalID)
	if err != nil {
		t.Fatal(err)
	}
	ab, err := s.GetArticleBrief(ctx, link.ArticleBriefID)
	if err != nil {
		t.Fatal(err)
	}
	if ap.Thesis != "Owner" || ab.Thesis != "Owner" || ab.Outline != "Outline" || ab.Status != "confirmed" || ab.ConfirmedAt == nil {
		t.Fatalf("compat fields: ap=%+v ab=%+v", ap, ab)
	}
	link2, err := s.ConfirmCreationBriefVersionAndEnsureLink(ctx, brief.ID, brief.CurrentVersion, "v2")
	if err != nil {
		t.Fatal(err)
	}
	if link2.ID != link.ID || link2.ArticleProposalID != link.ArticleProposalID || link2.ArticleBriefID != link.ArticleBriefID {
		t.Fatal("repeat created new compatibility IDs")
	}
	var wg sync.WaitGroup
	type ids struct{ link, ap, ab string }
	idsCh := make(chan ids, 8)
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			l, e := s.ConfirmCreationBriefVersionAndEnsureLink(ctx, brief.ID, brief.CurrentVersion, "v2")
			if e != nil {
				errs <- e
			} else {
				idsCh <- ids{link: l.ID, ap: l.ArticleProposalID, ab: l.ArticleBriefID}
			}
		}()
	}
	wg.Wait()
	close(idsCh)
	close(errs)
	for e := range errs {
		t.Fatal(e)
	}
	seen := map[ids]bool{}
	for id := range idsCh {
		seen[id] = true
	}
	if len(seen) != 1 {
		t.Fatalf("concurrent compatibility IDs=%v", seen)
	}
	var n int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM creation_article_links WHERE creation_proposal_id=?`, link.CreationProposalID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("link count=%d err=%v", n, err)
	}
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM article_proposals WHERE id=?`, link.ArticleProposalID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("AP count=%d err=%v", n, err)
	}
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM article_briefs WHERE id=?`, link.ArticleBriefID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("AB count=%d err=%v", n, err)
	}
}

func assertNoR18Compatibility(t *testing.T, s *Store) {
	t.Helper()
	ctx := context.Background()
	var n int
	for _, table := range []string{"creation_article_links", "article_proposals", "article_briefs"} {
		if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table).Scan(&n); err != nil || n != 0 {
			t.Fatalf("%s=%d err=%v", table, n, err)
		}
	}
}

func TestCreationArticleLinkR18_RejectionsRollback(t *testing.T) {
	cases := []struct {
		name string
		run  func(*testing.T, *Store, *models.CreationBrief, string, string)
	}{
		{"draft ensure", func(t *testing.T, s *Store, b *models.CreationBrief, p, k string) {
			if _, err := s.EnsureCreationArticleLinkExact(context.Background(), p, b.ID, b.CurrentVersion, "v2"); err == nil {
				t.Fatal("draft must reject")
			}
		}},
		{"stale version", func(t *testing.T, s *Store, b *models.CreationBrief, p, k string) {
			if _, err := s.ConfirmCreationBriefVersionAndEnsureLink(context.Background(), b.ID, b.CurrentVersion-1, "v2"); err == nil {
				t.Fatal("stale version must reject")
			}
		}},
		{"blocking gap", func(t *testing.T, s *Store, b *models.CreationBrief, p, k string) {
			if _, err := s.GetCreationProposal(context.Background(), p); err != nil {
				t.Fatal(err)
			}
			if _, err := s.CreateResearchNeed(context.Background(), models.ResearchNeed{CreationProposalID: p, Severity: "blocking", Question: "gap"}); err != nil {
				t.Fatal(err)
			}
			if _, err := s.ConfirmCreationBriefVersionAndEnsureLink(context.Background(), b.ID, b.CurrentVersion, "v2"); err == nil {
				t.Fatal("blocking gap must reject")
			}
		}},
		{"stale material", func(t *testing.T, s *Store, b *models.CreationBrief, p, k string) {
			if err := s.MarkKeyPointStale(context.Background(), k, "changed"); err != nil {
				t.Fatal(err)
			}
			if _, err := s.ConfirmCreationBriefVersionAndEnsureLink(context.Background(), b.ID, b.CurrentVersion, "v2"); err == nil {
				t.Fatal("stale material must reject")
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, b, _, kp := makeConfirmFixture(t)
			p, err := s.GetCreationProposal(context.Background(), b.CreationProposalID)
			if err != nil {
				t.Fatal(err)
			}
			tc.run(t, s, b, p.ID, kp)
			if got, _ := s.GetCreationBrief(context.Background(), b.ID); got.Status != "draft" || got.ConfirmedVersion != 0 {
				t.Fatalf("brief mutated: %+v", got)
			}
			assertNoR18Compatibility(t, s)
		})
	}
	t.Run("article brief insert failure", func(t *testing.T) {
		s, b, _, _ := makeConfirmFixture(t)
		if _, err := s.DB.ExecContext(context.Background(), `DROP TABLE article_briefs`); err != nil {
			t.Fatal(err)
		}
		if _, err := s.ConfirmCreationBriefVersionAndEnsureLink(context.Background(), b.ID, b.CurrentVersion, "v2"); err == nil {
			t.Fatal("expected compatibility insert failure")
		}
		got, err := s.GetCreationBrief(context.Background(), b.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != "draft" || got.ConfirmedVersion != 0 {
			t.Fatalf("confirmation must roll back: %+v", got)
		}
		var n int
		if err := s.DB.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM creation_article_links`).Scan(&n); err != nil || n != 0 {
			t.Fatalf("links=%d err=%v", n, err)
		}
		if err := s.DB.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM article_proposals`).Scan(&n); err != nil || n != 0 {
			t.Fatalf("AP=%d err=%v", n, err)
		}
	})
}

func TestCreationArticleLinkR18_NewRevisionReusesCompatibilityIDs(t *testing.T) {
	ctx := context.Background()
	s, b, _, _ := makeConfirmFixture(t)
	l1, err := s.ConfirmCreationBriefVersionAndEnsureLink(ctx, b.ID, b.CurrentVersion, "v2")
	if err != nil {
		t.Fatal(err)
	}
	v := b.CurrentVersion
	rev, err := s.GetCreationBriefRevision(ctx, b.ID, v)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateCreationBriefRevisionCAS(ctx, b.ID, v, models.CreationBriefRevision{OwnerClaim: rev.OwnerClaim, Outline: "新提纲", MaterialPlanJSON: rev.MaterialPlanJSON, Style: "新风格", TargetLength: intPtrR18(1200)}); err != nil {
		t.Fatal(err)
	}
	b2, _ := s.GetCreationBrief(ctx, b.ID)
	l2, err := s.ConfirmCreationBriefVersionAndEnsureLink(ctx, b.ID, b2.CurrentVersion, "v2")
	if err != nil {
		t.Fatal(err)
	}
	if l2.ID != l1.ID || l2.ArticleProposalID != l1.ArticleProposalID || l2.ArticleBriefID != l1.ArticleBriefID || l2.CreationBriefVersion != b2.CurrentVersion {
		t.Fatalf("IDs/version changed: %+v %+v", l1, l2)
	}
	ab, _ := s.GetArticleBrief(ctx, l2.ArticleBriefID)
	if ab.Outline != "新提纲" || ab.Style != "新风格" || ab.TargetLength == nil || *ab.TargetLength != 1200 {
		t.Fatalf("compat AB not updated: %+v", ab)
	}
	var n int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM creation_article_links WHERE creation_proposal_id=?`, l1.CreationProposalID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("links=%d err=%v", n, err)
	}
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM article_proposals WHERE id=?`, l1.ArticleProposalID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("AP=%d err=%v", n, err)
	}
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM article_briefs WHERE id=?`, l1.ArticleBriefID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("AB=%d err=%v", n, err)
	}
}
func intPtrR18(v int) *int { return &v }
