package store

import (
	"encoding/json"
	"fmt"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"strings"
	"testing"
)

func TestKnowledgeRecallKeepsOldOppositionAndDeniesPrivateCrowding(t *testing.T) {
	s, profile, _, _ := knowledgeStoreFixture(t)
	ctx := t.Context()
	private, err := s.CreatePastedDocument(ctx, "私有记忆材料", "记忆练习")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetSourceProductionPolicy(ctx, models.SourceDocument, private.ID, "internal", models.ModelDataLocalOnly); err != nil {
		t.Fatal(err)
	}
	main, err := s.CreatePastedDocument(ctx, "记忆练习主材料", "记忆练习用于检查自己的表达")
	if err != nil {
		t.Fatal(err)
	}
	opposition, err := s.CreatePastedDocument(ctx, "记忆练习限制", "记忆练习并不是所有情况下适用")
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []struct {
		id    string
		count int
	}{{private.ID, 220}, {main.ID, 80}, {opposition.ID, 2}} {
		for i := 0; i < source.count; i++ {
			text := fmt.Sprintf("记忆练习：用自己的话说明第%d种检查条件。", i)
			if source.id == opposition.ID {
				text = fmt.Sprintf("记忆练习的反例和限制：第%d种条件未必适用。", i)
			}
			_, err := s.CreateOwnerNote(ctx, models.OwnerNote{SourceType: "document", SourceID: source.id, Kind: "owner_reflection", Content: text})
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	req := provider.KnowledgeArticleRequest{PromptVersion: provider.KnowledgeArticlePromptVersion}
	got, err := s.RecallKnowledgeMaterials(ctx, profile, "pod", req, provider.KnowledgeTopic{Question: "记忆练习有哪些限制"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Coverage == nil || len(got.Candidates) != 82 || len(got.Materials) != 20 || !got.Coverage.LimitReached || got.Coverage.ReadCount != 20 {
		t.Fatalf("coverage %+v pool=%d materials=%d", got.Coverage, len(got.Candidates), len(got.Materials))
	}
	var opposite, unread int
	for _, m := range got.Materials {
		if m.SourceID == private.ID {
			t.Fatal("private source external")
		}
		if m.SourceID == opposition.ID {
			opposite++
			if !strings.Contains(m.RetrievalReason, "程序线索") {
				t.Fatal("clue lost", m)
			}
		}
	}
	for _, c := range got.Candidates {
		if c.State == "not_read" {
			unread++
			if !strings.Contains(c.Reason, "尚未读取") {
				t.Fatal(c)
			}
		}
	}
	if opposite != 2 || unread != 62 {
		t.Fatalf("opposition=%d unread=%d", opposite, unread)
	}
	// Restrict exact provider; local search still sees private/unsupported records.
	if err := s.SetSourceProductionPolicy(ctx, models.SourceDocument, opposition.ID, "internal", models.ModelDataApprovedProvidersOnly); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE documents SET approved_providers_json='[" POD "]' WHERE id=?`, opposition.ID); err != nil {
		t.Fatal(err)
	}
	got, err = s.RecallKnowledgeMaterials(ctx, profile, "pod", req, provider.KnowledgeTopic{Question: "记忆练习"})
	if err != nil || len(got.Candidates) != 82 {
		t.Fatal(got.Coverage, err)
	}
	local, err := s.SearchKnowledge(ctx, KnowledgeSearchQuery{Kind: "notes", Text: "记忆练习", Recall: true})
	if err != nil || local.Total != 302 {
		t.Fatal(local.Total, err)
	}
}

func TestKnowledgeRecallCapsMetadataAndKeepsCompleteEvidenceWindows(t *testing.T) {
	s, profile, _, _ := knowledgeStoreFixture(t)
	ctx := t.Context()
	var paragraphs []string
	for i := 0; i < 35; i++ {
		paragraphs = append(paragraphs, fmt.Sprintf("窗口第%d段：记忆练习的适用条件。%s", i, strings.Repeat("来源完整内容。", 45)))
	}
	doc, err := s.CreatePastedDocument(ctx, "完整证据窗口", strings.Join(paragraphs, "\n\n"))
	if err != nil {
		t.Fatal(err)
	}
	segs := DocumentSegments(doc)
	var citations []string
	for _, seg := range segs {
		citations = append(citations, seg.ID)
	}
	note, err := s.CreateOwnerNote(ctx, models.OwnerNote{SourceType: "document", SourceID: doc.ID, Kind: "source_note", Content: "记忆练习需要核对适用条件", CitationsJSON: jsonString(citations)})
	if err != nil {
		t.Fatal(err)
	}
	m, err := s.knowledgeMaterial(ctx, profile, "pod", note.ID)
	if err != nil || m == nil {
		t.Fatal(m, err)
	}
	originalContent := m.Content
	if err := s.compactKnowledgeEvidence(ctx, m, "记忆练习的适用条件"); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(m)
	if m.Content != originalContent || len(raw) > 10000 || len(m.EvidenceWindow) == 0 || len(m.OmittedCitationIDs) == 0 || len(m.Citations)+len(m.OmittedCitationIDs) != len(citations) {
		t.Fatalf("broken window: %d bytes %+v", len(raw), m)
	}
	for _, window := range m.EvidenceWindow {
		found := false
		for _, seg := range segs {
			if window.SegmentID == seg.ID && window.Text == seg.Text && window.Position == float64(seg.Position) {
				found = true
			}
		}
		if !found {
			t.Fatal("invented or cut segment", window)
		}
	}
	if err := s.CheckKnowledgeMaterials(ctx, profile, "pod", []provider.KnowledgeMaterial{*m}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 215; i++ {
		_, err := s.CreateOwnerNote(ctx, models.OwnerNote{SourceType: "document", SourceID: doc.ID, Kind: "owner_reflection", Content: fmt.Sprintf("记忆练习记录第%d项", i)})
		if err != nil {
			t.Fatal(err)
		}
	}
	req := provider.KnowledgeArticleRequest{PromptVersion: provider.KnowledgeArticlePromptVersion, Materials: []provider.KnowledgeMaterial{*m}}
	got, err := s.RecallKnowledgeMaterials(ctx, profile, "pod", req, provider.KnowledgeTopic{Question: "记忆练习"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Candidates) != 200 || got.Coverage.TotalMatches != 216 || !got.Coverage.LimitReached {
		t.Fatal(got.Coverage, len(got.Candidates))
	}
	size := 0
	for _, m := range got.Materials {
		b, _ := json.Marshal(m)
		size += len(b)
		if len(b) > 10000 {
			t.Fatal("oversize evidence")
		}
	}
	if size > 40000 || len(got.Materials) > 20 {
		t.Fatal("unbounded send", size)
	}
}

func TestKnowledgeRecallDoesNotAttributeDuplicatesAndOversizeToModel(t *testing.T) {
	s, profile, ep, _ := knowledgeStoreFixture(t)
	ctx := t.Context()
	var seeds []provider.KnowledgeMaterial
	for i := 0; i < 2; i++ {
		n, err := s.CreateOwnerNote(ctx, models.OwnerNote{SourceType: "episode", SourceID: ep, Kind: "owner_reflection", Content: "同来源重复的个人理解"})
		if err != nil {
			t.Fatal(err)
		}
		m, err := s.knowledgeMaterial(ctx, profile, "pod", n.ID)
		if err != nil {
			t.Fatal(err)
		}
		seeds = append(seeds, *m)
	}
	large := seeds[0]
	large.ID = "oversize-seed"
	large.Content = strings.Repeat("完整笔记不能截断", 1500)
	seeds = append(seeds, large)
	req := provider.KnowledgeArticleRequest{PromptVersion: provider.KnowledgeArticlePromptVersion, Materials: seeds, Topic: &provider.KnowledgeTopic{MaterialIDs: []string{seeds[0].ID, seeds[1].ID, "oversize-seed"}}}
	got, err := s.RecallKnowledgeMaterials(ctx, profile, "pod", req, provider.KnowledgeTopic{Question: "不匹配的词语"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Materials) != 1 || len(got.Topic.MaterialIDs) != 1 || len(got.Exclusions) != 2 {
		t.Fatal(got.Coverage, got.Topic)
	}
	for _, c := range got.Candidates {
		if c.MaterialID == "oversize-seed" && (c.State != "skipped" || !strings.Contains(c.Reason, "未截断")) {
			t.Fatal(c)
		}
	}
	req.ScopeJSON = "broken"
	if _, err := s.RecallKnowledgeMaterials(ctx, profile, "pod", req, provider.KnowledgeTopic{}); err == nil {
		t.Fatal("broken scope accepted")
	}
}

func TestWeightedKnowledgeSearchUsesActualTitleAndQuestionColumns(t *testing.T) {
	s, article, _, _ := readyKnowledgeRevisionFixture(t)
	ctx := t.Context()
	if _, err := s.DB.ExecContext(ctx, `UPDATE knowledge_articles SET topic_json='{"question":"lexical_question_only"}' WHERE id=?`, article.ID); err != nil {
		t.Fatal(err)
	}
	got, err := s.SearchKnowledge(ctx, KnowledgeSearchQuery{Text: "lexical_question_only", Kind: "article"})
	if err != nil || got.Total != 2 {
		t.Fatal(got.Total, err)
	}
	for _, x := range []struct{ id, title, body string }{{"title-hit", "rareterm", "无关正文"}, {"body-hit", "无关标题", "rareterm"}} {
		if _, err := s.DB.ExecContext(ctx, `INSERT INTO knowledge_search_docs(key,kind,object_id,title,body,created_at,updated_at)VALUES(?,'document',?,?,?,'2026-10-01','2026-10-01')`, x.id, x.id, x.title, x.body); err != nil {
			t.Fatal(err)
		}
	}
	got, err = s.SearchKnowledge(ctx, KnowledgeSearchQuery{Text: "rareterm", Kind: "document"})
	if err != nil || len(got.Hits) != 2 || got.Hits[0].ObjectID != "title-hit" {
		t.Fatal(got.Hits, err)
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE knowledge_search_docs SET title='other',body='changed' WHERE key='title-hit'`); err != nil {
		t.Fatal(err)
	}
	got, err = s.SearchKnowledge(ctx, KnowledgeSearchQuery{Text: "rareterm", Kind: "document"})
	if err != nil || got.Total != 1 {
		t.Fatal(got.Total, err)
	}
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM knowledge_search_docs WHERE key='body-hit'`); err != nil {
		t.Fatal(err)
	}
	got, err = s.SearchKnowledge(ctx, KnowledgeSearchQuery{Text: "rareterm", Kind: "document"})
	if err != nil || got.Total != 0 {
		t.Fatal(got.Total, err)
	}
}
