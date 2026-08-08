package store

import (
	"context"
	"testing"

	"github.com/woyin/orangecast/internal/models"
)

// TestMigration0014_NarrationsTable (ADR-0019 R4)
// 全新库应应用到 0014，narrations 表存在。
func TestMigration0014_NarrationsTable(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	v, err := AppliedVersion(ctx, s.DB)
	if err != nil {
		t.Fatal(err)
	}
	if v < 14 {
		t.Fatalf("应已应用到 0014，实际 %d", v)
	}
	var name string
	err = s.DB.QueryRowContext(ctx,
		`SELECT name FROM sqlite_master WHERE type='table' AND name='narrations'`).Scan(&name)
	if err != nil {
		t.Fatalf("narrations 表应存在: %v", err)
	}
}

// TestSetCurrentVersion_Highlight_NoLongerClobbersCard (既有 bug 修正)
// 之前 SetCurrentVersion(KindHighlight) 会落到 current_card_version 覆盖 KnowledgeCard 指针。
// 修正后应映射到 current_highlight_version，不影响 card 指针。
func TestSetCurrentVersion_Highlight_NoLongerClobbersCard(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedUser(t, s, "a@b.com")
	p, _ := s.CreatePodcast(ctx, "https://f.xml", "Pod", "", "")
	s.MergeEpisodes(ctx, p.ID, []models.Episode{{GUID: "g1", Title: "ep", AudioURL: "https://a.mp3"}})
	eps, _ := s.ListEpisodes(ctx, p.ID)
	st, src := models.SourceEpisode, eps[0].ID

	// 建一个 card 版本并设为 current。
	// 先建一个 job 满足 artifact_versions.job_id 的外键。
	jobID := "job-test-1"
	_, err := s.DB.ExecContext(ctx, `INSERT INTO processing_jobs (id, source_type, source_id, job_type, status, attempt_count) VALUES (?, ?, ?, 'analyze', 'succeeded', 1)`, jobID, string(st), src)
	if err != nil {
		t.Fatal(err)
	}
	v1, err := s.CreateArtifactVersion(ctx, st, src, KindKnowledgeCard, "groq", "m", "1", jobID, "{}")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetCurrentVersion(ctx, st, src, KindKnowledgeCard, v1); err != nil {
		t.Fatal(err)
	}
	// 建一个 highlight 版本并设为 current。
	v2, err := s.CreateArtifactVersion(ctx, st, src, KindHighlight, "groq", "m", "1", jobID, "{}")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetCurrentVersion(ctx, st, src, KindHighlight, v2); err != nil {
		t.Fatal(err)
	}

	// card current 必须仍是 v1，未被 highlight 覆盖。
	gotCard, err := s.GetCurrentVersion(ctx, st, src, KindKnowledgeCard)
	if err != nil {
		t.Fatal(err)
	}
	if gotCard.Version != v1 {
		t.Errorf("card current 应仍为 %d，不被 highlight 覆盖，实际 %d", v1, gotCard.Version)
	}
	// highlight current 应为 v2。
	gotHL, err := s.GetCurrentVersion(ctx, st, src, KindHighlight)
	if err != nil {
		t.Fatal(err)
	}
	if gotHL.Version != v2 {
		t.Errorf("highlight current 应为 %d，实际 %d", v2, gotHL.Version)
	}
}
