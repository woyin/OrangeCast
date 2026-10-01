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

func createQuestion(t *testing.T, s *Store) *LearningQuestion {
	t.Helper()
	q, err := s.CreateLearningQuestion(t.Context(), LearningQuestion{Body: "如何核对来源并表达自己的理解？", Goal: "写出可解释的判断", TargetDate: "2026-10-10"})
	if err != nil {
		t.Fatal(err)
	}
	return q
}
func questionChange(t *testing.T, s *Store, q *LearningQuestion, c LearningQuestionChange) *LearningQuestion {
	t.Helper()
	next, err := s.ChangeLearningQuestion(t.Context(), q.ID, q.Revision, c)
	if err != nil {
		t.Fatal(err)
	}
	return next
}

func TestLearningQuestionScopeFreezeCASAndOwnerState(t *testing.T) {
	s, profile, ep, note := knowledgeStoreFixture(t)
	ctx := t.Context()
	q := createQuestion(t, s)
	empty, _, _, err := s.BuildKnowledgeDiscoveryRequest(ctx, profile, "pod", KnowledgeScope{QuestionID: q.ID}, false)
	if err != nil || len(empty.Materials) != 0 || empty.Question == nil {
		t.Fatal(empty, err)
	}
	q = questionChange(t, s, q, LearningQuestionChange{Action: "suggest", Link: provider.LearningQuestionLink{Kind: "source", SourceType: "episode", SourceID: ep}})
	frozen, err := s.FreezeLearningQuestion(ctx, q.ID, false)
	if err != nil || len(frozen.Links) != 0 {
		t.Fatal(frozen, err)
	}
	suggestions, err := s.ListLearningQuestionRelations(ctx, q.ID)
	if err != nil || len(suggestions) != 1 || suggestions[0].State != "suggested" {
		t.Fatal(suggestions, err)
	}
	req, _, _, err := s.BuildKnowledgeDiscoveryRequest(ctx, profile, "pod", KnowledgeScope{QuestionID: q.ID, MaterialIDs: []string{note}}, false)
	if err != nil || len(req.Materials) != 0 {
		t.Fatal(req, err)
	}
	q = questionChange(t, s, q, LearningQuestionChange{Action: "link", Link: provider.LearningQuestionLink{Kind: "source", SourceType: "episode", SourceID: ep}})
	duplicate := questionChange(t, s, q, LearningQuestionChange{Action: "link", Link: provider.LearningQuestionLink{Kind: "source", SourceType: "episode", SourceID: ep}})
	if duplicate.Revision != q.Revision {
		t.Fatal("duplicate membership changed revision")
	}
	q = questionChange(t, s, q, LearningQuestionChange{Action: "suggest", Link: provider.LearningQuestionLink{Kind: "source", SourceType: "episode", SourceID: ep}})
	rels, _ := s.ListLearningQuestionRelations(ctx, q.ID)
	if rels[0].State != "confirmed" {
		t.Fatal("suggestion downgraded owner confirmation")
	}
	req, _, _, err = s.BuildKnowledgeDiscoveryRequest(ctx, profile, "pod", KnowledgeScope{QuestionID: q.ID}, false)
	if err != nil || len(req.Materials) != 2 {
		t.Fatal(len(req.Materials), err)
	}
	article, created, err := s.ReserveKnowledgeArticle(ctx, profile, "pod", "model", req, false)
	if err != nil || !created {
		t.Fatal(created, err)
	}
	attached, err := s.ListLearningQuestionArticles(ctx, q.ID)
	if err != nil || len(attached) != 1 || attached[0].ID != article.ID {
		t.Fatal(attached, err)
	}
	filtered, e := s.ListKnowledgeArticlesPage(ctx, KnowledgeListQuery{QuestionID: q.ID})
	if e != nil || filtered.Total != 1 || filtered.Items[0].InputJSON != "" {
		t.Fatal(filtered, e)
	}
	none, e := s.ListKnowledgeArticlesPage(ctx, KnowledgeListQuery{QuestionID: "another"})
	if e != nil || none.Total != 0 {
		t.Fatal(none, e)
	}
	oldRevision := q.Revision
	q = questionChange(t, s, q, LearningQuestionChange{Action: "edit", Body: "更具体的新问题", Goal: "新目标"})
	if _, err = s.ChangeLearningQuestion(ctx, q.ID, oldRevision, LearningQuestionChange{Action: "status", Status: "resolved"}); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	if err = s.CheckLearningQuestionExecution(ctx, req.Question, true); err != nil {
		t.Fatal("old frozen revision should remain usable", err)
	}
	v, err := s.GetKnowledgeArticle(ctx, article.ID)
	var persisted provider.KnowledgeArticleRequest
	if err != nil || json.Unmarshal([]byte(v.InputJSON), &persisted) != nil || persisted.Question.Body != req.Question.Body || persisted.Question.Revision != oldRevision {
		t.Fatal("frozen question rewritten", err)
	}
	for _, state := range []string{"paused", "active", "resolved", "active", "archived"} {
		q = questionChange(t, s, q, LearningQuestionChange{Action: "status", Status: state})
		e := s.CheckLearningQuestionExecution(ctx, req.Question, true)
		_, fe := s.FreezeLearningQuestion(ctx, q.ID, true)
		if state == "active" {
			if e != nil || fe != nil {
				t.Fatal(e, fe)
			}
		} else if !errors.Is(e, ErrConflict) || !errors.Is(fe, ErrConflict) {
			t.Fatal(state, e, fe)
		}
	}
	newer, _, _, err := s.BuildKnowledgeDiscoveryRequest(ctx, profile, "pod", KnowledgeScope{QuestionID: q.ID}, false)
	if err != nil || newer.Question.Revision == req.Question.Revision || newer.Question.Body != q.Body {
		t.Fatal(err, newer.Question)
	}
	if _, _, err = s.ReserveKnowledgeArticle(ctx, profile, "pod", "model", req, false); !errors.Is(err, ErrConflict) {
		t.Fatal("old input must not be newly admitted", err)
	}
	list, err := s.ListLearningQuestions(ctx, "更具体", "archived")
	if err != nil || len(list) != 1 {
		t.Fatal(list, err)
	}
	ops, err := s.ListLearningQuestionOperations(ctx, q.ID)
	if err != nil || len(ops) < 7 {
		t.Fatal(ops, err)
	}
	if _, err = s.ChangeLearningQuestion(ctx, q.ID, q.Revision, LearningQuestionChange{Action: "delete"}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.GetOwnerNote(ctx, note); err != nil {
		t.Fatal("question delete removed note", err)
	}
	if _, err = s.GetKnowledgeArticle(ctx, article.ID); err != nil {
		t.Fatal("question delete removed article", err)
	}
	if err = s.CheckLearningQuestionExecution(ctx, req.Question, false); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}

func TestLearningQuestionAtomicNoteAndPurge(t *testing.T) {
	s, _, ep, _ := knowledgeStoreFixture(t)
	ctx := t.Context()
	q := createQuestion(t, s)
	var before, after int
	s.DB.QueryRow(`SELECT count(*) FROM owner_notes`).Scan(&before)
	n, err := s.CreateLearningQuestionNote(ctx, q.ID, q.Revision, models.OwnerNote{SourceType: "episode", SourceID: ep, Content: "我现在的理解。"})
	if err != nil || n.Kind != "owner_reflection" || n.CitationsJSON != "[]" || !strings.Contains(n.AnchorJSON, "no_position") {
		t.Fatal(n, err)
	}
	links, _ := s.ListLearningQuestionRelations(ctx, q.ID)
	if len(links) != 1 || links[0].ObjectID != n.ID {
		t.Fatal(links)
	}
	if _, err = s.CreateLearningQuestionNote(ctx, q.ID, q.Revision, models.OwnerNote{SourceType: "episode", SourceID: ep, Content: "旧编辑器保存"}); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	s.DB.QueryRow(`SELECT count(*) FROM owner_notes`).Scan(&after)
	if after != before+1 {
		t.Fatal("stale note left an orphan")
	}
	q, _ = s.GetLearningQuestion(ctx, q.ID)
	if _, err = s.CreateLearningQuestionNote(ctx, q.ID, q.Revision, models.OwnerNote{SourceType: "episode", SourceID: ep, Content: "错误引用", ReferencesJSON: `["unknown"]`}); err == nil {
		t.Fatal("invalid reference accepted")
	}
	q = questionChange(t, s, q, LearningQuestionChange{Action: "link", Link: provider.LearningQuestionLink{Kind: "source", SourceType: "episode", SourceID: ep}})
	old := q.Revision
	if _, err = s.DB.Exec(`DELETE FROM episodes WHERE id=?`, ep); err != nil {
		t.Fatal(err)
	}
	links, err = s.ListLearningQuestionRelations(ctx, q.ID)
	if err != nil || len(links) != 0 {
		t.Fatal(links, err)
	}
	q, _ = s.GetLearningQuestion(ctx, q.ID)
	if q.Revision <= old {
		t.Fatal("purge did not invalidate editor")
	}
}

func TestLearningQuestionTargetsPermissionsAndFrozenRecall(t *testing.T) {
	s, profile, ep, note := knowledgeStoreFixture(t)
	ctx := t.Context()
	q := createQuestion(t, s)
	all, _, _ := s.BuildKnowledgeArticleRequest(ctx, profile, "pod")
	var kp provider.KnowledgeMaterial
	for _, m := range all.Materials {
		if m.Kind == "keypoint" {
			kp = m
		}
	}
	for _, l := range []provider.LearningQuestionLink{{Kind: "note", ObjectID: note}, {Kind: "keypoint", ObjectID: kp.ID}, {Kind: "evidence", ObjectID: kp.SnapshotID}} {
		q = questionChange(t, s, q, LearningQuestionChange{Action: "link", Link: l})
	}
	frozen, err := s.FreezeLearningQuestion(ctx, q.ID, false)
	if err != nil || len(frozen.Links) != 3 {
		t.Fatal(frozen, err)
	}
	other, err := s.CreateOwnerNote(ctx, models.OwnerNote{SourceType: "episode", SourceID: ep, Kind: "owner_reflection", Content: " unrelated material  unrelated "})
	if err != nil {
		t.Fatal(err)
	}
	req, _, _, err := s.BuildKnowledgeDiscoveryRequest(ctx, profile, "pod", KnowledgeScope{QuestionID: q.ID}, false)
	if err != nil {
		t.Fatal(err)
	}
	// An evidence relation permits only the exact frozen snapshot; no-position notes freeze to current source snapshot.
	for _, m := range req.Materials {
		if !questionAllowsMaterial(req.Question, m) {
			t.Fatal("out of scope", m.ID)
		}
	}
	q = questionChange(t, s, q, LearningQuestionChange{Action: "unlink", Link: provider.LearningQuestionLink{Kind: "evidence", ObjectID: kp.SnapshotID}})
	restricted, _, _, err := s.BuildKnowledgeDiscoveryRequest(ctx, profile, "pod", KnowledgeScope{QuestionID: q.ID}, false)
	if err != nil || len(restricted.Materials) != 2 {
		t.Fatal(len(restricted.Materials), err)
	}
	recalled, err := s.RecallKnowledgeMaterials(ctx, profile, "pod", restricted, provider.KnowledgeTopic{Question: "unrelated material", Thesis: "理解来源"})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range recalled.Materials {
		if m.ID == other.ID {
			t.Fatal("unrelated material silently attached")
		}
	}
	if _, err = s.DB.Exec(`UPDATE episodes SET model_data_policy='local_only' WHERE id=?`, ep); err != nil {
		t.Fatal(err)
	}
	denied, _, _, err := s.BuildKnowledgeDiscoveryRequest(ctx, profile, "pod", KnowledgeScope{QuestionID: q.ID}, false)
	if err != nil || len(denied.Materials) != 0 {
		t.Fatal("question membership expanded permission", err, denied)
	}
	if err = s.CheckKnowledgeMaterials(ctx, profile, "pod", restricted.Materials); err == nil {
		t.Fatal("live policy revocation bypassed")
	}
}

func TestLearningQuestionValidationThemeAndAllRelations(t *testing.T) {
	s, profile, ep, note := knowledgeStoreFixture(t)
	ctx := t.Context()
	for _, q := range []LearningQuestion{{}, {Body: "x", TargetDate: "2026-99-01"}, {Body: strings.Repeat("x", 2001)}, {Body: "x", Goal: strings.Repeat("x", 5001)}, {Body: "x", ThemeID: "unknown"}} {
		if _, err := s.CreateLearningQuestion(ctx, q); err == nil {
			t.Fatal("bad question accepted")
		}
	}
	theme, err := s.CreateTheme(ctx, models.Theme{EditorialProfileID: profile, Name: "学习", Status: "confirmed"})
	if err != nil {
		t.Fatal(err)
	}
	q, err := s.CreateLearningQuestion(ctx, LearningQuestion{Body: "主题问题", ThemeID: theme.ID})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []LearningQuestionChange{{Action: "unknown"}, {Action: "status", Status: "mastered"}, {Action: "link", Link: provider.LearningQuestionLink{Kind: "unknown"}}, {Action: "link", Link: provider.LearningQuestionLink{Kind: "note", ObjectID: "unknown"}}, {Action: "unlink", Link: provider.LearningQuestionLink{Kind: "note", ObjectID: "unknown"}}, {Action: "edit", Body: "x", ThemeID: "unknown"}, {Action: "link", Link: provider.LearningQuestionLink{Kind: "source", SourceType: "bad", SourceID: ep}}} {
		if _, err = s.ChangeLearningQuestion(ctx, q.ID, q.Revision, c); err == nil {
			t.Fatal("bad change accepted", c)
		}
	}
	if _, err = s.GetLearningQuestion(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	for _, args := range [][2]string{{"", "mastered"}, {strings.Repeat("x", 201), ""}} {
		if _, err = s.ListLearningQuestions(ctx, args[0], args[1]); err == nil {
			t.Fatal("invalid search")
		}
	}
	req, _, _ := s.BuildKnowledgeArticleRequest(ctx, profile, "pod")
	a, _, err := s.ReserveKnowledgeArticle(ctx, profile, "pod", "model", req, false)
	if err != nil {
		t.Fatal(err)
	}
	q = questionChange(t, s, q, LearningQuestionChange{Action: "link", Link: provider.LearningQuestionLink{Kind: "article", ObjectID: a.ID}})
	f, err := s.FreezeLearningQuestion(ctx, q.ID, false)
	if err != nil || len(f.Links) != 1 || len(f.Links[0].MaterialIDs) != 2 {
		t.Fatal(f, err)
	}
	scoped, _, _, err := s.BuildKnowledgeDiscoveryRequest(ctx, profile, "pod", KnowledgeScope{QuestionID: q.ID}, false)
	if err != nil || len(scoped.Materials) != 2 {
		t.Fatal(len(scoped.Materials), err)
	}
	for _, m := range scoped.Materials {
		if !questionAllowsMaterial(f, m) {
			t.Fatal(m)
		}
	}
	prefs, _ := s.GetKnowledgeArticleSettings(ctx)
	prefs.QuestionID = q.ID
	if err = s.SetKnowledgeArticleSettings(ctx, prefs); err != nil {
		t.Fatal(err)
	}
	saved, _ := s.GetKnowledgeArticleSettings(ctx)
	if saved.QuestionID != q.ID {
		t.Fatal(saved)
	}
	q = questionChange(t, s, q, LearningQuestionChange{Action: "link", Link: provider.LearningQuestionLink{Kind: "note", ObjectID: note}})
	s.DB.Exec(`DELETE FROM owner_notes WHERE id=?`, note)
	relations, _ := s.ListLearningQuestionRelations(ctx, q.ID)
	if len(relations) != 1 || relations[0].Kind != "article" {
		t.Fatal(relations)
	}
	q, _ = s.GetLearningQuestion(ctx, q.ID)
	q = questionChange(t, s, q, LearningQuestionChange{Action: "edit", Body: "保留目标", Goal: "g", ThemeID: theme.ID})
	if q.Goal != "g" {
		t.Fatal(q)
	}
	if _, err = s.ChangeLearningQuestion(ctx, q.ID, q.Revision, LearningQuestionChange{Action: "delete"}); err != nil {
		t.Fatal(err)
	}
	saved, _ = s.GetKnowledgeArticleSettings(ctx)
	if saved.QuestionID != "" {
		t.Fatal("deleted scope not cleared")
	}
	if err = s.CheckLearningQuestionExecution(ctx, nil, true); err != nil {
		t.Fatal(err)
	}
	prefs.QuestionID = "missing"
	if err = s.SetKnowledgeArticleSettings(ctx, prefs); err == nil {
		t.Fatal("missing scope accepted")
	}
}

func TestLearningQuestionBoundedConfirmedRelations(t *testing.T) {
	s, _, ep, _ := knowledgeStoreFixture(t)
	ctx := t.Context()
	q := createQuestion(t, s)
	for i := 0; i < 200; i++ {
		_, err := s.DB.Exec(`INSERT INTO learning_question_links(question_id,kind,object_id,source_type,source_id,state)VALUES(?,'note',?,'episode',?,'suggested')`, q.ID, fmt.Sprint("synthetic-", i), ep)
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.ChangeLearningQuestion(ctx, q.ID, q.Revision, LearningQuestionChange{Action: "link", Link: provider.LearningQuestionLink{Kind: "source", SourceType: "episode", SourceID: ep}}); err == nil {
		t.Fatal("link limit bypass")
	}
	if _, err := s.CreateLearningQuestionNote(ctx, q.ID, q.Revision, models.OwnerNote{SourceType: "episode", SourceID: ep, Content: "beyond capacity"}); err == nil {
		t.Fatal("atomic note limit bypass")
	}
}

func TestLearningQuestionAutomaticCandidatesStayInFrozenScope(t *testing.T) {
	s, profile, ep, _ := knowledgeStoreFixture(t)
	ctx := t.Context()
	q := createQuestion(t, s)
	q = questionChange(t, s, q, LearningQuestionChange{Action: "link", Link: provider.LearningQuestionLink{Kind: "source", SourceType: "episode", SourceID: ep}})
	req, last, _, err := s.BuildKnowledgeDiscoveryRequest(ctx, profile, "pod", KnowledgeScope{QuestionID: q.ID}, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err = s.BuildKnowledgeDiscoveryRequest(ctx, profile, "pod", KnowledgeScope{QuestionID: q.ID, ExpectedQuestionRevision: q.Revision - 1}, false); !errors.Is(err, ErrConflict) {
		t.Fatal("stale generate editor", err)
	}
	batch, err := s.EnsureKnowledgeDiscoveryBatch(ctx, profile, "pod", "model", req, last, true)
	if err != nil {
		t.Fatal(err)
	}
	topic := provider.KnowledgeTopic{Title: "独立方向", Question: "如何保留明确的来源上下文？", Thesis: "核对资料再表达", Score: 90, Sufficient: true}
	for _, m := range req.Materials {
		topic.MaterialIDs = append(topic.MaterialIDs, m.ID)
	}
	if _, err = s.DB.Exec(`INSERT INTO knowledge_topic_candidates(id,batch_id,direction_hash,topic_json,status)VALUES('scoped',?,'distinct',?,'waiting')`, batch.ID, jsonString(topic)); err != nil {
		t.Fatal(err)
	}
	filtered, e := s.ListKnowledgeTopicCandidatesPage(ctx, KnowledgeListQuery{QuestionID: q.ID})
	if e != nil || filtered.Total != 1 {
		t.Fatal(filtered, e)
	}
	none, e := s.ListKnowledgeTopicCandidatesPage(ctx, KnowledgeListQuery{QuestionID: "another"})
	if e != nil || none.Total != 0 {
		t.Fatal(none, e)
	}
	if id, err := s.NextAutomaticKnowledgeCandidate(ctx, profile); err != nil || id != "" {
		t.Fatal("question candidate leaked into global", id, err)
	}
	if id, err := s.NextAutomaticKnowledgeCandidateForQuestion(ctx, profile, q.ID); err != nil || id != "scoped" {
		t.Fatal(id, err)
	}
	q = questionChange(t, s, q, LearningQuestionChange{Action: "edit", Body: "缩小后的问题"})
	if id, err := s.NextAutomaticKnowledgeCandidateForQuestion(ctx, profile, q.ID); err != nil || id != "" {
		t.Fatal("old candidate silently admitted after rename", id, err)
	}
	q = questionChange(t, s, q, LearningQuestionChange{Action: "status", Status: "paused"})
	if _, _, err := s.StartKnowledgeCandidate(ctx, "scoped", true); !errors.Is(err, ErrConflict) {
		t.Fatal("paused candidate admitted", err)
	}
	// Explicit manual selection retains the discovery's frozen question revision.
	a, created, err := s.StartKnowledgeCandidate(ctx, "scoped", false)
	if err != nil || !created {
		t.Fatal(created, err)
	}
	var frozen provider.KnowledgeArticleRequest
	if json.Unmarshal([]byte(a.InputJSON), &frozen) != nil || frozen.Question.Revision != req.Question.Revision {
		t.Fatal("candidate thawed to current question")
	}
}
