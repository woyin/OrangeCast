package store

import (
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"path/filepath"
	"sync"
	"testing"
)

func saveUnderstandingFixture(t *testing.T, s *Store, q *LearningQuestion, answer string, rev int, refs []UnderstandingReference) *UnderstandingSnapshot {
	t.Helper()
	v, e := s.SaveUnderstanding(t.Context(), SaveUnderstandingCommand{QuestionID: q.ID, QuestionRevision: q.Revision, HeadRevision: rev, RequestKey: uuid.NewString(), Answer: answer, References: refs})
	if e != nil {
		t.Fatal(e)
	}
	return v
}
func TestUnderstandingSnapshotModelNoSourceImmutableAndDelete(t *testing.T) {
	s, _, _, _ := knowledgeStoreFixture(t)
	q := createQuestion(t, s)
	v := saveUnderstandingFixture(t, s, q, "主动回忆使我理解条件", 0, nil)
	if v.ModelDataPolicy != "local_only" || len(v.References) != 0 {
		t.Fatal(v)
	}
	if _, e := s.GetCurrentUnderstanding(t.Context(), q.ID); !errors.Is(e, ErrNotFound) {
		t.Fatal(e)
	}
	key := uuid.NewString()
	if e := s.ChooseCurrentUnderstanding(t.Context(), q.ID, v.ID, q.Revision, 1, key); e != nil {
		t.Fatal(e)
	}
	if e := s.ChooseCurrentUnderstanding(t.Context(), q.ID, v.ID, q.Revision, 1, key); e != nil {
		t.Fatal("choose replay", e)
	}
	v2 := saveUnderstandingFixture(t, s, q, "新的主动回忆理解", 2, nil)
	old, e := s.GetUnderstandingSnapshot(t.Context(), v.ID)
	if e != nil || old.Answer != v.Answer {
		t.Fatal(old, e)
	}
	current, e := s.GetCurrentUnderstanding(t.Context(), q.ID)
	if e != nil || current.ID != v.ID {
		t.Fatal(current, e)
	}
	search, e := s.SearchKnowledge(t.Context(), KnowledgeSearchQuery{Kind: "understanding", Text: "主动回忆"})
	if e != nil || search.Total != 1 || search.Hits[0].ObjectID != v.ID {
		t.Fatal(search, e)
	}
	history, e := s.SearchKnowledge(t.Context(), KnowledgeSearchQuery{Kind: "understanding", Text: "主动回忆", IncludeHistory: true})
	if e != nil || history.Total != 2 {
		t.Fatal(history, e, v2)
	}
	q = questionChange(t, s, q, LearningQuestionChange{Action: "edit", Body: "专属问题标题检索", Goal: q.Goal})
	titleSearch, e := s.SearchKnowledge(t.Context(), KnowledgeSearchQuery{Kind: "understanding", Text: "专属问题标题"})
	if e != nil || titleSearch.Total != 1 || titleSearch.Hits[0].ObjectID != v.ID {
		t.Fatal(titleSearch, e)
	}
	if e = s.RebuildKnowledgeSearch(t.Context()); e != nil {
		t.Fatal(e)
	}
	search, e = s.SearchKnowledge(t.Context(), KnowledgeSearchQuery{Kind: "understanding"})
	if e != nil || search.Total != 1 {
		t.Fatal(search, e)
	}
	if _, e = s.ChangeLearningQuestion(t.Context(), q.ID, q.Revision, LearningQuestionChange{Action: "delete"}); e != nil {
		t.Fatal(e)
	}
	if _, e = s.GetUnderstandingSnapshot(t.Context(), v.ID); !errors.Is(e, ErrNotFound) {
		t.Fatal(e)
	}
}
func TestUnderstandingSnapshotSaveCASReplayRollback(t *testing.T) {
	s, _, _, _ := knowledgeStoreFixture(t)
	q := createQuestion(t, s)
	c := SaveUnderstandingCommand{QuestionID: q.ID, QuestionRevision: q.Revision, RequestKey: uuid.NewString(), Answer: "我自己的答案"}
	v, e := s.SaveUnderstanding(t.Context(), c)
	if e != nil {
		t.Fatal(e)
	}
	replay, e := s.SaveUnderstanding(t.Context(), c)
	if e != nil || replay.ID != v.ID {
		t.Fatal(replay, e)
	}
	c.Answer = "不同输入"
	if _, e = s.SaveUnderstanding(t.Context(), c); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	c.RequestKey = uuid.NewString()
	if _, e = s.SaveUnderstanding(t.Context(), c); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	c.HeadRevision = 1
	c.References = []UnderstandingReference{{Kind: "note", ObjectID: "missing", Version: 1}}
	if _, e = s.SaveUnderstanding(t.Context(), c); !errors.Is(e, ErrNotFound) {
		t.Fatal(e)
	}
	h, e := s.UnderstandingHead(t.Context(), q.ID)
	if e != nil || h.Revision != 1 {
		t.Fatal(h, e)
	}
	q2 := questionChange(t, s, q, LearningQuestionChange{Action: "status", Status: "paused"})
	c.References = nil
	if _, e = s.SaveUnderstanding(t.Context(), c); !errors.Is(e, ErrConflict) {
		t.Fatal(e, q2)
	}
}
func TestUnderstandingSnapshotPermissionsPurgeKeepsOwner(t *testing.T) {
	s, _, ep, note := knowledgeStoreFixture(t)
	q := createQuestion(t, s)
	c := SaveUnderstandingCommand{QuestionID: q.ID, QuestionRevision: q.Revision, RequestKey: uuid.NewString(), Answer: "跨来源的我自己的判断", ModelDataPolicy: "external_allowed", References: []UnderstandingReference{{Kind: "note", ObjectID: note, Version: 1}}}
	v, e := s.SaveUnderstanding(t.Context(), c)
	if e != nil {
		t.Fatal(e)
	}
	ok, e := s.UnderstandingMaySend(t.Context(), v.ID, v.Version, "pod")
	if e != nil || !ok {
		t.Fatal(ok, e)
	}
	if e = s.SetSourceProductionPolicy(t.Context(), models.SourceEpisode, ep, "internal", models.ModelDataLocalOnly); e != nil {
		t.Fatal(e)
	}
	ok, e = s.UnderstandingMaySend(t.Context(), v.ID, v.Version, "pod")
	if e != nil || ok {
		t.Fatal(ok, e)
	}
	if _, e = s.DB.Exec(`DELETE FROM episodes WHERE id=?`, ep); e != nil {
		t.Fatal(e)
	}
	f, e := s.GetUnderstandingSnapshot(t.Context(), v.ID)
	if e != nil || f.Answer != v.Answer || len(f.References) != 1 || !f.References[0].Purged || f.References[0].Body != "" {
		t.Fatal(f, e)
	}
}

