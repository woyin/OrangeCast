package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/backup"
	"github.com/woyin/orangecast/internal/filehash"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/queue"
	"github.com/woyin/orangecast/internal/store"
)

type restoreLearningProvider struct {
	calls      int
	onResponse func()
}

func (f *restoreLearningProvider) Name() string { return "pod" }
func (f *restoreLearningProvider) KnowledgeArticleStep(_ context.Context, req provider.KnowledgeArticleRequest) (*provider.KnowledgeArticleResult, provider.TaskUsage, error) {
	f.calls++
	result := knowledgeStepResult(req, false, false, false)
	if f.onResponse != nil {
		f.onResponse()
	}
	return result, provider.TaskUsage{InputUnits: 10, OutputUnits: 10}, nil
}

func TestPersonalLearningV3CombinedBackupRecoveryAndPurge(t *testing.T) {
	srv := newTestServer(t)
	ctx := t.Context()
	source, noteID := seedKnowledgeLearning(t, srv)
	srv.cfg.PodBaseURL, srv.cfg.PodAPIKey, srv.cfg.PodModel = "https://example.test/v1", "test", "fixture"
	profile, e := srv.store.EnsureDefaultEditorialProfile(ctx)
	if e != nil {
		t.Fatal(e)
	}
	article, _, e := srv.enqueueKnowledgeArticle(ctx, profile.ID, false)
	if e != nil {
		t.Fatal(e)
	}
	paid := &restoreLearningProvider{onResponse: func() {
		if e := srv.store.ChangeRunControl(ctx, "direction", "knowledge_article:"+article.ID, "pause", "备份前暂停，不采用迟到响应", uuid.NewString(), 1, 0); e != nil {
			t.Fatal(e)
		}
	}}
	srv.worker.WithBundleResolver(func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{KnowledgeArticle: paid}, nil
	})
	if e = srv.worker.ProcessOne(ctx); e != nil {
		t.Fatal(e)
	}
	var originalJob string
	srv.store.DB.QueryRow(`SELECT id FROM processing_jobs WHERE source_type='knowledge_article' AND source_id=?`, article.ID).Scan(&originalJob)
	original, e := srv.store.GetJobExecution(ctx, originalJob)
	if e != nil || original.CheckpointJSON == "" || paid.calls != 1 {
		t.Fatal(original, e)
	}
	batch, _, e := srv.store.ReserveLearningReview(ctx, profile.ID, "pod", "fixture", time.Now(), false)
	if e != nil {
		t.Fatal(e)
	}
	if e = srv.store.CommitLearningReview(ctx, batch.JobID, batch.ID, &provider.KnowledgeArticleResult{Questions: []provider.LearningReviewQuestion{{Question: "怎样区分理解与原文？", AnswerBasis: "自建冻结提示", MaterialIDs: []string{noteID}}}}); e != nil {
		t.Fatal(e)
	}
	srv.store.MarkJobRunning(ctx, batch.JobID)
	srv.store.MarkJobSucceeded(ctx, batch.JobID)
	session, e := srv.store.StartReviewSession(ctx, uuid.NewString(), 3, time.Now().Add(time.Minute))
	if e != nil {
		t.Fatal(e)
	}
	q, e := srv.store.CreateLearningQuestion(ctx, store.LearningQuestion{Body: "怎样保留自己的解释？", Goal: "回听核对引用"})
	if e != nil {
		t.Fatal(e)
	}
	snap, e := srv.store.FreezeSourceSnapshot(ctx, models.SourceEpisode, source)
	if e != nil {
		t.Fatal(e)
	}
	anchor, _ := json.Marshal(models.NoteAnchor{SnapshotID: snap.ID, Version: snap.ContentVersion, Position: 0.5, SegmentIDs: []string{"seg-1"}, Mode: "original"})
	voiceDir := filepath.Join(srv.cfg.DataDir, "voice-notes")
	os.MkdirAll(voiceDir, 0700)
	savedFile := uuid.NewString() + ".wav"
	savedPath := filepath.Join(voiceDir, savedFile)
	if e = writeBrowserAcceptanceWAV(savedPath, 1); e != nil {
		t.Fatal(e)
	}
	os.Chmod(savedPath, 0600)
	hash, _ := filehash.SHA256(savedPath)
	fi, _ := os.Stat(savedPath)
	draft, _, e := srv.store.CreateVoiceNoteDraft(ctx, store.VoiceNoteDraft{ID: uuid.NewString(), SourceType: models.SourceEpisode, SourceID: source, AnchorJSON: string(anchor), UploadSHA256: hash, AudioSHA256: hash, AudioFile: savedFile, DurationSeconds: 1, SizeBytes: fi.Size(), Text: "自己的解释，等待回听核对"})
	if e != nil {
		t.Fatal(e)
	}
	savedNote, e := srv.store.SaveVoiceNoteDraft(ctx, draft.ID, 1, q.ID, q.Revision, true)
	if e != nil {
		t.Fatal(e)
	}
	// Unsaved recordings are intentionally excluded, while draft text remains recoverable.
	temporaryFile := uuid.NewString() + ".wav"
	temporaryPath := filepath.Join(voiceDir, temporaryFile)
	raw, _ := os.ReadFile(savedPath)
	os.WriteFile(temporaryPath, raw, 0600)
	tempDraft, _, e := srv.store.CreateVoiceNoteDraft(ctx, store.VoiceNoteDraft{ID: uuid.NewString(), SourceType: models.SourceEpisode, SourceID: source, AnchorJSON: string(anchor), UploadSHA256: hash, AudioSHA256: hash, AudioFile: temporaryFile, DurationSeconds: 1, SizeBytes: fi.Size(), Text: "未保存私人草稿"})
	if e != nil {
		t.Fatal(e)
	}
	os.MkdirAll(srv.cfg.EvidenceDir, 0755)
	archive := filepath.Join(t.TempDir(), "learning-v3.tar.gz")
	manifest, e := backup.Create(ctx, srv.store, srv.cfg.EvidenceDir, archive, voiceDir)
	if e != nil || len(manifest.Voice) != 1 {
		t.Fatal(manifest, e)
	}
	restoredDir := filepath.Join(t.TempDir(), "restored")
	if _, e = backup.Restore(ctx, archive, restoredDir, false); e != nil {
		t.Fatal(e)
	}
	restored, e := store.Open(filepath.Join(restoredDir, "cloudwisepod.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer restored.Close()
	if e = restored.ResetRunningOnStartup(ctx); e != nil {
		t.Fatal(e)
	}
	control, e := restored.GetRunControl(ctx, "direction", "knowledge_article:"+article.ID)
	if e != nil || !control.Paused {
		t.Fatal(control, e)
	}
	currentSession, e := restored.ActiveReviewSession(ctx)
	if e != nil || currentSession.ID != session.ID {
		t.Fatal(currentSession, e)
	}
	restoredNote, e := restored.GetOwnerNote(ctx, savedNote.ID)
	if e != nil || restoredNote.Content != savedNote.Content || restoredNote.AnchorJSON != savedNote.AnchorJSON {
		t.Fatal(restoredNote, e)
	}
	relations, e := restored.ListLearningQuestionRelations(ctx, q.ID)
	if e != nil || len(relations) != 1 {
		t.Fatal(relations, e)
	}
	gotHash, e := filehash.SHA256(filepath.Join(restoredDir, "voice-notes", savedFile))
	if e != nil || gotHash != hash {
		t.Fatal(gotHash, e)
	}
	if _, e = os.Stat(filepath.Join(restoredDir, "voice-notes", temporaryFile)); !os.IsNotExist(e) {
		t.Fatal("temporary recording entered backup", e)
	}
	if d, e := restored.GetVoiceNoteDraft(ctx, tempDraft.ID); e != nil || d.Text != "未保存私人草稿" {
		t.Fatal(d, e)
	}
	if e = restored.ChangeRunControl(ctx, "direction", "knowledge_article:"+article.ID, "resume", "核对后复用原付费响应", uuid.NewString(), 2, 0); e != nil {
		t.Fatal(e)
	}
	if e = restored.RetryKnowledgeArticle(ctx, article.ID); e != nil {
		t.Fatal(e)
	}
	recovered := &restoreLearningProvider{}
	worker := queue.NewWorker(restored, provider.NewSelector("", ""), filepath.Join(restoredDir, "tmp"), filepath.Join(restoredDir, "evidence"), filepath.Join(restoredDir, "narrations")).WithBundleResolver(func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{KnowledgeArticle: recovered}, nil
	})
	if e = worker.ProcessOne(ctx); e != nil {
		t.Fatal(e)
	}
	if recovered.calls != 0 {
		t.Fatal("restored known response paid again")
	}
	var receipts int
	restored.DB.QueryRow(`SELECT count(*) FROM usage_records WHERE receipt_id=?`, originalJob+":knowledge_article_discover").Scan(&receipts)
	if receipts != 1 {
		t.Fatal(receipts)
	}
	if e = restored.DeleteSourceRows(ctx, models.SourceEpisode, source); e != nil {
		t.Fatal(e)
	}
	restored.DB.QueryRow(`SELECT count(*) FROM usage_records WHERE receipt_id=?`, originalJob+":knowledge_article_discover").Scan(&receipts)
	if receipts != 1 {
		t.Fatal("purge erased paid facts")
	}
	items, e := restored.ListReviewSessionItems(ctx, session.ID)
	if e != nil || len(items) != 0 {
		t.Fatal("purge left review evidence", items, e)
	}
	var cleanup int
	restored.DB.QueryRow(`SELECT count(*) FROM voice_audio_cleanup WHERE file=?`, savedFile).Scan(&cleanup)
	if cleanup != 1 {
		t.Fatal("purge lost private file cleanup", cleanup)
	}
}
