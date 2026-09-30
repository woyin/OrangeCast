package store

import (
	"encoding/json"
	"fmt"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/woyin/orangecast/internal/evalset"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
)

func TestUnifiedKnowledgeSearchIdentityUpdatesAndFilters(t *testing.T) {
	s, article, _, noteID := readyKnowledgeRevisionFixture(t)
	ctx := t.Context()
	doc, err := s.CreatePastedDocument(ctx, "记忆学习", "主动回忆有助于发现理解的缺口。\n\nMemory retrieval practice preserves context.")
	if err != nil {
		t.Fatal(err)
	}
	segs := DocumentSegments(doc)
	note, err := s.CreateOwnerNote(ctx, models.OwnerNote{SourceType: "document", SourceID: doc.ID, Kind: "source_note", Content: "记忆学习要主动回忆。", CitationsJSON: jsonString([]string{segs[0].ID})})
	if err != nil {
		t.Fatal(err)
	}
	checks := []struct {
		query, kind string
		minimum     int
	}{{"记忆", "document", 1}, {"主动回忆", "source_note", 1}, {"retrieval practice", "document", 2}, {"保留", "original", 1}, {"上下文", "keypoint", 1}, {"自己的理解", "owner_reflection", 1}, {"来源", "article", 1}}
	for _, tc := range checks {
		result, err := s.SearchKnowledge(ctx, KnowledgeSearchQuery{Text: tc.query, Kind: tc.kind})
		if err != nil || result.Total < tc.minimum {
			t.Fatalf("%+v -> %+v %v", tc, result, err)
		}
		for _, hit := range result.Hits {
			if hit.Key == "" || hit.Snippet == "" {
				t.Fatal("unanchored hit", hit)
			}
		}
	}
	scoped, err := s.SearchKnowledge(ctx, KnowledgeSearchQuery{Text: "回忆", SourceType: "document", SourceID: doc.ID})
	if err != nil || scoped.Total < 2 {
		t.Fatal(scoped, err)
	}
	paged, err := s.SearchKnowledge(ctx, KnowledgeSearchQuery{Text: "回忆", SourceID: doc.ID, Page: 2, PerPage: 1})
	if err != nil || len(paged.Hits) != 1 || paged.Total < 2 {
		t.Fatal(paged, err)
	}
	if _, err := s.UpdateOwnerNote(ctx, note.ID, "新的课堂表达", note.CitationsJSON, "[]", note.Revision); err != nil {
		t.Fatal(err)
	}
	changed, err := s.SearchKnowledge(ctx, KnowledgeSearchQuery{Text: "记忆学习要主动回忆", Kind: "source_note"})
	if err != nil || changed.Total != 0 {
		t.Fatal("old note still indexed", changed, err)
	}
	changed, err = s.SearchKnowledge(ctx, KnowledgeSearchQuery{Text: "课堂", Kind: "source_note"})
	if err != nil || changed.Total != 1 {
		t.Fatal(changed, err)
	}
	original, _ := s.GetKnowledgeRevision(ctx, article.ID, 1)
	var req provider.KnowledgeArticleRequest
	var blocks []provider.KnowledgeBlock
	_ = json.Unmarshal([]byte(original.InputJSON), &req)
	_ = json.Unmarshal([]byte(original.BlocksJSON), &blocks)
	blocks[0].Text = "未审草稿里的独有名词：并发语义"
	if _, err := s.SaveKnowledgeDraft(ctx, article.ID, 1, "工作稿", blocks, req); err != nil {
		t.Fatal(err)
	}
	hidden, err := s.SearchKnowledge(ctx, KnowledgeSearchQuery{Text: "并发语义", Kind: "article"})
	if err != nil || hidden.Total != 0 {
		t.Fatal("draft leaked by default", hidden, err)
	}
	draft, err := s.SearchKnowledge(ctx, KnowledgeSearchQuery{Text: "并发语义", Kind: "article", IncludeDrafts: true})
	if err != nil || draft.Total != 1 || draft.Hits[0].Revision != 2 {
		t.Fatal(draft, err)
	}
	n, _ := s.GetOwnerNote(ctx, noteID)
	if err := s.DeleteOwnerNote(ctx, noteID, n.Revision); err != nil {
		t.Fatal(err)
	}
	invalid, err := s.SearchKnowledge(ctx, KnowledgeSearchQuery{Text: "来源", Kind: "article"})
	if err != nil || invalid.Total != 0 {
		t.Fatal("invalid article default search", invalid, err)
	}
	history, err := s.SearchKnowledge(ctx, KnowledgeSearchQuery{Text: "来源", Kind: "article", IncludeHistory: true})
	if err != nil || history.Total == 0 {
		t.Fatal("history not available", history, err)
	}
	before, err := s.SearchKnowledge(ctx, KnowledgeSearchQuery{Text: "课堂"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RebuildKnowledgeSearch(ctx); err != nil {
		t.Fatal(err)
	}
	after, err := s.SearchKnowledge(ctx, KnowledgeSearchQuery{Text: "课堂"})
	if err != nil || after.Total != before.Total {
		t.Fatal("rebuild diverged", before, after, err)
	}
	if err := s.DeleteSourceRows(ctx, models.SourceDocument, doc.ID); err != nil {
		t.Fatal(err)
	}
	deleted, err := s.SearchKnowledge(ctx, KnowledgeSearchQuery{Text: "课堂"})
	if err != nil || deleted.Total != 0 {
		t.Fatal("purge left indexed text", deleted, err)
	}
	sources, err := s.ListKnowledgeSearchSources(ctx)
	if err != nil || len(sources) == 0 {
		t.Fatal(sources, err)
	}
	for _, q := range []KnowledgeSearchQuery{{Text: strings.Repeat("字", 201)}, {Kind: "fake"}, {SourceType: "fake"}, {Page: 10001}, {From: "yesterday"}, {From: "2026-10-02", Until: "2026-10-01"}} {
		if _, err := s.SearchKnowledge(ctx, q); err == nil {
			t.Fatal("unbounded query accepted", q)
		}
	}
}

func TestKnowledgeSearchFixedRecallAndTenThousandP95(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	samples := []string{
		"记忆巩固需要区分睡眠与主动回忆。", "retrieval practice supports remembering; 主动回忆。", "旧笔记中的理解：保留来源归因。",
		"集中学习与间隔学习存在条件差异。", "我的理解不等于来源事实。", "材料不足时不补充数字。", "避免同义标题的重复文章。",
		"新增反方证据可以支持有增量的续篇。", "依据过期之后必须重新核查。", "<script>alert(1)</script>广告不是来源事实。",
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO knowledge_search_docs(key,kind,object_id,title,body,tokens,created_at,updated_at)VALUES(?,'keypoint',?,'固定检索样本',?,cwp_search_tokens(?),'2026-09-30','2026-09-30')`)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10000; i++ {
		body := fmt.Sprintf("无关资料编号%d：项目预算，播放控制，旅行记录。", i)
		if i < len(samples) {
			body = samples[i]
		}
		if _, err := stmt.ExecContext(ctx, fmt.Sprintf("eval:%d", i), fmt.Sprint(i), body, body); err != nil {
			t.Fatal(err)
		}
	}
	if err := stmt.Close(); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	queries := evalset.PersonalLearningCases
	correct := 0
	var durations []time.Duration
	for round := 0; round < 10; round++ {
		for _, q := range queries {
			start := time.Now()
			result, err := s.SearchKnowledge(ctx, KnowledgeSearchQuery{Text: q.Query, PerPage: 10})
			durations = append(durations, time.Since(start))
			if err != nil {
				t.Fatal(err)
			}
			if round == 0 {
				for _, hit := range result.Hits {
					if hit.ObjectID == q.ExpectedMaterialIDs[0] {
						correct++
						break
					}
				}
			}
		}
	}
	for round := 0; round < 10; round++ {
		start := time.Now()
		result, err := s.SearchKnowledge(ctx, KnowledgeSearchQuery{Text: "项目预算", PerPage: 10})
		durations = append(durations, time.Since(start))
		if err != nil || result.Total != 9990 {
			t.Fatalf("broad query %+v %v", result, err)
		}
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	p95 := durations[(len(durations)*95/100)-1]
	recall := float64(correct) / float64(len(queries))
	t.Logf("self-authored samples=10000 queries=%d runs=%d %s/%s Recall@10=%.0f%% p95=%s", len(queries), len(durations), runtime.GOOS, runtime.GOARCH, recall*100, p95)
	if recall < 0.9 {
		t.Fatalf("recall below 90%%: %.2f", recall)
	}
	// Race instrumentation changes SQLite/UDF timing substantially. Recall and
	// broad-query correctness still run; the latency gate uses the normal build.
	if !searchRaceInstrumented && p95 > 300*time.Millisecond {
		t.Fatalf("p95 above 300ms: %s", p95)
	}
}
