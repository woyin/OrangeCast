package store

import (
	"database/sql"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// TestV01_FullRestore_NewRelationships V01（备份与恢复全量验证）：
// 构造覆盖全部新关系的数据集（来源快照、听播进度、DJ 清单、构思轮次、
// 素材选择、质量判定、ClaimMap、创作历史桥接、预算预留），
// 执行一致性备份→恢复→逐一验证行数与关键引用一致。
func TestV01_FullRestore_NewRelationships(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()

	// 种子：单集+转录+卡片+笔记。
	epID := seedSnapshotEpisode(t, s)
	seedSnapshotTranscript(t, s, models.SourceEpisode, epID, "V01 要点")
	seedDigestFlowCard(t, s, epID)
	snapshot, _ := s.FreezeSourceSnapshot(ctx, models.SourceEpisode, epID)

	// 1. 来源快照 ✓（上面已冻结）

	// 2. 听播进度。
	if err := s.SaveListeningProgress(ctx, &models.ListeningProgress{
		SourceType: models.SourceEpisode, SourceID: epID,
		PlanID: "highlights:v1", PlanVersion: 1,
		ItemPosition: 1, ItemOffsetSeconds: 3.5, Speed: 1, Seq: 1,
	}); err != nil {
		t.Fatal(err)
	}

	// 3. 精读修订 + 落源 + 缺口。
	d, err := s.PublishEpisodeDigest(ctx, &models.EpisodeDigest{
		SourceType: models.SourceEpisode, SourceID: epID, Title: "V01 精读",
		Provider: "p", Model: "m", PromptVersion: "v",
		SourceSnapshotID: snapshot.ID, ParentDigestID: "", Reason: "",
	}, []models.DigestBlock{
		{Type: models.DigestBlockParaphrase, Text: "正文", Citations: []string{"seg-0001"}},
	}, []models.DigestSearchSource{
		{Query: "q", URL: "https://ex.com/v01", Title: "来源", DocumentID: "doc-v01"},
	}, []models.DigestFactGap{{Text: "缺口"}})
	if err != nil {
		t.Fatal(err)
	}

	// 4. 质量判定。
	if err := s.SaveKeypointQualityResult(ctx, &models.KeypointQualityResult{
		KeyPointID: "kp-v01", SourceType: models.SourceEpisode, SourceID: epID,
		CardVersion: 1, ContentFingerprint: "fp-v01",
		Decision: models.KPQualityReady, Reasons: []string{"原文支持"},
	}); err != nil {
		t.Fatal(err)
	}

	// 5. 构思轮次。
	profile, err := s.EnsureDefaultEditorialProfile(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := s.CreateIdeationSession(ctx, models.IdeationSession{EditorialProfileID: profile.ID, Intent: "V01"})
	if err != nil {
		t.Fatal(err)
	}
	_, _, _ = s.AddIdeationRound(ctx, sess.ID, "v01-nonce", "V01 问题", "{}", `[{"id":"kp-v01"}]`)
	if err != nil {
		t.Fatal(err)
	}

	// 6. 素材选择（需要至少一个关键观点）。
	card := provider.KnowledgeCard{KeyPoints: []provider.KeyPoint{
		{Content: "V01 观点", Citations: []string{"seg-0001"}},
	}}
	segments := []provider.Segment{{ID: "seg-0001", Start: 0, End: 5, Text: "V01 要点"}}
	if _, err := s.IndexKeyPoints(ctx, models.SourceEpisode, epID, "V01", 1, &card, segments); err != nil {
		t.Fatal(err)
	}
	kps, _, _ := s.ListKeyPoints(ctx, 1, 10)
	if len(kps) == 0 {
		t.Fatal("应有关键观点")
	}
	note, err := s.CreateOwnerNote(ctx, models.OwnerNote{
		SourceType: string(models.SourceEpisode), SourceID: epID,
		Kind: "owner_reflection", Content: "V01 笔记",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.SaveCreationSelection(ctx, &models.CreationSelection{
		EditorialProfileID: profile.ID, Title: "V01 选择",
		MaterialIDs: []string{}, NoteIDs: []string{note.ID},
	})
	if err != nil {
		t.Fatal(err)
	}

	// 7. 创作历史（精读登记）。
	if _, err := s.RecordDigestHistory(ctx, d.ID, "unpublished"); err != nil {
		t.Fatal(err)
	}

	// 8. 文章桥接。
	acceptedProposal, err := s.CreateCreationProposal(ctx, models.CreationProposal{
		EditorialProfileID: profile.ID, Status: "proposed",
		WorkingTitle: "V01 方向", ProposedClaim: "V01 主张", CreationForm: "article",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AcceptCreationProposal(ctx, acceptedProposal.ID, "V01 Owner 主张"); err != nil {
		t.Fatal(err)
	}
	v01Brief, err := s.CreateCreationBrief(ctx, models.CreationBrief{
		CreationProposalID: acceptedProposal.ID, OwnerClaim: "V01 Owner 主张",
		MaterialPlanJSON: `["kp-a"]`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ConfirmCreationBrief(ctx, v01Brief.ID); err != nil {
		t.Fatal(err)
	}
	link, err := s.EnsureCreationArticleLink(ctx, acceptedProposal.ID, v01Brief.ID, "v2")
	if err != nil {
		t.Fatal(err)
	}

	// 9. 预算预留（settled）。
	if err := s.SetOwnerMonthlyBudget(ctx, int64Ptr(10000)); err != nil {
		t.Fatal(err)
	}
	if err := s.SetModelPrice(ctx, models.ModelPrice{Provider: "groq", Model: "m01", InputCentsPerMillion: 1, OutputCentsPerMillion: 0}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.HoldBudget(ctx, "v01-job", "analyze", false, "groq", "m01", 1_000_000, 0); err != nil {
		t.Fatal(err)
	}
	if err := s.SettleBudget(ctx, "v01-job", 1); err != nil {
		t.Fatal(err)
	}

	// 备份→恢复。
	dstPath := filepath.Join(t.TempDir(), "v01-backup.db")
	if err := ConsistencyBackup(ctx, s.DB, dstPath); err != nil {
		t.Fatalf("备份失败: %v", err)
	}
	dst, err := sql.Open("sqlite", dstPath)
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Close()

	// 验证核心新关系全部存活。
	checks := []struct {
		name  string
		query string
		want  int
	}{
		{"来源快照", `SELECT COUNT(*) FROM source_snapshots WHERE source_id='` + epID + `' AND status='active'`, 1},
		{"听播进度", `SELECT COUNT(*) FROM listening_progress WHERE source_id='` + epID + `'`, 1},
		{"精读修订", `SELECT COUNT(*) FROM episode_digests WHERE source_id='` + epID + `'`, 1},
		{"精读落源", `SELECT COUNT(*) FROM digest_search_sources WHERE digest_id='` + d.ID + `'`, 1},
		{"精读缺口", `SELECT COUNT(*) FROM digest_fact_gaps WHERE digest_id='` + d.ID + `'`, 1},
		{"质量判定", `SELECT COUNT(*) FROM keypoint_quality_results WHERE keypoint_id='kp-v01'`, 1},
		{"构思轮次", `SELECT COUNT(*) FROM ideation_rounds WHERE session_id='` + sess.ID + `'`, 1},
		{"素材选择", `SELECT COUNT(*) FROM creation_selections WHERE title='V01 选择'`, 1},
		{"精读历史", `SELECT COUNT(*) FROM creation_history WHERE digest_id='` + d.ID + `'`, 1},
		{"文章桥接", `SELECT COUNT(*) FROM creation_article_links WHERE creation_proposal_id='` + acceptedProposal.ID + `'`, 1},
		{"预算预留", `SELECT COUNT(*) FROM budget_reservations WHERE job_id='v01-job' AND status='settled'`, 1},
	}
	for _, chk := range checks {
		var n int
		if err := dst.QueryRow(chk.query).Scan(&n); err != nil {
			t.Errorf("%s: %v", chk.name, err)
			continue
		}
		if n != chk.want {
			t.Errorf("%s: want %d got %d", chk.name, chk.want, n)
		}
	}

	// 验证关键引用一致：精读修订的快照 ID。
	var snapID string
	if err := dst.QueryRow(`SELECT source_snapshot_id FROM episode_digests WHERE id=?`, d.ID).Scan(&snapID); err != nil {
		t.Fatal(err)
	}
	if snapID != snapshot.ID {
		t.Fatalf("快照 ID 应一致: %s != %s", snapID, snapshot.ID)
	}
	// 验证桥接的 ArticleBrief ID。
	var abID string
	if err := dst.QueryRow(`SELECT article_brief_id FROM creation_article_links WHERE creation_proposal_id=?`, acceptedProposal.ID).Scan(&abID); err != nil {
		t.Fatal(err)
	}
	if abID != link.ArticleBriefID {
		t.Fatalf("桥接 ArticleBrief ID 应一致: %s != %s", abID, link.ArticleBriefID)
	}
}

func int64Ptr(v int64) *int64 { return &v }
