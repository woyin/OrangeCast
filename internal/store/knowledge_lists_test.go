package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
)

func TestKnowledgeListPagesReachOldItemsWithoutBodies(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	profile, err := s.EnsureDefaultEditorialProfile(ctx)
	if err != nil {
		t.Fatal(err)
	}
	batch, err := s.EnsureKnowledgeDiscoveryBatch(ctx, profile.ID, "pod", "model", provider.KnowledgeArticleRequest{PromptVersion: provider.KnowledgeArticlePromptVersion}, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 121; i++ {
		id := fmt.Sprintf("item-%03d", i)
		title := fmt.Sprintf("历史问题%03d", i)
		topic := provider.KnowledgeTopic{Title: title, Question: "问题" + id, Thesis: "主旨" + id, MaterialIDs: []string{"m"}, Sufficient: true, Score: 90}
		if _, err = tx.ExecContext(ctx, `INSERT INTO knowledge_articles(id,profile_id,input_hash,input_json,status,stage,title,topic_json,provider,model,created_at,updated_at)VALUES(?,?,?,?,'ready','review',?,?,'pod','model',?,?)`, id, profile.ID, id, strings.Repeat("secret-body", 10000), title, jsonString(topic), "2026-01-01 00:00:00", "2026-01-01 00:00:00"); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO knowledge_topic_candidates(id,batch_id,direction_hash,topic_json,status,created_at)VALUES(?,?,?,?,'waiting',?)`, id, batch.ID, id, jsonString(topic), "2026-01-01 00:00:00"); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for n := 1; n <= 7; n++ {
		page, e := s.ListKnowledgeArticlesPage(ctx, KnowledgeListQuery{Page: n, PerPage: 20})
		if e != nil || page.Total != 121 {
			t.Fatal(e, page.Total)
		}
		for _, v := range page.Items {
			if seen[v.ID] || v.InputJSON != "" || v.BlocksJSON != "" {
				t.Fatal("duplicate item or body read", v.ID)
			}
			seen[v.ID] = true
		}
		candidates, e := s.ListKnowledgeTopicCandidatesPage(ctx, KnowledgeListQuery{Page: n, PerPage: 20})
		if e != nil || candidates.Total != 121 || len(candidates.Items) != len(page.Items) {
			t.Fatal(e, candidates.Total)
		}
	}
	if len(seen) != 121 || !seen["item-000"] || !seen["item-120"] {
		t.Fatal("old/new metadata unreachable")
	}
	filtered, err := s.ListKnowledgeArticlesPage(ctx, KnowledgeListQuery{Text: "历史问题000", Status: "ready"})
	if err != nil || len(filtered.Items) != 1 {
		t.Fatal(err, filtered.Total)
	}
	if _, err = s.ListKnowledgeArticlesPage(ctx, KnowledgeListQuery{Page: 10001}); err == nil {
		t.Fatal("unbounded page")
	}
	if _, err = s.ListKnowledgeTopicCandidatesPage(ctx, KnowledgeListQuery{Text: strings.Repeat("字", 201)}); err == nil {
		t.Fatal("unbounded query")
	}
	old, _ := s.GetKnowledgeArticle(ctx, "item-000")
	var topic provider.KnowledgeTopic
	_ = json.Unmarshal([]byte(old.TopicJSON), &topic)
	if err = s.CheckKnowledgeDirection(ctx, profile.ID, "other-article", topic); !errors.Is(err, ErrConflict) {
		t.Fatal("old duplicate escaped UI limit", err)
	}
}

func TestKnowledgeSourcePickerSearchesBeyond500AndPreservesSelection(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	p, err := s.CreatePodcast(ctx, "https://example.test/source-list.xml", "列表", "", "")
	if err != nil {
		t.Fatal(err)
	}
	episodes := []models.Episode{}
	for i := 0; i < 521; i++ {
		episodes = append(episodes, models.Episode{GUID: fmt.Sprint(i), Title: fmt.Sprintf("来源%03d", i), AudioURL: "https://example.test/audio"})
	}
	if _, err = s.MergeEpisodes(ctx, p.ID, episodes); err != nil {
		t.Fatal(err)
	}
	all, err := s.ListEpisodes(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	selected := ""
	target := ""
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, ep := range all {
		if ep.Title == "来源000" {
			selected = "episode:" + ep.ID
		}
		if ep.Title == "来源520" {
			target = ep.ID
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO knowledge_search_docs(key,kind,object_id,source_type,source_id,title,body,created_at,updated_at)VALUES(?,'original',?,'episode',?,?,?,'2026-01-01','2026-01-01')`, ep.ID, ep.ID, ep.ID, ep.Title, "fixture"); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	found, err := s.SearchKnowledgeSources(ctx, KnowledgeListQuery{Text: "来源520"}, selected)
	if err != nil || found.Total != 1 || len(found.Items) != 2 || found.Items[1].SourceID != target || found.SelectedUnavailable {
		t.Fatal(err, found)
	}
	seen := map[string]bool{}
	for page := 1; page <= 27; page++ {
		found, err = s.SearchKnowledgeSources(ctx, KnowledgeListQuery{Page: page}, "")
		if err != nil || found.Total != 521 {
			t.Fatal(err, found.Total)
		}
		for _, v := range found.Items {
			if seen[v.SourceID] {
				t.Fatal("unstable tied pagination")
			}
			seen[v.SourceID] = true
		}
	}
	if len(seen) != 521 {
		t.Fatal(len(seen))
	}
	found, err = s.SearchKnowledgeSources(ctx, KnowledgeListQuery{}, "episode:gone")
	if err != nil || !found.SelectedUnavailable {
		t.Fatal(err, found)
	}
	if _, err = s.SearchKnowledgeSources(ctx, KnowledgeListQuery{}, "malformed"); err == nil {
		t.Fatal("malformed identity")
	}
}

func TestKnowledgeDirectionFallbackUsesTimeAndIndependentPools(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	profile, err := s.EnsureDefaultEditorialProfile(ctx)
	if err != nil {
		t.Fatal(err)
	}
	batch, err := s.EnsureKnowledgeDiscoveryBatch(ctx, profile.ID, "pod", "model", provider.KnowledgeArticleRequest{}, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []struct{ id, at string }{{"zz-old", "2020-01-01"}, {"aa-new", "2026-01-01"}} {
		topic := jsonString(provider.KnowledgeTopic{Title: v.id, Question: v.id, Thesis: v.id})
		if _, err = s.DB.ExecContext(ctx, `INSERT INTO knowledge_articles(id,profile_id,input_hash,input_json,status,title,topic_json,provider,model,created_at,updated_at)VALUES(?,?,?,'{}','ready',?,?,'pod','model',?,?)`, v.id, profile.ID, v.id, v.id, topic, v.at, v.at); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = s.DB.ExecContext(ctx, `INSERT INTO knowledge_topic_candidates(id,batch_id,direction_hash,topic_json,status,created_at)VALUES('pending',?,'hash',?,'waiting','2027-01-01')`, batch.ID, jsonString(provider.KnowledgeTopic{Title: "pending", Question: "待写问题"})); err != nil {
		t.Fatal(err)
	}
	history, err := s.knowledgeDirectionHistory(ctx, profile.ID, provider.KnowledgeTopic{})
	if err != nil || len(history) != 3 || history[0].Title != "pending" || history[1].Title != "aa-new" || history[2].Title != "zz-old" {
		t.Fatal(err, history)
	}
}

func TestKnowledgeParagraphIdentityIsOwnedByApplicationAndOldBodyIsImmutable(t *testing.T) {
	s, article, _, _ := readyKnowledgeRevisionFixture(t)
	ctx := t.Context()
	old, err := s.GetKnowledgeRevision(ctx, article.ID, article.WorkingRevision)
	if err != nil {
		t.Fatal(err)
	}
	var req provider.KnowledgeArticleRequest
	var blocks []provider.KnowledgeBlock
	if err = json.Unmarshal([]byte(old.InputJSON), &req); err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal([]byte(old.BlocksJSON), &blocks); err != nil {
		t.Fatal(err)
	}
	if blocks[0].ID == "" || blocks[0].ID == blocks[1].ID {
		t.Fatal("paragraph identities missing")
	}
	firstID := blocks[0].ID
	blocks[0].Text += " Owner补充条件。"
	blocks[1].ID = "a-model-or-other-article-id"
	next, err := s.SaveKnowledgeDraft(ctx, article.ID, old.Revision, old.Title, blocks, req)
	if err != nil {
		t.Fatal(err)
	}
	var saved []provider.KnowledgeBlock
	_ = json.Unmarshal([]byte(next.BlocksJSON), &saved)
	if saved[0].ID != firstID || saved[1].ID == blocks[1].ID || saved[1].ID == "" {
		t.Fatal("untrusted identity preserved", saved)
	}
	unchanged, err := s.GetKnowledgeRevision(ctx, article.ID, old.Revision)
	if err != nil || unchanged.ContentHash != old.ContentHash || unchanged.BlocksJSON != old.BlocksJSON || !unchanged.Passed {
		t.Fatal("old passed body rewritten", err)
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	assigned, err := assignKnowledgeBlockIDs(ctx, tx, article.ID, next.Revision, saved, false)
	if err != nil {
		t.Fatal(err)
	}
	if assigned[0].ID == saved[0].ID || assigned[1].ID == saved[1].ID {
		t.Fatal("model IDs treated as trusted parent IDs")
	}
}