func TestUnderstandingSnapshotNoSourceQuestionStudyAdmissionAndRevocation(t *testing.T) {
	s, _, _, _ := knowledgeStoreFixture(t)
	q := createQuestion(t, s)
	v, e := s.SaveUnderstanding(t.Context(), SaveUnderstandingCommand{QuestionID: q.ID, QuestionRevision: q.Revision, RequestKey: uuid.NewString(), Answer: "尚无来源，我认为主动回忆需要检验", ModelDataPolicy: "external_allowed"})
	if e != nil {
		t.Fatal(e)
	}
	if e = s.ChooseCurrentUnderstanding(t.Context(), q.ID, v.ID, q.Revision, 1, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	session, e := s.StartQuestionStudySession(t.Context(), q.ID, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	cfg, e := provider.NewSelector("", "").WithPod("secret", "https://pod.example/v1", "generate").QuestionStudy("generate", "review")
	if e != nil {
		t.Fatal(e)
	}
	scope, e := s.FreezeQuestionStudyScope(t.Context(), session.ID, "我的判断哪里需要依据", "pod", nil)
	if e != nil || len(scope.Materials) != 1 || scope.Materials[0].Kind != "understanding" || scope.Materials[0].SourceID != "" {
		t.Fatal(scope, e)
	}
	turn, job, _, e := s.SubmitQuestionStudyTurn(t.Context(), session.ID, 1, "我的判断哪里需要依据", uuid.NewString(), nil, cfg.Config())
	if e != nil {
		t.Fatal(e)
	}
	if turn == nil || job == nil {
		t.Fatal(turn, job)
	}
	if _, e = s.DB.Exec(`UPDATE understanding_snapshots SET model_data_policy='local_only' WHERE id=?`, v.ID); e != nil {
		t.Fatal(e)
	}
	if e = checkQuestionStudyDependencies(t.Context(), s.DB, turn.ID, "pod"); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
}

func TestUnderstandingSnapshotBackupRestorePreservesHistoryCurrentAndReferences(t *testing.T) {
	s, _, _, note := knowledgeStoreFixture(t)
	q := createQuestion(t, s)
	v := saveUnderstandingFixture(t, s, q, "备份我的理解", 0, []UnderstandingReference{{Kind: "note", ObjectID: note, Version: 1}})
	if e := s.ChooseCurrentUnderstanding(t.Context(), q.ID, v.ID, q.Revision, 1, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(t.TempDir(), "understanding-backup.db")
	if e := ConsistencyBackup(t.Context(), s.DB, path); e != nil {
		t.Fatal(e)
	}
	restored, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer restored.Close()
	got, e := restored.GetCurrentUnderstanding(t.Context(), q.ID)
	if e != nil || got.ID != v.ID || got.Answer != v.Answer || len(got.References) != 1 || got.References[0].ObjectID != note {
		t.Fatal(got, e)
	}
	head, e := restored.UnderstandingHead(t.Context(), q.ID)
	if e != nil || head.Revision != 2 {
		t.Fatal(head, e)
	}
}

func TestUnderstandingSnapshotExplicitEmbeddingScopeAndCurrentInvalidation(t *testing.T) {
	s, cfg, _ := embeddingIndexFixture(t)
	q := createQuestion(t, s)
	v, e := s.SaveUnderstanding(t.Context(), SaveUnderstandingCommand{QuestionID: q.ID, QuestionRevision: q.Revision, RequestKey: uuid.NewString(), Answer: "没有来源的主动回忆假设", ModelDataPolicy: "external_allowed"})
	if e != nil {
		t.Fatal(e)
	}
	if e = s.ChooseCurrentUnderstanding(t.Context(), q.ID, v.ID, q.Revision, 1, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	c, e := s.GetKnowledgeEmbeddingConfig(t.Context(), cfg.ID)
	if e != nil {
		t.Fatal(e)
	}
	c, e = s.UpdateKnowledgeEmbeddingSettings(t.Context(), EmbeddingSettingsCommand{ConfigID: cfg.ID, ExpectedRevision: c.Revision, IndexAuthorized: true, WindowCapacity: 100, UnderstandingSnapshotIDs: []string{v.ID}})
	if e != nil {
		t.Fatal(e)
	}
	windows, e := s.PrepareKnowledgeEmbeddingBatch(t.Context(), cfg.ID)
	if e != nil || len(windows) != 1 || windows[0].DocKey != "understanding:"+v.ID || len(windows[0].Sources) != 0 {
		t.Fatal(windows, e)
	}
	if n, e := s.AdoptKnowledgeEmbeddings(t.Context(), cfg.ID, windows, fakeEmbeddingResult(cfg, windows)); e != nil || n != 1 {
		t.Fatal(n, e)
	}
	v2, e := s.SaveUnderstanding(t.Context(), SaveUnderstandingCommand{QuestionID: q.ID, QuestionRevision: q.Revision, HeadRevision: 2, ParentID: v.ID, RequestKey: uuid.NewString(), Answer: "新版本未自动授权索引", ModelDataPolicy: "external_allowed"})
	if e != nil {
		t.Fatal(e)
	}
	if e = s.ChooseCurrentUnderstanding(t.Context(), q.ID, v2.ID, q.Revision, 3, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	status, e := s.KnowledgeEmbeddingStatus(t.Context(), cfg.ID)
	if e != nil || status.IndexedWindows != 0 {
		t.Fatal(status, e)
	}
	windows, e = s.PrepareKnowledgeEmbeddingBatch(t.Context(), cfg.ID)
	if e != nil || len(windows) != 0 {
		t.Fatal(windows, e)
	}
}

func TestUnderstandingSnapshotPurgeRedactsDerivedReferencesAndStopsArticle(t *testing.T) {
	s, profile, ep, note := knowledgeStoreFixture(t)
	q := createQuestion(t, s)
	v, e := s.SaveUnderstanding(t.Context(), SaveUnderstandingCommand{QuestionID: q.ID, QuestionRevision: q.Revision, RequestKey: uuid.NewString(), Answer: "Owner保留的判断", ModelDataPolicy: "external_allowed", References: []UnderstandingReference{{Kind: "note", ObjectID: note, Version: 1}}})
	if e != nil {
		t.Fatal(e)
	}
	m, e := s.UnderstandingKnowledgeMaterial(t.Context(), v.ID, "pod")
	if e != nil || m == nil {
		t.Fatal(m, e)
	}
	req := provider.KnowledgeArticleRequest{PromptVersion: provider.KnowledgeArticlePromptVersion, Materials: []provider.KnowledgeMaterial{*m}}
	raw := jsonString(req)
	article := uuid.NewString()
	if _, e = s.DB.Exec(`INSERT INTO knowledge_articles(id,profile_id,input_hash,input_json,provider,model)VALUES(?,?,?,?,?,?)`, article, profile, uuid.NewString(), raw, "pod", "model"); e != nil {
		t.Fatal(e)
	}
	tx, e := s.DB.BeginTx(t.Context(), nil)
	if e != nil {
		t.Fatal(e)
	}
	rev, e := appendKnowledgeRevision(t.Context(), tx, article, 0, "Owner理解文章", req, []provider.KnowledgeBlock{{ID: "b", Kind: "reflection", Text: "Owner保留的判断", MaterialIDs: []string{v.ID}}}, "test", "pod", "model", req.PromptVersion)
	if e != nil {
		tx.Rollback()
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	outer, e := s.SaveUnderstanding(t.Context(), SaveUnderstandingCommand{QuestionID: q.ID, QuestionRevision: q.Revision, HeadRevision: 1, RequestKey: uuid.NewString(), Answer: "外层Owner答案保留", ModelDataPolicy: "external_allowed", References: []UnderstandingReference{{Kind: "article", ObjectID: article, Version: rev}}})
	if e != nil {
		t.Fatal(e)
	}
	if ok, e := s.UnderstandingMaySend(t.Context(), outer.ID, outer.Version, "pod"); e != nil || !ok {
		t.Fatal("nested article understanding not admitted", ok, e)
	}
	if e = s.SetSourceProductionPolicy(t.Context(), models.SourceEpisode, ep, "internal", models.ModelDataLocalOnly); e != nil {
		t.Fatal(e)
	}
	if ok, e := s.UnderstandingMaySend(t.Context(), outer.ID, outer.Version, "pod"); e != nil || ok {
		t.Fatal("nested source revoked", ok, e)
	}
	if _, e = s.DB.Exec(`DELETE FROM episodes WHERE id=?`, ep); e != nil {
		t.Fatal(e)
	}
	outerRead, e := s.GetUnderstandingSnapshot(t.Context(), outer.ID)
	if e != nil || outerRead.Answer != "外层Owner答案保留" || !outerRead.References[0].Purged || outerRead.References[0].Body != "" {
		t.Fatal(outerRead, e)
	}
	saved, e := s.GetKnowledgeRevision(t.Context(), article, rev)
	if e != nil {
		t.Fatal(e)
	}
	var input provider.KnowledgeArticleRequest
	if e = json.Unmarshal([]byte(saved.InputJSON), &input); e != nil {
		t.Fatal(e)
	}
	if saved.EvidenceStatus != "unavailable" || len(input.Materials) != 1 || input.Materials[0].Content != "Owner保留的判断" || len(input.Materials[0].UnderstandingReferences) != 1 || input.Materials[0].UnderstandingReferences[0].Body != "" || !input.Materials[0].UnderstandingReferences[0].Purged {
		t.Fatal(saved, input)
	}
}

func TestUnderstandingSnapshotSaveConcurrentIdempotenceAndFaultRollback(t *testing.T) {
	s, _, _, _ := knowledgeStoreFixture(t)
	q := createQuestion(t, s)
	cmd := SaveUnderstandingCommand{QuestionID: q.ID, QuestionRevision: q.Revision, RequestKey: uuid.NewString(), Answer: "同时请求只能保存一份"}
	var group sync.WaitGroup
	ids := make(chan string, 8)
	failures := make(chan error, 8)
	for i := 0; i < 8; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			v, e := s.SaveUnderstanding(t.Context(), cmd)
			if e != nil {
				failures <- e
			} else {
				ids <- v.ID
			}
		}()
	}
	group.Wait()
	close(ids)
	close(failures)
	for e := range failures {
		t.Fatal(e)
	}
	unique := ""
	for id := range ids {
		if unique != "" && unique != id {
			t.Fatal("duplicate", unique, id)
		}
		unique = id
	}
	if _, e := s.DB.Exec(`CREATE TRIGGER understanding_test_abort BEFORE INSERT ON understanding_snapshots BEGIN SELECT RAISE(ABORT,'injected write fault'); END;`); e != nil {
		t.Fatal(e)
	}
	cmd.RequestKey = uuid.NewString()
	cmd.HeadRevision = 1
	if _, e := s.SaveUnderstanding(t.Context(), cmd); e == nil {
		t.Fatal("fault should rollback")
	}
	h, e := s.UnderstandingHead(t.Context(), q.ID)
	if e != nil || h.Revision != 1 {
		t.Fatal(h, e)
	}
	history, e := s.HistoryUnderstanding(t.Context(), q.ID, 0, 20)
	if e != nil || len(history) != 1 {
		t.Fatal(history, e)
	}
}

func TestUnderstandingSnapshotMultiSourceExactReferenceAndAllProviderPermission(t *testing.T) {
	s, _, ep, note := knowledgeStoreFixture(t)
	q := createQuestion(t, s)
	doc, e := s.CreatePastedDocument(t.Context(), "另一集观点", "边界与反例")
	if e != nil {
		t.Fatal(e)
	}
	n, e := s.CreateOwnerNote(t.Context(), models.OwnerNote{SourceType: "document", SourceID: doc.ID, Kind: "owner_reflection", Content: "另一个来源的笔记"})
	if e != nil {
		t.Fatal(e)
	}
	if e = s.SetSourceProductionPolicy(t.Context(), models.SourceDocument, doc.ID, "internal", models.ModelDataLocalOnly); e != nil {
		t.Fatal(e)
	}
	v, e := s.SaveUnderstanding(t.Context(), SaveUnderstandingCommand{QuestionID: q.ID, QuestionRevision: q.Revision, RequestKey: uuid.NewString(), Answer: "跨集形成的判断", ModelDataPolicy: "approved_providers_only", ApprovedProviders: []string{"pod"}, References: []UnderstandingReference{{Kind: "note", ObjectID: note, Version: 1}, {Kind: "note", ObjectID: n.ID, Version: 1}}})
	if e != nil {
		t.Fatal(e)
	}
	if ok, e := s.UnderstandingMaySend(t.Context(), v.ID, v.Version, "pod"); e != nil || ok {
		t.Fatal(ok, e)
	}
	if e = s.SetSourceProductionPolicy(t.Context(), models.SourceDocument, doc.ID, "internal", models.ModelDataExternalAllowed); e != nil {
		t.Fatal(e)
	}
	if ok, e := s.UnderstandingMaySend(t.Context(), v.ID, v.Version, "pod"); e != nil || !ok {
		t.Fatal(ok, e)
	}
	if ok, e := s.UnderstandingMaySend(t.Context(), v.ID, v.Version, "different"); e != nil || ok {
		t.Fatal(ok, e)
	}
	scoped, e := s.SearchKnowledge(t.Context(), KnowledgeSearchQuery{Kind: "understanding", SourceID: doc.ID, IncludeHistory: true})
	if e != nil || scoped.Total != 1 || scoped.Hits[0].ObjectID != v.ID {
		t.Fatal("source scope must include real Reference", scoped, e)
	}
	if _, e = s.UpdateOwnerNote(t.Context(), n.ID, "后来修改", n.CitationsJSON, n.ReferencesJSON, n.Revision); e != nil {
		t.Fatal(e)
	}
	old, e := s.GetUnderstandingSnapshot(t.Context(), v.ID)
	if e != nil || old.References[1].Body != "另一个来源的笔记" || old.References[1].Version != 1 || !old.References[1].Changed {
		t.Fatal(old, e)
	}
	if _, e = s.DB.Exec(`DELETE FROM episodes WHERE id=?`, ep); e != nil {
		t.Fatal(e)
	}
	old, e = s.GetUnderstandingSnapshot(t.Context(), v.ID)
	if e != nil || old.Answer != "跨集形成的判断" || !old.References[0].Purged || old.References[1].Purged {
		t.Fatal(old, e)
	}
}

func TestUnderstandingSnapshotSourceReferenceNeedsRealVersionAndNeverCitation(t *testing.T) {
	s, _, _, _ := knowledgeStoreFixture(t)
	doc, e := s.CreatePastedDocument(t.Context(), "真实文档", "不要把我的理解当引文")
	if e != nil {
		t.Fatal(e)
	}
	ref, e := s.ReadCurrentSourceUnderstandingReference(t.Context(), "document", doc.ID)
	if e != nil || ref.Kind != "source" || ref.Version != doc.Version || ref.SourceID != doc.ID {
		t.Fatal(ref, e)
	}
	q := createQuestion(t, s)
	c := SaveUnderstandingCommand{QuestionID: q.ID, QuestionRevision: q.Revision, RequestKey: uuid.NewString(), Answer: "我的判断，不是原话", ModelDataPolicy: "external_allowed", References: []UnderstandingReference{ref}}
	v, e := s.SaveUnderstanding(t.Context(), c)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.SetSourceProductionPolicy(t.Context(), models.SourceDocument, doc.ID, "internal", models.ModelDataExternalAllowed); e != nil {
		t.Fatal(e)
	}
	m, e := s.UnderstandingKnowledgeMaterial(t.Context(), v.ID, "pod")
	if e != nil || m == nil || len(m.Citations) != 0 || m.SourceID != "" || len(m.UnderstandingReferences) != 1 {
		t.Fatal(m, e)
	}
	c.HeadRevision = 1
	c.RequestKey = uuid.NewString()
	c.References[0].Version++
	if _, e = s.SaveUnderstanding(t.Context(), c); !errors.Is(e, ErrNotFound) {
		t.Fatal("fabricated source version accepted", e)
	}
}

func TestUnderstandingSnapshotManualKeypointZeroVersionRemainsRealAndRejectsChangedBody(t *testing.T) {
	s, _, ep, _ := knowledgeStoreFixture(t)
	q := createQuestion(t, s)
	kp, e := s.CreateManualKeyPoint(t.Context(), KeyPointRow{SourceType: models.SourceEpisode, SourceID: ep, SourceTitle: "真实Owner重点", TimeStart: 12, TimeEnd: 20, Content: "Owner人工重点0", CitationsJSON: `["seg-1"]`})
	if e != nil {
		t.Fatal(e)
	}
	if kp.CardVersion != 0 {
		t.Fatal(kp)
	}
	if e = s.SetKeyPointQualityStatus(t.Context(), kp.ID, models.KeyPointOwnerConfirmed); e != nil {
		t.Fatal(e)
	}
	v, e := s.SaveUnderstanding(t.Context(), SaveUnderstandingCommand{QuestionID: q.ID, QuestionRevision: q.Revision, RequestKey: uuid.NewString(), Answer: "据人工重点形成的Owner判断", ModelDataPolicy: "external_allowed", References: []UnderstandingReference{{Kind: "keypoint", ObjectID: kp.ID, Version: 0}}})
	if e != nil || v.References[0].Version != 0 {
		t.Fatal(v, e)
	}
	if ok, e := s.UnderstandingMaySend(t.Context(), v.ID, v.Version, "pod"); e != nil || !ok {
		t.Fatal(ok, e)
	}
	if _, e = s.DB.Exec(`UPDATE keypoint_index SET content='后续真实修改人工重点' WHERE id=?`, kp.ID); e != nil {
		t.Fatal(e)
	}
	if ok, e := s.UnderstandingMaySend(t.Context(), v.ID, v.Version, "pod"); e != nil || ok {
		t.Fatal("changed manual reference remained authorized", ok, e)
	}
	got, e := s.GetUnderstandingSnapshot(t.Context(), v.ID)
	if e != nil || got.References[0].Version != 0 || got.References[0].Body != "Owner人工重点0" || !got.References[0].Changed {
		t.Fatal(got, e)
	}
}
