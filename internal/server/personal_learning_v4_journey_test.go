package server

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
)

type personalV4Fixture struct {
	URL            string   `json:"url"`
	Sources        []string `json:"sources"`
	Snapshots      []string `json:"snapshots"`
	Notes          []string `json:"notes"`
	Questions      []string `json:"questions"`
	DBPath         string   `json:"db_path"`
	Excerpt        string   `json:"excerpt"`
	StudySession   string   `json:"study_session"`
	Reflection     string   `json:"reflection"`
	ReflectionNote string   `json:"reflection_note"`
	Articles       []string `json:"articles"`
}
type personalV4Provider struct{ calls *atomic.Int64 }

func (p personalV4Provider) Name() string { return "pod" }
func (p personalV4Provider) KnowledgeArticleStep(ctx context.Context, req provider.KnowledgeArticleRequest) (*provider.KnowledgeArticleResult, provider.TaskUsage, error) {
	p.calls.Add(1)
	result, usage, err := (purposeBrowserProvider{}).KnowledgeArticleStep(ctx, req)
	if err == nil && req.Stage == "select" {
		topic := *req.Topic
		topic.MaterialIDs = nil
		topic.Selection = nil
		for _, m := range req.Materials {
			topic.MaterialIDs = append(topic.MaterialIDs, m.ID)
			topic.Selection = append(topic.Selection, provider.KnowledgeSelection{MaterialID: m.ID, Selected: true, Role: "support", Reason: "自建同一问题材料"})
		}
		result.Topics = []provider.KnowledgeTopic{topic}
	}
	if err == nil && (req.Stage == "write" || req.Stage == "revise") && req.WritingPurpose.Mode == "synthesis" {
		var sourceID string
		for _, m := range req.Materials {
			if m.Kind == "source_note" {
				sourceID = m.ID
				break
			}
		}
		result.Blocks = []provider.KnowledgeBlock{{Kind: "source", Text: "自建来源提出先核对主动回忆的适用条件。", MaterialIDs: []string{sourceID}}, {Kind: "synthesis", Text: "AI综合：解释时区分来源条件和自己的判断，材料没有的效果不编造。", MaterialIDs: []string{sourceID}}}
	}
	return result, usage, err
}

