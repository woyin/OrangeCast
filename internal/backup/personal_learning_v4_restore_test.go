package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/queue"
	"github.com/woyin/orangecast/internal/store"
)

// TestPersonalLearningV4RealRestoreRelationships uses production commands and a
// real v2 archive. Every assertion follows an exact identity/version or owner
// decision; a restored row count alone cannot prove any relationship intact.
func TestPersonalLearningV4RealRestoreRelationships(t *testing.T) {
	ctx := t.Context()
	check := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	src := t.TempDir()
	evidence := filepath.Join(src, "evidence")
	check(os.MkdirAll(evidence, 0700))
	s, e := store.Open(filepath.Join(src, dbFileName))
	check(e)
	defer s.Close()
	podcast, e := s.CreatePodcast(ctx, "https://v4-restore.test/feed", "恢复资料", "", "")
	check(e)
	_, e = s.MergeEpisodes(ctx, podcast.ID, []models.Episode{{GUID: "v4-original", Title: "恢复后来源和Owner理解分开", AudioURL: "https://v4-restore.test/original.wav"}})
	check(e)
	episodes, e := s.ListEpisodes(ctx, podcast.ID)
	check(e)
	if len(episodes) != 1 {
		t.Fatal(episodes)
	}
	ep := episodes[0].ID
	seed, e := s.EnqueueJob(ctx, models.SourceEpisode, ep, models.JobTranscribe)
	check(e)
	segments := []provider.Segment{{ID: "v4-a", Start: 5, End: 10, Text: "先明确适用条件"}, {ID: "v4-b", Start: 20, End: 25, Text: "再检查另一种解释"}}
	payload, e := json.Marshal(map[string]any{"language": "zh", "text": "先明确适用条件，再检查另一种解释", "segments": segments})
	check(e)
	version, e := s.CreateArtifactVersion(ctx, models.SourceEpisode, ep, store.KindTranscript, "pod", "frozen-asr", "transcript-v1", seed.ID, string(payload))
	check(e)
	check(s.SetCurrentVersion(ctx, models.SourceEpisode, ep, store.KindTranscript, version))
	check(s.MarkJobSucceeded(ctx, seed.ID))
	var wav bytes.Buffer
	wav.WriteString("RIFF")
	check(binary.Write(&wav, binary.LittleEndian, uint32(36+30*16000)))
	wav.WriteString("WAVEfmt ")
	check(binary.Write(&wav, binary.LittleEndian, uint32(16)))
	check(binary.Write(&wav, binary.LittleEndian, uint16(1)))
	check(binary.Write(&wav, binary.LittleEndian, uint16(1)))
	check(binary.Write(&wav, binary.LittleEndian, uint32(8000)))
	check(binary.Write(&wav, binary.LittleEndian, uint32(16000)))
	check(binary.Write(&wav, binary.LittleEndian, uint16(2)))
	check(binary.Write(&wav, binary.LittleEndian, uint16(16)))
	wav.WriteString("data")
	check(binary.Write(&wav, binary.LittleEndian, uint32(30*16000)))
	wav.Write(make([]byte, 30*16000))
	audio := wav.Bytes()
	digest := sha256.Sum256(audio)
	audioHash := hex.EncodeToString(digest[:])
	rel := "v4-original.wav"
	check(os.WriteFile(filepath.Join(evidence, rel), audio, 0600))
	check(s.UpsertEvidenceAudio(ctx, models.SourceEpisode, ep, rel, "wav", int64(len(audio)), audioHash))
	snapshot, e := s.FreezeSourceSnapshot(ctx, models.SourceEpisode, ep)
	check(e)
	one, e := s.CreateLearningExcerpt(ctx, snapshot.ID, []string{"v4-a"})
	check(e)
	two, e := s.CreateLearningExcerpt(ctx, snapshot.ID, []string{"v4-b"})
	check(e)
	listening, e := s.GetListeningQueue(ctx)
	check(e)
	for _, clip := range []*models.LearningExcerpt{one, two} {
		listening, e = s.ChangeListeningQueue(ctx, listening.Revision, store.ListeningQueueChange{Action: "add", SourceType: models.SourceEpisode, SourceID: ep, Mode: "excerpt", ExcerptID: clip.ID})
		check(e)
	}
	listening, e = s.ChangeListeningQueue(ctx, listening.Revision, store.ListeningQueueChange{Action: "play", ItemID: listening.Items[1].ID})
	check(e)
	for _, p := range []*models.ListeningProgress{{SourceType: models.SourceEpisode, SourceID: ep, Mode: "original", AudioSHA256: audioHash, ItemOffsetSeconds: 17, Speed: 1}, {SourceType: models.SourceEpisode, SourceID: ep, Mode: "excerpt", AudioSHA256: audioHash, ExcerptID: one.ID, SnapshotID: snapshot.ID, ItemOffsetSeconds: 7, Speed: 1.25}, {SourceType: models.SourceEpisode, SourceID: ep, Mode: "excerpt", AudioSHA256: audioHash, ExcerptID: two.ID, SnapshotID: snapshot.ID, ItemOffsetSeconds: 23, Speed: 0.75}} {
		_, e = s.SaveListeningProgressCAS(ctx, p, 0)
		check(e)
	}
	note, e := s.CreateOwnerNote(ctx, models.OwnerNote{SourceType: "episode", SourceID: ep, Kind: "owner_reflection", Content: "Owner先写自己的解释，再检查条件"})
	check(e)
	secondSource, e := s.CreatePastedDocument(ctx, "另一种解释的条件", "条件改变时解释也可能改变。")
	check(e)
	documentCitations, e := json.Marshal([]string{store.DocumentSegments(secondSource)[0].ID})
	check(e)
	secondEvidence, e := s.CreateOwnerNote(ctx, models.OwnerNote{SourceType: "document", SourceID: secondSource.ID, Kind: "source_note", Content: secondSource.Content, CitationsJSON: string(documentCitations)})
	check(e)
	other, e := s.CreateOwnerNote(ctx, models.OwnerNote{SourceType: "document", SourceID: secondSource.ID, Kind: "owner_reflection", Content: "Owner认为另一种解释也要检验"})
	check(e)
	card := provider.KnowledgeCard{KeyPoints: []provider.KeyPoint{{Content: "先明确适用条件", Description: "明确解释边界", Citations: []string{"v4-a"}}}}
	cardRaw, e := json.Marshal(card)
	check(e)
	cardJob, _, e := s.EnqueueJobIdempotent(ctx, store.JobIntentSpec{SourceType: models.SourceEpisode, SourceID: ep, JobType: models.JobAnalyze, IntentID: "v4-indexed-card"})
	check(e)
	cardVersion, e := s.CreateArtifactVersion(ctx, models.SourceEpisode, ep, store.KindKnowledgeCard, "pod", "frozen-card", "card-v1", cardJob.ID, string(cardRaw))
	check(e)
	check(s.SetCurrentVersion(ctx, models.SourceEpisode, ep, store.KindKnowledgeCard, cardVersion))
	_, e = s.IndexKeyPoints(ctx, models.SourceEpisode, ep, "恢复资料", cardVersion, &card, segments)
	check(e)
	check(s.MarkJobSucceeded(ctx, cardJob.ID))
	points, _, e := s.ListKeyPoints(ctx, 1, 10)
	check(e)
	if len(points) != 1 {
		t.Fatal(points)
	}
	point := points[0]
	check(s.SetKeyPointQualityStatus(ctx, point.ID, models.KeyPointOwnerConfirmed))

	question, e := s.CreateLearningQuestion(ctx, store.LearningQuestion{Body: "怎样保留理解的条件？", Goal: "写出有出处的判断"})
	check(e)
	for _, link := range []provider.LearningQuestionLink{{Kind: "note", ObjectID: note.ID, SourceType: "episode", SourceID: ep, Version: note.Revision}, {Kind: "note", ObjectID: other.ID, SourceType: "document", SourceID: secondSource.ID, Version: other.Revision}, {Kind: "evidence", ObjectID: snapshot.ID}, {Kind: "keypoint", ObjectID: point.ID, SourceType: "episode", SourceID: ep, Version: point.CardVersion}} {
		question, e = s.ChangeLearningQuestion(ctx, question.ID, question.Revision, store.LearningQuestionChange{Action: "link", Link: link})
		check(e)
	}
	u1, e := s.SaveUnderstanding(ctx, store.SaveUnderstandingCommand{QuestionID: question.ID, QuestionRevision: question.Revision, RequestKey: uuid.NewString(), Answer: "Owner第一版跨材料理解", Uncertainty: "还没有实践例子", NextStep: "找真实例子", ModelDataPolicy: "external_allowed", References: []store.UnderstandingReference{{Kind: "note", ObjectID: note.ID, Version: note.Revision}}})
	check(e)
	check(s.ChooseCurrentUnderstanding(ctx, question.ID, u1.ID, question.Revision, 1, uuid.NewString()))
	u2, e := s.SaveUnderstanding(ctx, store.SaveUnderstandingCommand{QuestionID: question.ID, QuestionRevision: question.Revision, HeadRevision: 2, ParentID: u1.ID, RequestKey: uuid.NewString(), Answer: "Owner第二版保留两种解释的条件", Uncertainty: "实践还需要证实", NextStep: "比较真实情境", ModelDataPolicy: "external_allowed", References: []store.UnderstandingReference{{Kind: "note", ObjectID: note.ID, Version: note.Revision}, {Kind: "note", ObjectID: other.ID, Version: other.Revision}}})
	check(e)
	check(s.ChooseCurrentUnderstanding(ctx, question.ID, u2.ID, question.Revision, 3, uuid.NewString()))
	question, e = s.ChangeLearningQuestion(ctx, question.ID, question.Revision, store.LearningQuestionChange{Action: "link", Link: provider.LearningQuestionLink{Kind: "understanding", ObjectID: u2.ID, Version: u2.Version}})
	check(e)
	profile, e := s.EnsureDefaultEditorialProfile(ctx)
	check(e)
	prefs, e := s.GetKnowledgeArticleSettings(ctx)
	check(e)
	prefs.WritingMode = "explanation"
	prefs.PreviewWritingPlan = true
	check(s.SetKnowledgeArticleSettings(ctx, prefs))
	req, _, e := s.BuildKnowledgeArticleRequest(ctx, profile.ID, "pod")
	check(e)
	req.ReviewModel = "frozen-review"
	if len(req.Materials) < 2 {
		t.Fatal("real materials missing", req.Materials)
	}
	article, _, e := s.ReserveKnowledgeArticle(ctx, profile.ID, "pod", "frozen-writer", req, false)
	check(e)
	stage := func(id string, result *provider.KnowledgeArticleResult) {
		t.Helper()
		job, e := s.GetJob(ctx, id)
		check(e)
		ex, e := s.GetJobExecution(ctx, id)
		check(e)
		var input store.KnowledgeStageInput
		check(json.Unmarshal([]byte(ex.InputSnapshotJSON), &input))
		check(provider.ValidateKnowledgeResult(input.Request, result))
		check(s.CommitKnowledgeStage(ctx, job, input, result))
		check(s.MarkJobSucceeded(ctx, id))
	}
	findStage := func(articleID, stageName string) string {
		t.Helper()
		var id string
		check(s.DB.QueryRowContext(ctx, `SELECT id FROM processing_jobs WHERE source_id=? AND json_extract(input_snapshot_json,'$.stage')=? ORDER BY created_at DESC,id DESC LIMIT 1`, articleID, stageName).Scan(&id))
		return id
	}
	ids := []string{}
	comparisonIDs := []string{}
	for _, m := range req.Materials {
		ids = append(ids, m.ID)
		if (m.Kind == "keypoint" && m.SourceID == ep) || (m.Kind == "source_note" && m.ID == secondEvidence.ID && m.SourceID == secondSource.ID) {
			comparisonIDs = append(comparisonIDs, m.ID)
		}
	}
	if len(comparisonIDs) != 2 {
		t.Fatalf("two real distinct-source materials missing: %+v", req.Materials)
	}
	topic := provider.KnowledgeTopic{Title: "如何保留解释条件", Question: "怎样可靠理解", Thesis: "来源和Owner判断分开", Outline: "概念、条件、检验", MaterialIDs: ids, Score: 90, Sufficient: true}
	stage(findStage(article.ID, "discover"), &provider.KnowledgeArticleResult{Topics: []provider.KnowledgeTopic{topic}})
	plan1, e := s.GetKnowledgeWritingPlan(ctx, article.ID)
	check(e)
	plan2, e := s.EditKnowledgeWritingPlanPurpose(ctx, article.ID, plan1.Revision, plan1.Hash, "先解释概念，再比较适用条件", "comparison")
	check(e)
	confirmed, e := s.ConfirmKnowledgeWritingPlan(ctx, article.ID, plan2.Revision, plan2.Hash)
	check(e)
	stage(confirmed.JobID, &provider.KnowledgeArticleResult{Title: "保留解释条件", Blocks: []provider.KnowledgeBlock{
		{PurposeSection: "positions", Kind: "synthesis", Text: "来源提出解释条件；Owner记录了另一种理解，二者身份分开。", MaterialIDs: comparisonIDs},
		{PurposeSection: "agreement", Kind: "synthesis", Text: "材料不足以认定跨来源共识，先核对各自的适用条件。", MaterialIDs: comparisonIDs},
		{PurposeSection: "disagreement", Kind: "synthesis", Text: "尚未找到经过核对的分歧，不能把不同条件当作矛盾。", MaterialIDs: comparisonIDs},
		{PurposeSection: "conditions", Kind: "synthesis", Text: "以上仅来自当前资料及个人记录，仍需寻找真实实践情境。", MaterialIDs: comparisonIDs},
	}})
	passed := true
	stage(findStage(article.ID, "review"), &provider.KnowledgeArticleResult{Passed: &passed})
	ready, e := s.GetKnowledgeArticle(ctx, article.ID)
	check(e)
	revision, e := s.GetKnowledgeRevision(ctx, article.ID, ready.PassedRevision)
	check(e)
	feedback, e := s.RecordArticleQualityFeedback(ctx, article.ID, revision.Revision, -1, revision.ContentHash, "", "useful", "条件表达有帮助")
	check(e)
	case1, e := s.AcceptArticleQualityCase(ctx, feedback, uuid.NewString(), 0, "保留材料的确切条件")
	check(e)
	case2, e := s.AcceptArticleQualityCase(ctx, feedback, uuid.NewString(), 1, "比较条件并给出实践路径")
	check(e)
	quality, e := s.BuildArticleQualityManifest(ctx, []string{case1.ID, case2.ID}, "pod")
	check(e)
	question, e = s.ChangeLearningQuestion(ctx, question.ID, question.Revision, store.LearningQuestionChange{Action: "link", Link: provider.LearningQuestionLink{Kind: "article", ObjectID: article.ID, Version: revision.Revision}})
	check(e)
	gap, e := s.CreateEvidenceGap(ctx, "question", question.ID, question.Revision, "practice", "owner", "需要真实实践例子", "仅有解释材料")
	check(e)
	gap, e = s.ChangeEvidenceGap(ctx, "question", question.ID, gap.ID, question.Revision, gap.Revision, "helpful", "第一条资料有帮助")
	check(e)
	gap, e = s.ChangeEvidenceGap(ctx, "question", question.ID, gap.ID, question.Revision, gap.Revision, "insufficient", "还没有真实实践")
	check(e)
	gapHistory, e := s.ListEvidenceGapOperations(ctx, gap.ID)
	check(e)
	reflection, e := s.StartListeningReflection(ctx, uuid.NewString(), store.ListeningCapture{SourceType: "episode", SourceID: ep, Title: episodes[0].Title, Anchor: models.NoteAnchor{Position: 7, SegmentIDs: []string{"v4-a"}, SnapshotID: snapshot.ID, Version: version, AudioSHA256: audioHash, Mode: "original"}}, question.ID, question.Revision)
	check(e)
	reflection, e = s.SaveListeningReflection(ctx, reflection.ID, uuid.NewString(), reflection.Revision, store.ReflectionAnswers{Remember: "我记住了条件", Uncertain: "还缺实践", Apply: "比较情境后应用"})
	check(e)
	adopted, e := s.GetOwnerNote(ctx, reflection.SavedNoteID)
	check(e)
	selection, e := s.SaveCreationSelection(ctx, &models.CreationSelection{EditorialProfileID: profile.ID, Title: "保留确切理解材料", NoteIDs: []string{note.ID, other.ID}, MaterialIDs: []string{point.ID}})
	check(e)
	// Weekly explanation/adoption retains the frozen material selection and Owner answer history.
	weekly, _, e := s.ReserveLearningReview(ctx, profile.ID, "pod", "weekly-review", time.Now(), false)
	check(e)
	if weekly == nil {
		t.Fatal("weekly batch missing")
	}
	check(s.CommitLearningReview(ctx, weekly.JobID, weekly.ID, &provider.KnowledgeArticleResult{Questions: []provider.LearningReviewQuestion{{Question: "怎样说明边界？", AnswerBasis: "从确切材料说明适用条件", MaterialIDs: []string{note.ID}}}}))
	check(s.MarkJobSucceeded(ctx, weekly.JobID))
	weeklyItems, e := s.ListLearningReviewItems(ctx, weekly.ID)
	check(e)
	if len(weeklyItems) != 1 {
		t.Fatal(weeklyItems)
	}
	check(s.AnswerLearningReview(ctx, weeklyItems[0].ID, "Owner先核对条件再解释", "explain", "answer", weeklyItems[0].Revision))
	weeklyItems, e = s.ListLearningReviewItems(ctx, weekly.ID)
	check(e)
	weeklyNote, e := s.SaveLearningReviewNote(ctx, weeklyItems[0].ID, weeklyItems[0].Revision)
	check(e)
	weeklyItems, e = s.ListLearningReviewItems(ctx, weekly.ID)
	check(e)
	weeklyAnswers, e := s.LearningReviewAnswerHistory(ctx, weeklyItems[0].ID)
	check(e)
	weekly, e = s.GetLearningReviewBatch(ctx, weekly.ID)
	check(e)
	// Accepted new question-study and legacy single-source sessions remain separate.
	cfg := provider.QuestionStudyConfig{Provider: "pod", ConnectionID: strings.Repeat("a", 64), GenerationModel: "frozen-generate", ReviewModel: "frozen-review", IndependentModel: true}
	session, e := s.StartQuestionStudySession(ctx, question.ID, uuid.NewString())
	check(e)
	probe, e := s.FreezeQuestionStudyScope(ctx, session.ID, "说明条件", "pod", nil)
	check(e)
	for _, m := range probe.Materials {
		if m.Revision < 1 || m.ContentHash != provider.QuestionStudyMaterialHash(m) {
			t.Fatalf("invalid actual frozen material: %#v", m)
		}
	}
	turn, studyJob, _, e := s.SubmitQuestionStudyTurn(ctx, session.ID, session.Revision, "说明条件", uuid.NewString(), nil, cfg)
	check(e)
	execution, e := s.GetJobExecution(ctx, studyJob.ID)
	check(e)
	var studyInput store.QuestionStudyJobInput
	check(json.Unmarshal([]byte(execution.InputSnapshotJSON), &studyInput))
	var basis provider.QuestionStudyMaterial
	for _, m := range studyInput.Scope.Materials {
		if m.Kind == "keypoint" {
			basis = m
			break
		}
	}
	if basis.Key == "" {
		t.Fatal("source basis missing", studyInput.Scope.Materials)
	}
	answer := provider.QuestionStudyAnswer{Version: "question-study-v1", State: "answered", SourceClaims: []provider.QuestionStudyClaim{{Text: "来源要求明确适用条件", Conditions: "当前材料支持这项解释", References: []provider.QuestionStudyReference{{MaterialKey: basis.Key, Revision: basis.Revision, SegmentIDs: []string{"v4-a"}}}}}}
	check(s.CommitQuestionStudyGeneration(ctx, studyJob.ID, studyInput, answer))
	check(s.MarkJobSucceeded(ctx, studyJob.ID))
	turn, e = s.GetQuestionStudyTurn(ctx, turn.ID)
	check(e)
	reviewExecution, e := s.GetJobExecution(ctx, turn.CheckJobID)
	check(e)
	var reviewInput store.QuestionStudyJobInput
	check(json.Unmarshal([]byte(reviewExecution.InputSnapshotJSON), &reviewInput))
	check(s.CommitQuestionStudyReview(ctx, turn.CheckJobID, reviewInput, provider.QuestionStudyReview{Version: "question-study-review-v1", Verdict: "accept", Reason: "给定材料支持", Checks: []provider.QuestionStudyClaimCheck{{Key: "source:0", Relevant: true, Supported: true, ConditionsPreserved: true}}}))
	check(s.MarkJobSucceeded(ctx, turn.CheckJobID))
	newHistory, e := s.QuestionStudyHistory(ctx, session.ID)
	check(e)
	legacyCfg := provider.QuestionStudyConfig{Provider: "groq", ConnectionID: strings.Repeat("b", 64), GenerationModel: "frozen-qa", ReviewModel: "frozen-qa"}
	oldTurn, oldJob, _, e := s.SubmitLegacyStudyTurn(ctx, models.SourceEpisode, ep, "", 1, "解释来源条件", uuid.NewString(), legacyCfg)
	check(e)
	oldEx, e := s.GetJobExecution(ctx, oldJob.ID)
	check(e)
	var oldInput store.LegacyStudyJobInput
	check(json.Unmarshal([]byte(oldEx.InputSnapshotJSON), &oldInput))
	check(s.CommitLegacyStudyGeneration(ctx, oldJob.ID, oldInput, &provider.StudyChatResult{Answer: &provider.StudyChatMessage{Role: "assistant", Content: "来源先要求明确适用条件。", ReferenceSegmentIDs: []string{"v4-a"}}}))
	check(s.MarkJobSucceeded(ctx, oldJob.ID))
	oldTurn, e = s.GetLegacyStudyTurn(ctx, oldTurn.ID)
	check(e)
	oldReviewEx, e := s.GetJobExecution(ctx, oldTurn.CheckJobID)
	check(e)
	var oldReview store.LegacyStudyJobInput
	check(json.Unmarshal([]byte(oldReviewEx.InputSnapshotJSON), &oldReview))
	check(s.CommitLegacyStudyReview(ctx, oldTurn.CheckJobID, oldReview, provider.ReferenceCheckResult{Related: true}))
	check(s.MarkJobSucceeded(ctx, oldTurn.CheckJobID))
	oldMessages, e := s.ListStudyMessages(ctx, oldTurn.SessionID, true)
	check(e)
	// A stopped, already-paid successor keeps receipt facts but cannot publish.
	oldSession, e := s.GetStudySession(ctx, oldTurn.SessionID)
	check(e)
	stoppedTurn, stoppedJob, _, e := s.SubmitLegacyStudyTurn(ctx, models.SourceEpisode, ep, oldTurn.SessionID, oldSession.Revision, "还有哪些反例", uuid.NewString(), legacyCfg)
	check(e)
	stoppedEx, e := s.GetJobExecution(ctx, stoppedJob.ID)
	check(e)
	var stoppedInput store.LegacyStudyJobInput
	check(json.Unmarshal([]byte(stoppedEx.InputSnapshotJSON), &stoppedInput))
	check(s.RecordLegacyStudyReceipt(ctx, stoppedJob.ID, stoppedInput, &provider.QuestionStudyResponse{Model: "frozen-qa", Content: "原响应已收到", UsageKnown: true, InputUnits: 73, OutputUnits: 11}))
	check(s.ChangeRunControl(ctx, "job", stoppedJob.ID, "stop", "先核对反例", uuid.NewString(), 1, 0))
	oldMessages, e = s.ListStudyMessages(ctx, oldTurn.SessionID, true)
	check(e)
	for _, lane := range []string{"knowledge", "study"} {
		control, e := s.GetRunControl(ctx, "lane", lane)
		check(e)
		check(s.ChangeRunControl(ctx, "lane", lane, "pause", "恢复后仍须Owner明确继续", uuid.NewString(), control.Revision, 0))
	}
	// Device/session grants and browser audio are intentionally excluded as usable restored authorization.
	offline, e := s.EnableOfflineDevice(ctx, "v4-hashed-owner-session", uuid.NewString())
	check(e)
	packID := uuid.NewString()
	offlineManifest, _ := json.Marshal(map[string]any{"pack_id": packID, "files": []map[string]any{{"path": "audio/original.wav", "size": len(audio)}}})
	savedPack, e := s.SaveOfflinePack(ctx, "v4-hashed-owner-session", offline.Namespace, string(offlineManifest), []store.OfflineSource{{SourceType: models.SourceEpisode, SourceID: ep, SnapshotID: snapshot.ID, AudioSHA256: audioHash}})
	check(e)
	browserDir := filepath.Join(src, "browser-cache")
	check(os.MkdirAll(browserDir, 0700))
	check(os.WriteFile(filepath.Join(browserDir, "offline-audio.wav"), audio, 0600))
	bundleDir := filepath.Join(src, "learning-exports")
	check(os.MkdirAll(bundleDir, 0700))
	check(os.WriteFile(filepath.Join(bundleDir, "temporary.zip"), []byte("temporary-private-bundle"), 0600))
	// One known frozen paid response is interrupted before adoption; one unknown response has no checkpoint and NULL cost.
	budget := int64(10000)
	check(s.SetOwnerMonthlyBudget(ctx, &budget))
	check(s.SetModelPrice(ctx, models.ModelPrice{Provider: "pod", Model: "frozen-asr", InputCentsPerMillion: 10, OutputCentsPerMillion: 0}))
	known, _, e := s.EnqueueJobIdempotent(ctx, store.JobIntentSpec{SourceType: models.SourceEpisode, SourceID: ep, JobType: models.JobTranscribe, IntentID: "v4-known-response", InputSnapshotJSON: `{"duration_seconds":30,"source_id":"` + ep + `"}`, ConfiguredProvider: "pod", ConfiguredModel: "frozen-asr", ConfigVersion: "asr-frozen-v1"})
	check(e)
	_, e = s.HoldBudget(ctx, known.ID, "transcription", false, "pod", "frozen-asr", 1000000, 0)
	check(e)
	check(s.MarkJobRemoteCallStarted(ctx, known.ID))
	_, e = s.MarkJobRunning(ctx, known.ID)
	check(e)
	cp, _ := json.Marshal(map[string]any{"job_id": known.ID, "audio_sha256": audioHash, "configured_provider": "pod", "configured_model": "frozen-asr", "provider": "pod", "model": "frozen-asr", "result": map[string]any{"language": "zh", "text": "已支付断点恢复，无新模型调用", "segments": segments}, "usage": provider.TaskUsage{InputUnits: 1000000}})
	check(s.SaveJobCheckpoint(ctx, known.ID, string(cp)))
	check(s.RecordUsageReceipt(ctx, models.UsageReceipt{ReceiptID: known.ID + ":transcription", AttemptID: known.ID + ":response", Operation: "transcription", Provider: "pod", Model: "frozen-asr", InputUnits: 1000000, CostKnown: true, CostCents: 10}))
	unknown, _, e := s.EnqueueJobIdempotent(ctx, store.JobIntentSpec{SourceType: models.SourceEpisode, SourceID: ep, JobType: models.JobTranscribe, IntentID: "v4-unknown-response", InputSnapshotJSON: `{"unknown_call":true}`, ConfiguredProvider: "groq", ConfiguredModel: "unknown-asr", ConfigVersion: "asr-unknown-v1"})
	check(e)
	check(s.MarkJobRemoteCallStarted(ctx, unknown.ID))
	check(s.SaveJobResult(ctx, unknown.ID, "", models.JobResultUnknown))
	check(s.RecordUsageReceipt(ctx, models.UsageReceipt{ReceiptID: unknown.ID + ":unknown", AttemptID: unknown.ID + ":attempt", Operation: "transcription", Provider: "groq", Model: "unknown-asr", CostKnown: false}))
	check(s.MarkJobFailed(ctx, unknown.ID, "远端结果未知；等待Owner显式新尝试"))
	// Schedule the specific interrupted checkpoint first; unrelated queued legacy source work is not part of this continuation assertion.
	_, e = s.DB.ExecContext(ctx, `UPDATE processing_jobs SET priority=10 WHERE id=?`, known.ID)
	check(e)
	frozenKnown, e := s.GetJobExecution(ctx, known.ID)
	check(e)
	frozenUnknown, e := s.GetJobExecution(ctx, unknown.ID)
	check(e)
	// Capture the actual current question after reflection adoption adds its note relation.
	question, e = s.GetLearningQuestion(ctx, question.ID)
	check(e)
	questionRelations, e := s.ListLearningQuestionRelations(ctx, question.ID)
	check(e)
	currentGaps, e := s.ListEvidenceGaps(ctx, "question", question.ID)
	check(e)
	if len(currentGaps) != 1 {
		t.Fatal(currentGaps)
	}
	gap = currentGaps[0]
	// Archive boundary: real files, version 2, independent restore destination.
	archive := filepath.Join(t.TempDir(), "all-v4.tar.gz")
	manifest, e := Create(ctx, s, evidence, archive)
	check(e)
	if manifest.Version != 2 {
		t.Fatal(manifest.Version)
	}
	file, e := os.Open(archive)
	check(e)
	gz, e := gzip.NewReader(file)
	check(e)
	tr := tar.NewReader(gz)
	for {
		h, e := tr.Next()
		if e == io.EOF {
			break
		}
		check(e)
		if strings.HasPrefix(h.Name, "learning-exports/") || strings.HasPrefix(h.Name, "browser-cache/") {
			t.Fatal("temporary/client material entered instance backup", h.Name)
		}
	}
	check(gz.Close())
	check(file.Close())
	dst := filepath.Join(t.TempDir(), "independent-instance")
	restoredManifest, e := Restore(ctx, archive, dst, false)
	check(e)
	if restoredManifest.Version != 2 || restoredManifest.DBSHA256 != manifest.DBSHA256 {
		t.Fatal(restoredManifest)
	}
	restored, e := store.Open(filepath.Join(dst, dbFileName))
	check(e)
	defer restored.Close()
	gotAudio, e := os.ReadFile(filepath.Join(dst, "evidence", rel))
	check(e)
	if !bytes.Equal(gotAudio, audio) {
		t.Fatal("physical original evidence changed")
	}
	for _, excluded := range []string{"learning-exports", "browser-cache"} {
		if _, e = os.Stat(filepath.Join(dst, excluded)); !os.IsNotExist(e) {
			t.Fatal("excluded directory restored", excluded, e)
		}
	}
	assertEqual := func(label string, want, got any, e error) {
		t.Helper()
		check(e)
		if !reflect.DeepEqual(want, got) {
			t.Fatalf("%s changed\nwant=%#v\ngot=%#v", label, want, got)
		}
	}
	gotSecondEvidence, e := restored.GetOwnerNote(ctx, secondEvidence.ID)
	assertEqual("second genuine source evidence", secondEvidence, gotSecondEvidence, e)
	gotQueue, e := restored.GetListeningQueue(ctx)
	assertEqual("two clips and selected queue identity", listening, gotQueue, e)
	for _, clip := range []*models.LearningExcerpt{one, two} {
		got, e := restored.GetLearningExcerpt(ctx, clip.ID)
		assertEqual("immutable excerpt", clip, got, e)
		_, e = restored.CheckLearningExcerpt(ctx, got)
		check(e)
	}
	for _, want := range []struct {
		mode, id        string
		position, speed float64
	}{{"original", "", 17, 1}, {"excerpt", one.ID, 7, 1.25}, {"excerpt", two.ID, 23, .75}} {
		var got *models.ListeningProgress
		if want.mode == "excerpt" {
			got, e = restored.GetLearningExcerptProgress(ctx, models.SourceEpisode, ep, want.id)
		} else {
			got, e = restored.GetListeningProgressMode(ctx, models.SourceEpisode, ep, want.mode)
		}
		check(e)
		if got.Mode != want.mode || got.ItemOffsetSeconds != want.position || got.Speed != want.speed || got.Revision != 1 {
			t.Fatal("progress domains mixed", got, want)
		}
	}
	current, e := restored.GetCurrentUnderstanding(ctx, question.ID)
	assertEqual("exact current understanding with frozen note bodies", u2, current, e)
	oldUnderstanding, e := restored.GetUnderstandingSnapshot(ctx, u1.ID)
	assertEqual("understanding parent/history", u1, oldUnderstanding, e)
	head, e := restored.UnderstandingHead(ctx, question.ID)
	check(e)
	if head.CurrentSnapshotID != u2.ID || head.Revision != 4 {
		t.Fatal("current pointer was inferred", head)
	}
	gotQuestion, e := restored.GetLearningQuestion(ctx, question.ID)
	assertEqual("question revision/body", question, gotQuestion, e)
	gotRelations, e := restored.ListLearningQuestionRelations(ctx, question.ID)
	assertEqual("explicit understanding and preserved legacy material relations", questionRelations, gotRelations, e)
	gotGaps, e := restored.ListEvidenceGaps(ctx, "question", question.ID)
	check(e)
	if len(gotGaps) != 1 {
		t.Fatal(gotGaps)
	}
	assertEqual("helpful then insufficient owner decision", gap, gotGaps[0], nil)
	gotGapHistory, e := restored.ListEvidenceGapOperations(ctx, gap.ID)
	assertEqual("gap operations exact revisions", gapHistory, gotGapHistory, e)
	gotPlan, e := restored.GetKnowledgeWritingPlan(ctx, article.ID)
	assertEqual("purpose/outline/selected request/frozen write job", confirmed, gotPlan, e)
	if gotPlan.Request.WritingPurpose == nil || gotPlan.Request.WritingPurpose.Mode != "comparison" {
		t.Fatal(gotPlan)
	}
	reconfirmed, e := restored.ConfirmKnowledgeWritingPlan(ctx, article.ID, confirmed.Revision, confirmed.Hash)
	check(e)
	if reconfirmed.JobID != confirmed.JobID {
		t.Fatal("restore duplicated immutable plan confirmation", reconfirmed)
	}
	if _, e = restored.ConfirmKnowledgeWritingPlan(ctx, article.ID, plan1.Revision, plan1.Hash); !errors.Is(e, store.ErrConflict) {
		t.Fatal("old plan hash revived", e)
	}
	oldCase, e := restored.GetArticleQualityCase(ctx, case1.ID)
	assertEqual("accepted case version 1 frozen input/body", case1, oldCase, e)
	newCase, e := restored.GetArticleQualityCase(ctx, case2.ID)
	assertEqual("accepted case version 2 frozen input/body", case2, newCase, e)
	gotManifest, e := restored.BuildArticleQualityManifest(ctx, []string{case1.ID, case2.ID}, "pod")
	assertEqual("quality manifest fingerprint and version selection", quality, gotManifest, e)
	restoredWeekly, e := restored.GetLearningReviewBatch(ctx, weekly.ID)
	assertEqual("frozen weekly batch/materials", weekly, restoredWeekly, e)
	restoredWeeklyItems, e := restored.ListLearningReviewItems(ctx, weekly.ID)
	assertEqual("Owner weekly answer/reveal/adoption", weeklyItems, restoredWeeklyItems, e)
	restoredWeeklyAnswers, e := restored.LearningReviewAnswerHistory(ctx, weeklyItems[0].ID)
	assertEqual("immutable weekly explanation history", weeklyAnswers, restoredWeeklyAnswers, e)
	restoredWeeklyNote, e := restored.GetOwnerNote(ctx, weeklyNote.ID)
	assertEqual("exact adopted weekly Owner note", weeklyNote, restoredWeeklyNote, e)
	gotReflection, e := restored.GetListeningReflection(ctx, reflection.ID)
	assertEqual("saved listening reflection adoption", reflection, gotReflection, e)
	gotAdopted, e := restored.GetOwnerNote(ctx, reflection.SavedNoteID)
	assertEqual("exact adopted Owner note", adopted, gotAdopted, e)
	gotSelection, e := restored.GetCreationSelection(ctx, selection.ID)
	assertEqual("exact manual material selection", selection, gotSelection, e)
	gotNewHistory, e := restored.QuestionStudyHistory(ctx, session.ID)
	assertEqual("independently accepted structured question history", newHistory, gotNewHistory, e)
	gotOldMessages, e := restored.ListStudyMessages(ctx, oldTurn.SessionID, true)
	assertEqual("legacy single-source accepted and pending owner messages", oldMessages, gotOldMessages, e)
	stopped, e := restored.GetLegacyStudyTurn(ctx, stoppedTurn.ID)
	check(e)
	if stopped.SessionID != oldTurn.SessionID || stopped.GenerationJobID != stoppedJob.ID {
		t.Fatal(stopped)
	}
	if e = restored.CheckRunControl(ctx, stoppedJob.ID); !errors.Is(e, store.ErrRunControlled) {
		t.Fatal("stop lost", e)
	}
	if e = restored.CommitLegacyStudyGeneration(ctx, stoppedJob.ID, stoppedInput, &provider.StudyChatResult{Answer: &provider.StudyChatMessage{Role: "assistant", Content: "迟到付费响应不得采用", ReferenceSegmentIDs: []string{"v4-a"}}}); !errors.Is(e, store.ErrRunControlled) {
		t.Fatal("stopped paid response adopted", e)
	}
	for _, lane := range []string{"knowledge", "study"} {
		c, e := restored.GetRunControl(ctx, "lane", lane)
		check(e)
		if !c.Paused || c.Reason != "恢复后仍须Owner明确继续" {
			t.Fatal(c)
		}
	}
	actualKnown, e := restored.GetJobExecution(ctx, known.ID)
	assertEqual("known response/input/config/checkpoint", frozenKnown, actualKnown, e)
	actualUnknown, e := restored.GetJobExecution(ctx, unknown.ID)
	assertEqual("unknown response is not a zero-cost completion", frozenUnknown, actualUnknown, e)
	var unknownCost sql.NullFloat64
	var stoppedInputUnits, stoppedOutputUnits int
	check(restored.DB.QueryRowContext(ctx, `SELECT estimated_cost FROM usage_records WHERE receipt_id=?`, unknown.ID+":unknown").Scan(&unknownCost))
	if unknownCost.Valid {
		t.Fatal("unknown cost was filled with zero", unknownCost)
	}
	check(restored.DB.QueryRowContext(ctx, `SELECT input_units,output_units FROM usage_records WHERE receipt_id=?`, stoppedJob.ID+":legacy_study_generate").Scan(&stoppedInputUnits, &stoppedOutputUnits))
	if stoppedInputUnits != 73 || stoppedOutputUnits != 11 {
		t.Fatal("stopped paid usage lost", stoppedInputUnits, stoppedOutputUnits)
	}
	// Serving the restored instance rotates epoch before any device grant validation.
	check(restored.BeginOfflineInstance(ctx))
	if _, e = restored.ValidateOfflineDevice(ctx, "v4-hashed-owner-session", offline.Namespace); !errors.Is(e, store.ErrOfflineRevoked) {
		t.Fatal("old device authorization revived", e)
	}
	if _, e = restored.GetOfflinePack(ctx, "v4-hashed-owner-session", offline.Namespace, savedPack); !errors.Is(e, store.ErrOfflineRevoked) {
		t.Fatal("old pack namespace revived", e)
	}
	// Restore startup requeues only interrupted jobs; known response is adopted once without provider resolution/network/paid retry.
	check(restored.ResetRunningOnStartup(ctx))

	w := queue.NewWorker(restored, nil, filepath.Join(dst, "tmp"), filepath.Join(dst, "evidence"), filepath.Join(dst, "narrations"))
	w.WithRawAudioResolver(func(context.Context, *models.ProcessingJob) (string, func(), error) {
		t.Fatal("known paid checkpoint tried to fetch source audio again")
		return "", func() {}, nil
	})
	w.WithBundleResolver(func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		t.Fatal("known checkpoint attempted another model call")
		return nil, nil
	})
	check(w.ProcessOne(ctx))
	after, e := restored.GetJob(ctx, known.ID)
	check(e)
	if after.Status != models.StatusSucceeded {
		t.Fatal("known response did not continue", after)
	}
	var inputUnits int
	var cost float64
	var receiptAttempt string
	check(restored.DB.QueryRowContext(ctx, `SELECT input_units,estimated_cost,attempt_id FROM usage_records WHERE receipt_id=?`, known.ID+":transcription").Scan(&inputUnits, &cost, &receiptAttempt))
	if inputUnits != 1000000 || cost != 10 || receiptAttempt != known.ID+":response" {
		t.Fatal("known receipt changed after resume", inputUnits, cost, receiptAttempt)
	}
	var duplicates int
	check(restored.DB.QueryRowContext(ctx, `SELECT count(*) FROM usage_records WHERE receipt_id=?`, known.ID+":transcription").Scan(&duplicates))
	if duplicates != 1 {
		t.Fatal("paid receipt duplicated", duplicates)
	}
	reservation, e := restored.GetJobBudgetReservation(ctx, known.ID)
	check(e)
	if reservation.Status != "settled" || reservation.ActualCostCents == nil || *reservation.ActualCostCents != 10 {
		t.Fatal("known budget not settled", reservation)
	}
	afterUnknown, e := restored.GetJobExecution(ctx, unknown.ID)
	assertEqual("known continuation never touched unknown attempt", frozenUnknown, afterUnknown, e)
	// The same archive in a second independent target must not follow an evidence symlink outside that instance, even when its bytes/hash match.
	unsafeDst := filepath.Join(t.TempDir(), "symlink-instance")
	_, e = Restore(ctx, archive, unsafeDst, false)
	check(e)
	unsafeStore, e := store.Open(filepath.Join(unsafeDst, dbFileName))
	check(e)
	defer unsafeStore.Close()
	check(os.Remove(filepath.Join(unsafeDst, "evidence", rel)))
	check(os.Symlink(filepath.Join(src, "evidence", rel), filepath.Join(unsafeDst, "evidence", rel)))
	check(unsafeStore.ResetRunningOnStartup(ctx))
	unsafeWorker := queue.NewWorker(unsafeStore, nil, filepath.Join(unsafeDst, "tmp"), filepath.Join(unsafeDst, "evidence"), filepath.Join(unsafeDst, "narrations"))
	rejectedLocal := 0
	unsafeWorker.WithRawAudioResolver(func(context.Context, *models.ProcessingJob) (string, func(), error) {
		rejectedLocal++
		return "", func() {}, errors.New("外部实例symlink证据拒绝，测试禁止新下载")
	})
	unsafeWorker.WithBundleResolver(func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		t.Fatal("symlink evidence reached model resolution")
		return nil, nil
	})
	check(unsafeWorker.ProcessOne(ctx))
	unsafeJob, e := unsafeStore.GetJob(ctx, known.ID)
	check(e)
	if unsafeJob.Status != models.StatusFailed || rejectedLocal != 1 {
		t.Fatal("external symlink reused", unsafeJob, rejectedLocal)
	}
	unsafeExecution, e := unsafeStore.GetJobExecution(ctx, known.ID)
	check(e)
	if unsafeExecution.ResultState == models.JobResultComplete || unsafeExecution.CheckpointJSON != frozenKnown.CheckpointJSON {
		t.Fatal("rejected local evidence lost or adopted paid response", unsafeExecution)
	}
	var unsafeVersion int
	check(unsafeStore.DB.QueryRowContext(ctx, `SELECT current_transcript_version FROM episodes WHERE id=?`, ep).Scan(&unsafeVersion))
	if unsafeVersion != version {
		t.Fatal("symlink response adopted a new transcript", unsafeVersion)
	}

}
