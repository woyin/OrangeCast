package store

import (
	"errors"
	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEvidenceGapOwnerCASHistoryAndExpiry(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	q, err := s.CreateLearningQuestion(ctx, LearningQuestion{Body: "主动回忆", Goal: "找到真实实践"})
	if err != nil {
		t.Fatal(err)
	}
	g, err := s.CreateEvidenceGap(ctx, "question", q.ID, q.Revision, "practice", "owner", "需要实践例子", "")
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.CreateEvidenceGap(ctx, "question", q.ID, q.Revision, "practice", "owner", "需要实践例子", "")
	if err != nil || again.ID != g.ID {
		t.Fatal(again, err)
	}
	var before, after int
	s.DB.QueryRow(`SELECT total_changes()`).Scan(&before)
	list, err := s.ListEvidenceGaps(ctx, "question", q.ID)
	s.DB.QueryRow(`SELECT total_changes()`).Scan(&after)
	if err != nil || len(list) != 1 || before != after {
		t.Fatal(list, before, after, err)
	}
	g, err = s.ChangeEvidenceGap(ctx, "question", q.ID, g.ID, q.Revision, g.Revision, "helpful", "这个例子有帮助")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ChangeEvidenceGap(ctx, "question", q.ID, g.ID, q.Revision, g.Revision-1, "ignored", ""); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	g, err = s.ChangeEvidenceGap(ctx, "question", q.ID, g.ID, q.Revision, g.Revision, "insufficient", "缺少反例")
	if err != nil {
		t.Fatal(err)
	}
	ops, err := s.ListEvidenceGapOperations(ctx, g.ID)
	if err != nil || len(ops) != 2 || ops[0].State != "helpful" || ops[1].State != "insufficient" {
		t.Fatal(ops, err)
	}
	q, err = s.ChangeLearningQuestion(ctx, q.ID, q.Revision, LearningQuestionChange{Action: "edit", Body: "主动回忆新版", Goal: q.Goal})
	if err != nil {
		t.Fatal(err)
	}
	list, err = s.ListEvidenceGaps(ctx, "question", q.ID)
	if err != nil || list[0].State != "expired" {
		t.Fatal(list, err)
	}
	if _, err = s.ChangeEvidenceGap(ctx, "question", q.ID, g.ID, q.Revision, g.Revision, "ignored", ""); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	if _, err = s.ChangeLearningQuestion(ctx, q.ID, q.Revision, LearningQuestionChange{Action: "delete"}); err != nil {
		t.Fatal(err)
	}
	var n int
	s.DB.QueryRow(`SELECT count(*) FROM evidence_gaps`).Scan(&n)
	if n != 0 {
		t.Fatal(n)
	}
}
func TestEvidenceGapLocalCandidatesNoPermissionOrPaidWork(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	doc, err := s.CreatePastedDocument(ctx, "主动回忆", "主动回忆实践有助于学习。")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetSourceProductionPolicy(ctx, models.SourceDocument, doc.ID, "internal", models.ModelDataLocalOnly); err != nil {
		t.Fatal(err)
	}
	note, err := s.CreateOwnerNote(ctx, models.OwnerNote{SourceType: "document", SourceID: doc.ID, Kind: "owner_reflection", Content: "主动回忆实践记录"})
	if err != nil {
		t.Fatal(err)
	}
	q, err := s.CreateLearningQuestion(ctx, LearningQuestion{Body: "主动回忆"})
	if err != nil {
		t.Fatal(err)
	}
	g, err := s.CreateEvidenceGap(ctx, "question", q.ID, 1, "practice", "owner", "主动回忆", "")
	if err != nil {
		t.Fatal(err)
	}
	var before, after, jobs int
	s.DB.QueryRow(`SELECT total_changes()`).Scan(&before)
	result, err := s.FindEvidenceGapCandidates(ctx, "question", q.ID, g.ID, "主动回忆", true, "")
	s.DB.QueryRow(`SELECT total_changes()`).Scan(&after)
	if err != nil || len(result.Candidates) < 2 || before != after || result.Coverage == "" || result.Degradation == "" {
		t.Fatal(result, before, after, err)
	}
	s.DB.QueryRow(`SELECT count(*) FROM processing_jobs`).Scan(&jobs)
	if jobs != 0 {
		t.Fatal(jobs)
	}
	links, err := s.ListLearningQuestionRelations(ctx, q.ID)
	if err != nil || len(links) != 0 {
		t.Fatal(links, err)
	}
	key := ""
	for _, c := range result.Candidates {
		if c.ObjectID == note.ID {
			key = c.Key
		}
	}
	if key == "" {
		t.Fatal(result)
	}
	q, err = s.ConfirmEvidenceGapCandidate(ctx, q.ID, g.ID, "主动回忆", key, q.Revision)
	if err != nil {
		t.Fatal(err)
	}
	links, err = s.ListLearningQuestionRelations(ctx, q.ID)
	if err != nil || len(links) != 1 || links[0].Kind != "note" || links[0].State != "confirmed" {
		t.Fatal(links, err)
	}
	repeat, e := s.ConfirmEvidenceGapCandidate(ctx, q.ID, g.ID, "主动回忆", key, q.Revision)
	if e != nil || repeat.Revision != q.Revision {
		t.Fatal("repeat confirmation changed scope", repeat, e)
	}
	var policy string
	s.DB.QueryRow(`SELECT model_data_policy FROM documents WHERE id=?`, doc.ID).Scan(&policy)
	if policy != "local_only" {
		t.Fatal(policy)
	}
	if _, err = s.ConfirmEvidenceGapCandidate(ctx, q.ID, g.ID, "主动回忆", key, 1); err == nil {
		t.Fatal("old gap/scope accepted")
	}
	if _, err = s.DB.Exec(`DELETE FROM documents WHERE id=?`, doc.ID); err != nil {
		t.Fatal(err)
	}
	var n int
	s.DB.QueryRow(`SELECT count(*) FROM evidence_gaps WHERE id=?`, g.ID).Scan(&n)
	if n != 0 {
		t.Fatal("purge retained gap", n)
	}
}

func TestEvidenceGapModelMissingReadOnlyDedupAndScopeExpiry(t *testing.T) {
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
	raw := `{"missing":["缺少反例","缺少反例","缺少限定条件"]}`
	if _, err = s.DB.Exec(`INSERT INTO knowledge_topic_candidates(id,batch_id,direction_hash,topic_json)VALUES('gap-candidate',?,'gap-hash',?)`, batch.ID, raw); err != nil {
		t.Fatal(err)
	}
	var before, after int
	s.DB.QueryRow(`SELECT total_changes()`).Scan(&before)
	gaps, err := s.ListEvidenceGaps(ctx, "candidate", "gap-candidate")
	s.DB.QueryRow(`SELECT total_changes()`).Scan(&after)
	if err != nil || len(gaps) != 2 || before != after || gaps[0].Origin != "model" || gaps[0].Kind != "counterexample" || gaps[0].Revision != 0 {
		t.Fatal(gaps, before, after, err)
	}
	g, err := s.ChangeEvidenceGap(ctx, "candidate", "gap-candidate", gaps[0].ID, 1, 0, "ignored", "暂不处理")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ChangeEvidenceGap(ctx, "candidate", "gap-candidate", g.ID, 1, 0, "helpful", ""); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec(`UPDATE knowledge_topic_candidates SET selection_json='["new-material"]' WHERE id='gap-candidate'`); err != nil {
		t.Fatal(err)
	}
	gaps, err = s.ListEvidenceGaps(ctx, "candidate", "gap-candidate")
	if err != nil || len(gaps) != 3 {
		t.Fatal(gaps, err)
	}
	expired := false
	for _, v := range gaps {
		if v.ID == g.ID && v.State == "expired" {
			expired = true
		}
	}
	if !expired {
		t.Fatal(gaps)
	}
	if _, err = s.ChangeEvidenceGap(ctx, "candidate", "gap-candidate", g.ID, 1, g.Revision, "helpful", ""); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec(`DELETE FROM knowledge_topic_candidates WHERE id='gap-candidate'`); err != nil {
		t.Fatal(err)
	}
	var n int
	s.DB.QueryRow(`SELECT count(*) FROM evidence_gap_operations`).Scan(&n)
	if n != 0 {
		t.Fatal(n)
	}
}

func TestEvidenceGapHomeNextActionOnlyOpenOwnerJudgments(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	q, err := s.CreateLearningQuestion(ctx, LearningQuestion{Body: "当前问题"})
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.GetLearningPreferences(ctx)
	if err != nil {
		t.Fatal(err)
	}
	p.CurrentQuestionID = q.ID
	if err = s.SaveLearningPreferences(ctx, p, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	g, err := s.CreateEvidenceGap(ctx, "question", q.ID, q.Revision, "comparison", "owner", "对比两种解释", "")
	if err != nil {
		t.Fatal(err)
	}
	var before, after int
	s.DB.QueryRow(`SELECT total_changes()`).Scan(&before)
	a, err := s.LearningNextActions(ctx, time.Now())
	s.DB.QueryRow(`SELECT total_changes()`).Scan(&after)
	if err != nil || len(a) != 1 || a[0].Kind != "gap" || !strings.HasSuffix(a[0].Href, "/gaps") || !strings.Contains(a[0].Reason, "我的记录") || before != after {
		t.Fatal(a, before, after, err)
	}
	if _, err = s.ChangeEvidenceGap(ctx, "question", q.ID, g.ID, q.Revision, g.Revision, "ignored", ""); err != nil {
		t.Fatal(err)
	}
	a, err = s.LearningNextActions(ctx, time.Now())
	if err != nil || len(a) != 1 || a[0].Kind != "question" {
		t.Fatal(a, err)
	}
}
func TestEvidenceGapRealAudioCandidatesAndUntreatedTitle(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	ep, snap := seedLearningExcerpt(t, s)
	if err := s.RebuildKnowledgeSearch(ctx); err != nil {
		t.Fatal(err)
	}
	var initialJobs int
	s.DB.QueryRow(`SELECT count(*) FROM processing_jobs`).Scan(&initialJobs)
	q, err := s.CreateLearningQuestion(ctx, LearningQuestion{Body: "甲"})
	if err != nil {
		t.Fatal(err)
	}
	g, err := s.CreateEvidenceGap(ctx, "question", q.ID, q.Revision, "example", "owner", "甲", "")
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.FindEvidenceGapCandidates(ctx, "question", q.ID, g.ID, "甲", false, "")
	if err != nil {
		t.Fatal(err)
	}
	playable := false
	for _, c := range result.Candidates {
		if c.SourceID == ep && c.SegmentID == "a" && c.SnapshotID == snap && c.Playable && c.Start == 20 && c.End == 30 {
			playable = true
		}
	}
	if !playable {
		t.Fatal("actual segment window not resolved", result)
	}
	if err = s.UpsertEvidenceAudio(ctx, models.SourceEpisode, ep, "replacement.mp3", "mp3", 100, "new-audio-sha"); err != nil {
		t.Fatal(err)
	}
	result, err = s.FindEvidenceGapCandidates(ctx, "question", q.ID, g.ID, "甲", false, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range result.Candidates {
		if c.SourceID == ep && c.Playable {
			t.Fatal("replaced original playable", c)
		}
	}
	podcast, err := s.CreatePodcast(ctx, "https://untreated.example/feed", "未处理资料", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.MergeEpisodes(ctx, podcast.ID, []models.Episode{{GUID: "untreated", Title: "甲未处理节目", AudioURL: "https://example/a.mp3"}}); err != nil {
		t.Fatal(err)
	}
	result, err = s.FindEvidenceGapCandidates(ctx, "question", q.ID, g.ID, "甲", false, "")
	if err != nil {
		t.Fatal(err)
	}
	pending := false
	for _, c := range result.Candidates {
		if c.Title == "甲未处理节目" && c.RequiresProcessing && !c.Playable && c.SnapshotID == "" {
			pending = true
		}
	}
	if !pending {
		t.Fatal(result)
	}
	var jobs int
	s.DB.QueryRow(`SELECT count(*) FROM processing_jobs`).Scan(&jobs)
	if jobs != initialJobs {
		t.Fatal(jobs)
	}
}

func TestEvidenceGapQuestionProjectsOnlyCurrentScopedModelMissing(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	q, err := s.CreateLearningQuestion(ctx, LearningQuestion{Body: "比较条件"})
	if err != nil {
		t.Fatal(err)
	}
	profile, err := s.EnsureDefaultEditorialProfile(ctx)
	if err != nil {
		t.Fatal(err)
	}
	request := provider.KnowledgeArticleRequest{PromptVersion: provider.KnowledgeArticlePromptVersion, Question: &provider.FrozenLearningQuestion{ID: q.ID, Revision: q.Revision, Body: q.Body}}
	batch, err := s.EnsureKnowledgeDiscoveryBatch(ctx, profile.ID, "pod", "model", request, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec(`INSERT INTO knowledge_topic_candidates(id,batch_id,direction_hash,topic_json)VALUES('question-gap-candidate',?,'gap-hash','{"missing":["缺少限定条件"]}')`, batch.ID); err != nil {
		t.Fatal(err)
	}
	gaps, err := s.ListEvidenceGaps(ctx, "question", q.ID)
	if err != nil || len(gaps) != 1 || gaps[0].Origin != "model" || gaps[0].Kind != "condition" {
		t.Fatal(gaps, err)
	}
	g, err := s.ChangeEvidenceGap(ctx, "question", q.ID, gaps[0].ID, q.Revision, 0, "ignored", "我的判断")
	if err != nil {
		t.Fatal(err)
	}
	q, err = s.ChangeLearningQuestion(ctx, q.ID, q.Revision, LearningQuestionChange{Action: "edit", Body: "新的比较范围"})
	if err != nil {
		t.Fatal(err)
	}
	gaps, err = s.ListEvidenceGaps(ctx, "question", q.ID)
	if err != nil || len(gaps) != 1 || gaps[0].ID != g.ID || gaps[0].State != "expired" {
		t.Fatal("old model suggestion revived in new scope", gaps, err)
	}
}

func TestEvidenceGapUpdateParentChangeAndUnknownProgramCause(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	profile, err := s.EnsureDefaultEditorialProfile(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.DB.Exec(`INSERT INTO knowledge_articles(id,profile_id,input_hash,input_json,status,provider,model,working_revision,topic_json)VALUES('gap-article',?,'gap-article-hash','{}','insufficient','pod','m',1,'{"missing":["缺少反例"]}');INSERT INTO knowledge_update_proposals(id,article_id,parent_revision,parent_hash,passed_revision,input_hash,material_fingerprint,input_json,provider,model,prompt_version,analysis_json)VALUES('gap-update','gap-article',1,'parent-hash',1,'update-hash','materials','{}','pod','m','v','{"missing":["缺少实践依据"]}')`, profile.ID)
	if err != nil {
		t.Fatal(err)
	}
	article, err := s.ListEvidenceGaps(ctx, "article", "gap-article")
	if err != nil || len(article) != 1 || article[0].Origin != "model" {
		t.Fatal(article, err)
	}
	update, err := s.ListEvidenceGaps(ctx, "update", "gap-update")
	if err != nil || len(update) != 1 || update[0].Kind != "practice" {
		t.Fatal(update, err)
	}
	g, err := s.ChangeEvidenceGap(ctx, "update", "gap-update", update[0].ID, 1, 0, "insufficient", "仍不足")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec(`UPDATE knowledge_articles SET working_revision=2 WHERE id='gap-article'`); err != nil {
		t.Fatal(err)
	}
	update, err = s.ListEvidenceGaps(ctx, "update", "gap-update")
	if err != nil || len(update) != 1 || update[0].ID != g.ID || update[0].State != "expired" {
		t.Fatal("old update revived", update, err)
	}
	q, err := s.CreateLearningQuestion(ctx, LearningQuestion{Body: "不足原因是否已知"})
	if err != nil {
		t.Fatal(err)
	}
	session, err := s.StartQuestionStudySession(ctx, q.ID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.DB.Exec(`INSERT INTO question_study_turns(id,session_id,ordinal,request_key,payload_hash,state,owner_input)VALUES('insufficient-turn',?,1,?,'payload-hash','insufficient','Owner提问')`, session.ID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	gaps, err := s.ListEvidenceGaps(ctx, "question", q.ID)
	if err != nil || len(gaps) != 1 || gaps[0].Origin != "program" || !strings.Contains(gaps[0].Explanation, "原因未知") {
		t.Fatal(gaps, err)
	}
	for _, in := range []struct{ kind, origin, text string }{{"bad", "owner", "x"}, {"example", "model", "x"}, {"other", "owner", ""}} {
		if _, err = s.CreateEvidenceGap(ctx, "question", q.ID, q.Revision, in.kind, in.origin, in.text, ""); !errors.Is(err, ErrInvalidEditorialState) {
			t.Fatal(in, err)
		}
	}
	if _, err = s.ListEvidenceGaps(ctx, "bad", q.ID); !errors.Is(err, ErrInvalidEditorialState) {
		t.Fatal(err)
	}
	if _, err = s.ListEvidenceGaps(ctx, "question", "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if _, err = s.FindEvidenceGapCandidates(ctx, "question", q.ID, "missing-gap", "query", false, ""); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}

func TestEvidenceGapUnderstandingLinkAndExactRetrieval(t *testing.T) {
	s, _, _, _ := knowledgeStoreFixture(t)
	ctx := t.Context()
	var baseline int
	if err := s.DB.QueryRowContext(ctx, `SELECT count(*) FROM processing_jobs`).Scan(&baseline); err != nil {
		t.Fatal(err)
	}
	owner := createQuestion(t, s)
	u := saveUnderstandingFixture(t, s, owner, "主动回忆实践理解", 0, nil)
	if err := s.ChooseCurrentUnderstanding(ctx, owner.ID, u.ID, owner.Revision, 1, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	q := createQuestion(t, s)
	g, err := s.CreateEvidenceGap(ctx, "question", q.ID, q.Revision, "practice", "owner", "主动回忆实践理解", "")
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.FindEvidenceGapCandidates(ctx, "question", q.ID, g.ID, "主动回忆实践理解", true, uuid.NewString())
	if err != nil || !strings.HasPrefix(result.Method, "fts") || result.Degradation == "" {
		t.Fatal(result, err)
	}
	var key string
	for _, c := range result.Candidates {
		if c.Kind == "understanding" {
			key = c.Key
			if c.ObjectID != u.ID || c.SourceID != "" || c.SourceType != "" || c.Playable {
				t.Fatal(c)
			}
		}
	}
	if key == "" {
		t.Fatal("understanding candidate missing")
	}
	if _, err = s.ConfirmEvidenceGapCandidateWithRetrieval(ctx, q.ID, g.ID, "主动回忆实践理解", key, q.Revision, true, "", "hybrid"); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	if _, err = s.ConfirmEvidenceGapCandidate(ctx, q.ID, g.ID, "主动回忆实践理解", "understanding:"+uuid.NewString(), q.Revision); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	linked, err := s.ConfirmEvidenceGapCandidateWithRetrieval(ctx, q.ID, g.ID, "主动回忆实践理解", key, q.Revision, true, "", result.Method)
	if err != nil {
		t.Fatal(err)
	}
	links, err := s.ListLearningQuestionRelations(ctx, q.ID)
	if err != nil || len(links) != 1 || links[0].Kind != "understanding" || links[0].ObjectID != u.ID || links[0].SourceID != "" || links[0].Version != u.Version {
		t.Fatal(links, err)
	}
	stored, err := s.GetUnderstandingSnapshot(ctx, u.ID)
	if err != nil || stored.ModelDataPolicy != "local_only" {
		t.Fatal(stored, err)
	}
	if _, err = s.ConfirmEvidenceGapCandidate(ctx, q.ID, g.ID, "主动回忆实践理解", key, q.Revision); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	var jobs int
	if err = s.DB.QueryRowContext(ctx, `SELECT count(*) FROM processing_jobs`).Scan(&jobs); err != nil || jobs != baseline {
		t.Fatal(jobs, err)
	}

	external, e := s.Retrieve(ctx, KnowledgeRetrieveQuery{Purpose: RetrieveExternal, Search: KnowledgeSearchQuery{Text: "主动回忆实践理解", Kind: "understanding", SendProvider: "groq"}})
	if e != nil || len(external.Hits) != 0 {
		t.Fatal("link must not grant external permission", external, e)
	}
	frozen, e := s.FreezeLearningQuestion(ctx, q.ID, false)
	if e != nil || len(frozen.Links) != 1 || frozen.Links[0].Kind != "understanding" || frozen.Links[0].Version != u.Version || frozen.Links[0].SourceID != "" {
		t.Fatal(frozen, e)
	}
	if linked.Revision != q.Revision+1 {
		t.Fatal(linked)
	}
}

func TestEvidenceGapUnderstandingLinksUpgrade87RollbackAndPreserve(t *testing.T) {
	ctx := t.Context()
	db := openRaw(t, filepath.Join(t.TempDir(), "v87.db"))
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(ctx, schemaMigrationsTable); err != nil {
		t.Fatal(err)
	}
	ms, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	var latest migration
	for _, m := range ms {
		if m.version <= 87 {
			if err = applyOne(ctx, db, m); err != nil {
				t.Fatal(err)
			}
		} else if m.version == 88 {
			latest = m
		}
	}
	s := &Store{DB: db}
	ep := seedEpisodeForArtifact(t, s)
	q := createQuestion(t, s)
	if _, err = db.ExecContext(ctx, `INSERT INTO learning_question_links(question_id,kind,object_id,source_type,source_id,version,state,origin,created_at) VALUES(?,'source',?,'episode',?,7,'suggested','model','2026-01-01')`, q.ID, "episode:"+ep, ep); err != nil {
		t.Fatal(err)
	}
	broken := latest
	broken.up += "\nINSERT INTO deliberately_missing_table VALUES(1);"
	if err = applyOne(ctx, db, broken); err == nil {
		t.Fatal("expected migration failure")
	}
	var schema string
	if err = db.QueryRowContext(ctx, `SELECT sql FROM sqlite_master WHERE name='learning_question_links'`).Scan(&schema); err != nil || strings.Contains(schema, "understanding") {
		t.Fatal(schema, err)
	}
	if err = applyOne(ctx, db, latest); err != nil {
		t.Fatal(err)
	}
	var version int
	var state, origin, created string
	if err = db.QueryRowContext(ctx, `SELECT version,state,origin,created_at FROM learning_question_links WHERE question_id=?`, q.ID).Scan(&version, &state, &origin, &created); err != nil || version != 7 || state != "suggested" || origin != "model" || created != "2026-01-01" {
		t.Fatal(version, state, origin, created, err)
	}
	u := saveUnderstandingFixture(t, s, q, "升级保留后理解", 0, nil)
	if _, err = s.ChangeLearningQuestion(ctx, q.ID, q.Revision, LearningQuestionChange{Action: "link", Link: provider.LearningQuestionLink{Kind: "understanding", ObjectID: u.ID}}); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `DELETE FROM understanding_snapshots WHERE id=?`, u.ID); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM learning_question_links WHERE kind='understanding'`).Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
	if _, err = db.ExecContext(ctx, `DELETE FROM episodes WHERE id=?`, ep); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM learning_question_links`).Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
	var violations int
	rows, err := db.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		violations++
	}
	rows.Close()
	if violations != 0 {
		t.Fatal(violations)
	}
}

func TestEvidenceGapCachedSemanticExactMethodNoNewJob(t *testing.T) {
	s, cfg, doc := embeddingIndexFixture(t)
	ctx := t.Context()
	enableEmbeddingDoc(t, s, cfg, doc)
	windows, err := s.PrepareKnowledgeEmbeddingBatch(ctx, cfg.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.AdoptKnowledgeEmbeddings(ctx, cfg.ID, windows, fakeEmbeddingResult(cfg, windows)); err != nil {
		t.Fatal(err)
	}
	cacheSyntheticQuery(t, s, cfg, "反例")
	q := createQuestion(t, s)
	gap, err := s.CreateEvidenceGap(ctx, "question", q.ID, q.Revision, "counterexample", "owner", "反例", "")
	if err != nil {
		t.Fatal(err)
	}
	// This declared synthetic test report tests the admission mechanism, never real quality.
	if _, err = s.SaveKnowledgeEmbeddingQualityReport(ctx, cfg.ID, qualityGateFixture(t, s, cfg.ID)); err != nil {
		t.Fatal(err)
	}
	current, err := s.GetKnowledgeEmbeddingConfig(ctx, cfg.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetKnowledgeSemanticEnabled(ctx, cfg.ID, current.Revision, true); err != nil {
		t.Fatal(err)
	}
	var baseline int
	s.DB.QueryRowContext(ctx, `SELECT count(*) FROM processing_jobs`).Scan(&baseline)
	result, err := s.FindEvidenceGapCandidates(ctx, "question", q.ID, gap.ID, "反例", true, cfg.ID)
	if err != nil || result.Method != "rrf" || len(result.Candidates) == 0 {
		t.Fatal(result, err)
	}
	c := result.Candidates[0]
	if _, err = s.ConfirmEvidenceGapCandidateWithRetrieval(ctx, q.ID, gap.ID, "反例", c.Key, q.Revision, true, cfg.ID, "fts"); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	if _, err = s.ConfirmEvidenceGapCandidateWithRetrieval(ctx, q.ID, gap.ID, "反例", c.Key, q.Revision, true, cfg.ID, result.Method); err != nil {
		t.Fatal(err)
	}
	var after int
	s.DB.QueryRowContext(ctx, `SELECT count(*) FROM processing_jobs`).Scan(&after)
	if after != baseline {
		t.Fatal("read or confirm created work", baseline, after)
	}
}