// One fixture owns all material and modules in this journey. These are explicit
// self-authored test notes, not a substitute for missing real personal notes.
func personalV4Seed(t *testing.T) (*Server, *http.Cookie, *personalV4Fixture, *atomic.Int64, *atomic.Int64) {
	t.Helper()
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "personal-v4@example.com", "password123")
	f := &personalV4Fixture{DBPath: srv.cfg.DBPath}
	questionCalls := &atomic.Int64{}
	articleCalls := &atomic.Int64{}
	srv.cfg.NarrationDir = filepath.Join(srv.cfg.DataDir, "narrations")
	for _, dir := range []string{srv.cfg.EvidenceDir, srv.cfg.NarrationDir} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 3; i++ {
		ep := seedEpisodeWithTranscript(t, srv, fmt.Sprintf("v4自建来源%d", i+1))
		f.Sources = append(f.Sources, ep)
		// Make absolute original-audio coordinates visible and distinct from an excerpt offset.
		payload := fmt.Sprintf(`{"language":"zh","text":"自建来源%d解释主动回忆与条件","segments":[{"id":"seg-1","start":20,"end":40,"text":"主动回忆须先核对来源条件%d。"},{"id":"seg-2","start":60,"end":90,"text":"个人解释不能冒充来源原话%d。"}]}`, i+1, i+1, i+1)
		if _, err := srv.store.DB.Exec(`UPDATE artifact_versions SET payload=? WHERE source_type='episode' AND source_id=? AND kind='transcript' AND version=1`, payload, ep); err != nil {
			t.Fatal(err)
		}
		name := fmt.Sprintf("v4-original-%d.wav", i)
		path := filepath.Join(srv.cfg.EvidenceDir, name)
		if err := writeBrowserAcceptanceWAV(path, 120); err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err = srv.store.UpsertEvidenceAudio(t.Context(), models.SourceEpisode, ep, name, "wav", int64(len(b)), fmt.Sprintf("%x", sha256.Sum256(b))); err != nil {
			t.Fatal(err)
		}
		snapshot, err := srv.store.FreezeSourceSnapshot(t.Context(), models.SourceEpisode, ep)
		if err != nil {
			t.Fatal(err)
		}
		f.Snapshots = append(f.Snapshots, snapshot.ID)
	}
	if _, err := srv.store.DB.Exec(`UPDATE processing_jobs SET status='succeeded'`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		note, err := srv.store.CreateOwnerNote(t.Context(), models.OwnerNote{SourceType: "episode", SourceID: f.Sources[i%3], Kind: "source_note", Content: fmt.Sprintf("自建笔记%02d：主动回忆需要区分来源条件和个人解释。", i+1), CitationsJSON: `["seg-1"]`})
		if err != nil {
			t.Fatal(err)
		}
		f.Notes = append(f.Notes, note.ID)
	}
	for i := 0; i < 2; i++ {
		q, err := srv.store.CreateLearningQuestion(t.Context(), store.LearningQuestion{Body: fmt.Sprintf("自建问题%d：怎样核对主动回忆条件？", i+1), Goal: "同一跨模块验收旅程，非真实质量评价"})
		if err != nil {
			t.Fatal(err)
		}
		for _, ep := range f.Sources {
			q, err = srv.store.ChangeLearningQuestion(t.Context(), q.ID, q.Revision, store.LearningQuestionChange{Action: "link", Link: provider.LearningQuestionLink{Kind: "source", SourceType: "episode", SourceID: ep}})
			if err != nil {
				t.Fatal(err)
			}
		}
		f.Questions = append(f.Questions, q.ID)
	}
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		questionCalls.Add(1)
		var in struct {
			Model    string                          `json:"model"`
			Messages []provider.QuestionStudyMessage `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			http.Error(w, "invalid", 400)
			return
		}
		var output any
		if in.Model == "journey-generate" {
			var scope provider.QuestionStudyScope
			if err := json.Unmarshal([]byte(in.Messages[1].Content), &scope); err != nil {
				http.Error(w, "scope", 400)
				return
			}
			refs := []provider.QuestionStudyReference{}
			seen := map[string]bool{}
			for _, m := range scope.Materials {
				if len(m.Segments) > 0 && !seen[m.SourceID] {
					seen[m.SourceID] = true
					refs = append(refs, provider.QuestionStudyReference{MaterialKey: m.Key, Revision: m.Revision, SegmentIDs: []string{m.Segments[0].SegmentID}})
				}
			}
			output = provider.QuestionStudyAnswer{Version: provider.QuestionStudyPromptVersion, State: "answered", SourceClaims: []provider.QuestionStudyClaim{{Text: "自建来源要求核对条件。", References: refs[:1]}}, Disagreements: []provider.QuestionStudyClaim{{Text: "三个来源各有条件，不能混成无条件结论。", Conditions: "仅限给定来源和片段", References: refs}}}
		} else {
			output = provider.QuestionStudyReview{Version: "question-study-review-v1", Verdict: "accept", Reason: "自建协议检查", Checks: []provider.QuestionStudyClaimCheck{{Key: "source:0", Relevant: true, Supported: true, ConditionsPreserved: true}, {Key: "disagreement:0", Relevant: true, Supported: true, ConditionsPreserved: true}}}
		}
		raw, _ := json.Marshal(output)
		_ = json.NewEncoder(w).Encode(map[string]any{"model": in.Model, "choices": []map[string]any{{"message": map[string]string{"content": string(raw)}}}, "usage": map[string]int{"prompt_tokens": 30, "completion_tokens": 12}})
	}))
	t.Cleanup(remote.Close)
	srv.cfg.PodBaseURL = remote.URL
	srv.cfg.PodAPIKey = "fixture"
	srv.cfg.PodModel = "journey-generate"
	srv.cfg.PodQuestionStudyReviewModel = "journey-review"
	srv.selector.WithPod("fixture", remote.URL, "journey-generate")
	srv.worker.WithBundleResolver(func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{KnowledgeArticle: personalV4Provider{articleCalls}}, nil
	})
	return srv, cookie, f, questionCalls, articleCalls
}

func personalV4Field(t *testing.T, body, name string) string {
	t.Helper()
	m := regexp.MustCompile(`name="` + name + `" value="([^"]+)"`).FindStringSubmatch(body)
	if len(m) != 2 {
		t.Fatal("missing field", name, body)
	}
	return m[1]
}

func personalV4RunJourney(t *testing.T, srv *Server, cookie *http.Cookie, f *personalV4Fixture, qCalls, aCalls *atomic.Int64) {
	t.Helper()
	ctx := t.Context()
	// Original audio capture -> deliberate reflection save -> absolute note anchor.
	capture := store.ListeningCapture{SourceType: "episode", SourceID: f.Sources[0], Title: "同旅程原音", Anchor: models.NoteAnchor{Mode: "original", SnapshotID: f.Snapshots[0], Version: 1, SegmentIDs: []string{"seg-1"}, Position: 25}}
	id := uuid.NewString()
	reflectionQuestion, err := srv.store.GetLearningQuestion(ctx, f.Questions[0])
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(reflectionCommand{Action: "start", ID: id, Capture: capture, QuestionID: reflectionQuestion.ID, QuestionRevision: reflectionQuestion.Revision})
	r := reflectionPost(srv, cookie, string(raw), true)
	if r.Code != 200 {
		t.Fatal("reflection start", r.Code, r.Body.String())
	}
	raw, _ = json.Marshal(reflectionCommand{Action: "save", ID: id, RequestKey: uuid.NewString(), ExpectedRevision: 1, Answers: store.ReflectionAnswers{Remember: "自建反思：我应核对适用条件", Uncertain: "缺少自己的实践例子", Apply: "回听并再解释"}})
	r = reflectionPost(srv, cookie, string(raw), true)
	if r.Code != 200 {
		t.Fatal("reflection save", r.Code, r.Body.String())
	}
	var reflection store.ListeningReflection
	if err := json.Unmarshal(r.Body.Bytes(), &reflection); err != nil {
		t.Fatal(err)
	}
	f.Reflection = reflection.ID
	f.ReflectionNote = reflection.SavedNoteID
	if reflection.QuestionID != f.Questions[0] {
		t.Fatal("reflection detached from journey question", reflection.QuestionID)
	}
	note, err := srv.store.GetOwnerNote(ctx, reflection.SavedNoteID)
	if err != nil || !strings.Contains(note.AnchorJSON, `"position":25`) {
		t.Fatal("absolute capture lost", note, err)
	}
	// Same question sees all three distinct source notes. A known paid response
	// interrupted at the receipt boundary recovers without a supplier call.
	session, err := srv.store.StartQuestionStudySession(ctx, f.Questions[0], uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	f.StudySession = session.ID
	client, err := srv.selector.QuestionStudy("journey-generate", "journey-review")
	if err != nil {
		t.Fatal(err)
	}
	turn, job, _, err := srv.store.SubmitQuestionStudyTurn(ctx, session.ID, 1, "比较三个来源条件", uuid.NewString(), []string{"note:" + f.Notes[0], "note:" + f.Notes[1], "note:" + f.Notes[2]}, client.Config())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = srv.store.DB.Exec(`CREATE TRIGGER v4_journey_receipt_fault BEFORE INSERT ON usage_records WHEN new.operation='question_study_generate' BEGIN SELECT RAISE(FAIL,'journey receipt failure');END`); err != nil {
		t.Fatal(err)
	}
	if err = srv.worker.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	if qCalls.Load() != 1 {
		t.Fatal(qCalls.Load())
	}
	history, err := srv.store.QuestionStudyHistory(ctx, session.ID)
	if err != nil || len(history) != 0 {
		t.Fatal("unchecked response visible", history, err)
	}
	if _, err = srv.store.DB.Exec(`DROP TRIGGER v4_journey_receipt_fault`); err != nil {
		t.Fatal(err)
	}
	var control int
	if err = srv.store.DB.QueryRow(`SELECT control_revision FROM processing_jobs WHERE id=?`, job.ID).Scan(&control); err != nil {
		t.Fatal(err)
	}
	if _, _, err = srv.store.RetryQuestionStudyGeneration(ctx, job.ID, uuid.NewString(), control, false); err != nil {
		t.Fatal(err)
	}
	if err = srv.worker.ProcessOne(ctx); err != nil || qCalls.Load() != 1 {
		t.Fatal("known response recalled", err, qCalls.Load())
	}
	if err = srv.worker.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	history, err = srv.store.QuestionStudyHistory(ctx, session.ID)
	if err != nil || len(history) != 1 || qCalls.Load() != 2 {
		t.Fatal(history, err, qCalls.Load())
	}
	_ = turn
	path := "/questions/" + f.Questions[0] + "/study?session=" + session.ID
	if r = doWithCookie(srv, cookie, "GET", path); r.Code != 200 || !strings.Contains(r.Body.String(), "三个来源各有条件") {
		t.Fatal(r.Code, r.Body.String())
	}
	// Gap is explicit; a stale Owner disposition leaves its draft in the error page.
	q, err := srv.store.GetLearningQuestion(ctx, f.Questions[0])
	if err != nil {
		t.Fatal(err)
	}
	gapPath := "/questions/" + q.ID + "/gaps"
	r = postForm(t, srv, cookie, gapPath, url.Values{"action": {"create"}, "expected_revision": {strconv.Itoa(q.Revision)}, "kind": {"practice"}, "explanation": {"自建缺口：需要实践例子"}}.Encode())
	if r.Code != 303 {
		t.Fatal(r.Code, r.Body.String())
	}
	gaps, err := srv.store.ListEvidenceGaps(ctx, "question", q.ID)
	if err != nil || len(gaps) != 1 {
		t.Fatal(gaps, err)
	}
	r = postForm(t, srv, cookie, gapPath, url.Values{"action": {"disposition"}, "gap_id": {gaps[0].ID}, "expected_revision": {strconv.Itoa(q.Revision)}, "gap_revision": {"0"}, "state": {"ignored"}, "comment": {"失败仍保留的Owner缺口判断"}}.Encode())
	if r.Code != 409 || !strings.Contains(r.Body.String(), "失败仍保留的Owner缺口判断") {
		t.Fatal(r.Code, r.Body.String())
	}
	ex, err := srv.store.CreateLearningExcerpt(ctx, f.Snapshots[0], []string{"seg-1"})
	if err != nil {
		t.Fatal(err)
	}
	f.Excerpt = ex.ID
	if _, err = srv.store.ChangeListeningQueue(ctx, 0, store.ListeningQueueChange{Action: "add", Mode: "excerpt", SourceType: models.SourceEpisode, SourceID: f.Sources[0], ExcerptID: ex.ID}); err != nil {
		t.Fatal(err)
	}
	if ex.StartSeconds != 20 || ex.EndSeconds != 40 {
		t.Fatal("excerpt became relative", ex)
	}
	// Two immutable understanding versions, explicitly select current v2.
	v1, err := srv.store.SaveUnderstanding(ctx, store.SaveUnderstandingCommand{QuestionID: q.ID, QuestionRevision: q.Revision, RequestKey: uuid.NewString(), Answer: "自建理解v1：原话与解释分开", ModelDataPolicy: "external_allowed", References: []store.UnderstandingReference{{Kind: "note", ObjectID: f.Notes[0], Version: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	v2, err := srv.store.SaveUnderstanding(ctx, store.SaveUnderstandingCommand{QuestionID: q.ID, QuestionRevision: q.Revision, HeadRevision: 1, ParentID: v1.ID, RequestKey: uuid.NewString(), Answer: "自建理解v2：补听后保留适用条件", ModelDataPolicy: "external_allowed", References: []store.UnderstandingReference{{Kind: "note", ObjectID: f.Notes[0], Version: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	if err = srv.store.ChooseCurrentUnderstanding(ctx, q.ID, v2.ID, q.Revision, 2, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	old, err := srv.store.GetUnderstandingSnapshot(ctx, v1.ID)
	if err != nil || old.Answer != v1.Answer {
		t.Fatal("understanding overwritten", old, err)
	}
	// Explicit gap/relation membership uses the real v4 resolver and exact snapshot version.
	q, err = srv.store.ChangeLearningQuestion(ctx, q.ID, q.Revision, store.LearningQuestionChange{Action: "link", Link: provider.LearningQuestionLink{Kind: "understanding", ObjectID: v2.ID, Version: 2}})
	if err != nil {
		t.Fatal("explicit understanding membership", err)
	}
	relations, err := srv.store.ListLearningQuestionRelations(ctx, q.ID)
	if err != nil {
		t.Fatal(err)
	}
	linked := false
	for _, r := range relations {
		if r.Kind == "understanding" && r.ObjectID == v2.ID && r.Version == 2 && r.State == "confirmed" {
			linked = true
		}
	}
	if !linked {
		t.Fatal("exact understanding relation absent", relations)
	}
	recoveryKey := uuid.NewString()
	staleUnderstanding := postForm(t, srv, cookie, "/questions/understanding-action", url.Values{"action": {"save"}, "owner_confirmed": {"yes"}, "question_id": {q.ID}, "question_revision": {strconv.Itoa(q.Revision)}, "head_revision": {"0"}, "parent_id": {v1.ID}, "request_key": {recoveryKey}, "answer": {"失败后保留的理解草稿"}}.Encode())
	if staleUnderstanding.Code != 409 || !strings.Contains(staleUnderstanding.Body.String(), "失败后保留的理解草稿") {
		t.Fatal("stale understanding lost Owner draft", staleUnderstanding.Code, staleUnderstanding.Body.String())
	}
	for _, exact := range []string{`data-understanding-recovery`, `name="answer" rows="6">失败后保留的理解草稿</textarea>`, `name="head_revision" value="0"`, `name="parent_id" value="` + v1.ID + `"`, `name="request_key" value="` + recoveryKey + `"`} {
		if !strings.Contains(staleUnderstanding.Body.String(), exact) {
			t.Fatal("recovery form lost exact field", exact)
		}
	}
	profile, err := srv.store.EnsureDefaultEditorialProfile(ctx)
	if err != nil {
		t.Fatal(err)
	}
	article, created, err := srv.enqueueKnowledgeArticleScope(ctx, profile.ID, false, store.KnowledgeScope{QuestionID: q.ID, WritingMode: "synthesis", MaterialIDs: []string{f.Notes[0], f.Notes[1], f.Notes[2], v2.ID}})
	if err != nil || !created {
		t.Fatal("same-question synthesis", created, err)
	}
	for step := 0; step < 4; step++ {
		if err = srv.worker.ProcessOne(ctx); err != nil {
			t.Fatal(err)
		}
	}
	firstArticle, err := srv.store.GetKnowledgeArticle(ctx, article.ID)
	if err != nil || firstArticle.PassedRevision != 1 {
		t.Fatal("synthesis exact review", firstArticle, err)
	}
	var firstInput, firstHash string
	if err = srv.store.DB.QueryRow(`SELECT input_json,content_hash FROM knowledge_article_revisions WHERE article_id=? AND revision=1`, article.ID).Scan(&firstInput, &firstHash); err != nil {
		t.Fatal(err)
	}
	var selected provider.KnowledgeArticleRequest
	if err = json.Unmarshal([]byte(firstInput), &selected); err != nil {
		t.Fatal(err)
	}
	foundUnderstanding := false
	for _, m := range selected.Materials {
		if m.ID == v2.ID && m.Kind == "understanding" && m.Version == 2 && len(m.UnderstandingReferences) > 0 {
			foundUnderstanding = true
		}
	}
	if !foundUnderstanding {
		t.Fatal("explicit selected current understanding missing actual request", selected)
	}
	if err = srv.store.QueueKnowledgePurposeRevision(ctx, article.ID, 1, "explanation", "同一问题改为知识解释，保留旧通过版", srv.cfg.KnowledgeReviewModel()); err != nil {
		t.Fatal(err)
	}
	for step := 0; step < 2; step++ {
		if err = srv.worker.ProcessOne(ctx); err != nil {
			t.Fatal(err)
		}
	}
	currentArticle, err := srv.store.GetKnowledgeArticle(ctx, article.ID)
	if err != nil || currentArticle.PassedRevision <= 1 {
		t.Fatal("same-question explanation exact review", currentArticle, err)
	}
	var preservedHash, secondInput string
	if err = srv.store.DB.QueryRow(`SELECT content_hash FROM knowledge_article_revisions WHERE article_id=? AND revision=1`, article.ID).Scan(&preservedHash); err != nil || preservedHash != firstHash {
		t.Fatal("old passed purpose overwritten", err, preservedHash, firstHash)
	}
	if err = srv.store.DB.QueryRow(`SELECT input_json FROM knowledge_article_revisions WHERE article_id=? AND revision=?`, article.ID, currentArticle.PassedRevision).Scan(&secondInput); err != nil {
		t.Fatal(err)
	}
	var second provider.KnowledgeArticleRequest
	if err = json.Unmarshal([]byte(secondInput), &second); err != nil || second.WritingPurpose == nil || second.WritingPurpose.Mode != "explanation" || second.Question == nil || second.Question.ID != q.ID {
		t.Fatal("purpose changed question scope", second, err)
	}
	f.Articles = []string{article.ID, article.ID}
	// Feedback is separate from human scoring; precise paragraph hash binds it.
	var blocksRaw, hash string
	var revision int
	if err = srv.store.DB.QueryRow(`SELECT a.passed_revision,r.blocks_json,r.content_hash FROM knowledge_articles a JOIN knowledge_article_revisions r ON r.article_id=a.id AND r.revision=a.passed_revision WHERE a.id=?`, f.Articles[0]).Scan(&revision, &blocksRaw, &hash); err != nil {
		t.Fatal(err)
	}
	var blocks []provider.KnowledgeBlock
	if err = json.Unmarshal([]byte(blocksRaw), &blocks); err != nil || len(blocks) == 0 {
		t.Fatal(err)
	}
	r = postForm(t, srv, cookie, "/quality-cases/action", url.Values{"action": {"feedback"}, "article_id": {f.Articles[0]}, "revision": {strconv.Itoa(revision)}, "content_hash": {hash}, "paragraph_index": {"0"}, "paragraph_hash": {articleParagraphHash(blocks[0].Text)}, "category": {"shallow"}, "comment": {"自建反馈：补充具体适用条件"}}.Encode())
	if r.Code != 303 {
		t.Fatal(r.Code, r.Body.String())
	}
	// Real no-model ZIP generation, not a manually published placeholder.
	r = postForm(t, srv, cookie, "/learning-exports/preview", url.Values{"selection": {"question:" + q.ID}, "include_excerpts": {"yes"}, "include_history": {"yes"}}.Encode())
	if r.Code != 200 {
		t.Fatal("export preview", r.Code, r.Body.String())
	}
	create := url.Values{"confirmed": {"yes"}, "preview_id": {personalV4Field(t, r.Body.String(), "preview_id")}, "preview_hash": {personalV4Field(t, r.Body.String(), "preview_hash")}, "request_key": {uuid.NewString()}}
	create.Set("preview_hash", "tampered")
	bad := postForm(t, srv, cookie, "/learning-exports/create", create.Encode())
	if bad.Code < 400 {
		t.Fatal("tampered export accepted")
	}
	create.Set("preview_hash", personalV4Field(t, r.Body.String(), "preview_hash"))
	r = postForm(t, srv, cookie, "/learning-exports/create", create.Encode())
	if r.Code != 303 {
		t.Fatal(r.Code, r.Body.String())
	}
	exportID := strings.TrimPrefix(r.Header().Get("Location"), "/learning-exports#export-")
	beforeA, beforeQ := aCalls.Load(), qCalls.Load()
	if err = srv.worker.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	download := doWithCookie(srv, cookie, "GET", "/learning-exports/"+exportID+"/download")
	if download.Code != 200 {
		t.Fatal("ZIP download", download.Code, download.Body.String())
	}
	archive, err := zip.NewReader(bytes.NewReader(download.Body.Bytes()), int64(download.Body.Len()))
	if err != nil || len(archive.File) == 0 {
		t.Fatal("not a real ZIP", err)
	}
	// Foreground atomic offline sync + lost acknowledgement replay in this same DB.
	device := uuid.NewString()
	r = queueJSONRequest(t, srv, cookie, "POST", "/api/offline/enable", fmt.Sprintf(`{"device_id":%q}`, device), true)
	if r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	r = queueJSONRequest(t, srv, cookie, "POST", "/api/offline/manifest", fmt.Sprintf(`{"device_id":%q,"sources":[{"source_type":"episode","source_id":%q}]}`, device, f.Sources[0]), true)
	if r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	var manifest OfflineManifest
	if err = json.Unmarshal(r.Body.Bytes(), &manifest); err != nil {
		t.Fatal(err)
	}
	op := store.OfflineOperation{SchemaVersion: 1, Namespace: manifest.Namespace, UUID: uuid.NewString(), Kind: "organizing_draft", SourceType: models.SourceEpisode, SourceID: f.Sources[0], SnapshotID: f.Snapshots[0], EditedAt: time.Now().Unix(), Payload: json.RawMessage(`{"content":"同旅程离线整理：明确区分自己的理解"}`)}
	first := offlineAcceptanceSync(t, srv, cookie, device, manifest, op)
	again := offlineAcceptanceSync(t, srv, cookie, device, manifest, op)
	if first.Code != 200 || again.Code != 200 || first.Body.String() != again.Body.String() {
		t.Fatal("offline acknowledgement replay", first.Code, again.Code, first.Body.String(), again.Body.String())
	}
	if aCalls.Load() != beforeA || qCalls.Load() != beforeQ {
		t.Fatal("export/offline invoked model", aCalls.Load(), qCalls.Load())
	}
}

func TestPersonalLearningV4CrossModuleJourney(t *testing.T) {
	srv, cookie, f, q, a := personalV4Seed(t)
	personalV4RunJourney(t, srv, cookie, f, q, a)
}

// Explicit opt-in browser fixture, same seed and module chain as the Go test.
func TestPersonalLearningV4BrowserHarness(t *testing.T) {
	if os.Getenv("CWP_PERSONAL_V4_BROWSER") != "1" {
		t.Skip("explicit independent v4 browser journey")
	}
	stop := os.Getenv("CWP_PERSONAL_V4_STOP")
	if stop == "" {
		t.Fatal("explicit stop file required")
	}
	srv, cookie, f, q, a := personalV4Seed(t)
	personalV4RunJourney(t, srv, cookie, f, q, a)
	listener, err := net.Listen("tcp", "127.0.0.1:18131")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: srv.Router(), ReadHeaderTimeout: 5 * time.Second}
	go server.Serve(listener)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); srv.worker.Run(ctx) }()
	defer func() {
		cancel()
		<-done
		shutdown, end := context.WithTimeout(context.Background(), 5*time.Second)
		defer end()
		server.Shutdown(shutdown)
	}()
	f.URL = "http://" + listener.Addr().String()
	payload, _ := json.Marshal(f)
	fmt.Printf("PERSONAL_V4_BROWSER_READY=%s\n", payload)
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-t.Context().Done():
			t.Fatal("journey cancelled")
		case <-ticker.C:
			if _, err = os.Stat(stop); err == nil {
				fmt.Printf("PERSONAL_V4_MODEL_COUNTS question=%d article=%d\n", q.Load(), a.Load())
				final := map[string]map[string]int{}
				for label, query := range map[string]string{"jobs_by_status": "SELECT status,count(*) FROM processing_jobs GROUP BY status", "receipts_by_operation": "SELECT operation,count(*) FROM usage_records GROUP BY operation"} {
					rows, queryErr := srv.store.DB.QueryContext(t.Context(), query)
					if queryErr != nil {
						t.Fatal(queryErr)
					}
					counts := map[string]int{}
					for rows.Next() {
						var key string
						var count int
						if queryErr = rows.Scan(&key, &count); queryErr != nil {
							t.Fatal(queryErr)
						}
						counts[key] = count
					}
					queryErr = rows.Err()
					rows.Close()
					if queryErr != nil {
						t.Fatal(queryErr)
					}
					final[label] = counts
				}
				stats, _ := json.Marshal(final)
				fmt.Printf("PERSONAL_V4_FINAL_COUNTS=%s\n", stats)
				return
			}
		}
	}
}
