package backup

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	jobqueue "github.com/woyin/orangecast/internal/queue"
	"github.com/woyin/orangecast/internal/store"
)

// A real v2 archive exercises the database/audio boundary, not copied structs.
func TestLearningExcerptsV2RestorePreservesFrozenLearningAndPaidFacts(t *testing.T) {
	ctx := t.Context()
	src := t.TempDir()
	evidenceDir := filepath.Join(src, "evidence")
	if err := os.MkdirAll(evidenceDir, 0700); err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(filepath.Join(src, dbFileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	podcast, err := s.CreatePodcast(ctx, "https://backup-excerpts.test/feed", "恢复组合", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.MergeEpisodes(ctx, podcast.ID, []models.Episode{{GUID: "backup-excerpts", Title: "真实原音区间", AudioURL: "https://backup-excerpts.test/audio.wav"}}); err != nil {
		t.Fatal(err)
	}
	episodes, err := s.ListEpisodes(ctx, podcast.ID)
	if err != nil || len(episodes) != 1 {
		t.Fatal(episodes, err)
	}
	ep := episodes[0].ID
	frozen := `{"source_id":"` + ep + `","model":"frozen-transcriber","price_version":"fixed-price-v1","segments_policy":"real-audio"}`
	job, created, err := s.EnqueueJobIdempotent(ctx, store.JobIntentSpec{SourceType: models.SourceEpisode, SourceID: ep, JobType: models.JobTranscribe, IntentID: "backup-learning-paid", InputSnapshotJSON: frozen, ConfigVersion: "frozen-config", ConfiguredProvider: "fixture-provider", ConfiguredModel: "frozen-transcriber"})
	if err != nil || !created {
		t.Fatal(job, created, err)
	}
	if _, err = s.MarkJobRunning(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.MarkJobRemoteCallStarted(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	payload := `{"language":"zh","text":"真实区间","segments":[{"id":"a","start":5,"end":10,"text":"第一区间"},{"id":"b","start":10,"end":15,"text":"第二段"},{"id":"c","start":20,"end":25,"text":"第二个区间"}]}`
	version, err := s.CreateArtifactVersion(ctx, models.SourceEpisode, ep, store.KindTranscript, "fixture-provider", "frozen-transcriber", "v1", job.ID, payload)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetCurrentVersion(ctx, models.SourceEpisode, ep, store.KindTranscript, version); err != nil {
		t.Fatal(err)
	}
	if err = s.RecordUsageReceipt(ctx, models.UsageReceipt{ReceiptID: job.ID + ":remote", AttemptID: job.ID, Operation: "transcribe", Provider: "fixture-provider", Model: "frozen-transcriber", InputUnits: 30, OutputUnits: 3, CostKnown: true, CostCents: 37}); err != nil {
		t.Fatal(err)
	}
	if err = s.SaveJobResult(ctx, job.ID, `{"artifact_version":1}`, models.JobResultComplete); err != nil {
		t.Fatal(err)
	}
	if err = s.MarkJobSucceeded(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	// Thirty seconds of PCM: transcript coordinates are inside physical audio.
	var wav bytes.Buffer
	wav.WriteString("RIFF")
	binary.Write(&wav, binary.LittleEndian, uint32(36+30*16000))
	wav.WriteString("WAVEfmt ")
	binary.Write(&wav, binary.LittleEndian, uint32(16))
	binary.Write(&wav, binary.LittleEndian, uint16(1))
	binary.Write(&wav, binary.LittleEndian, uint16(1))
	binary.Write(&wav, binary.LittleEndian, uint32(8000))
	binary.Write(&wav, binary.LittleEndian, uint32(16000))
	binary.Write(&wav, binary.LittleEndian, uint16(2))
	binary.Write(&wav, binary.LittleEndian, uint16(16))
	wav.WriteString("data")
	binary.Write(&wav, binary.LittleEndian, uint32(30*16000))
	wav.Write(make([]byte, 30*16000))
	audio := wav.Bytes()
	sum := sha256.Sum256(audio)
	hash := hex.EncodeToString(sum[:])
	rel := "frozen-original.wav"
	if err = os.WriteFile(filepath.Join(evidenceDir, rel), audio, 0600); err != nil {
		t.Fatal(err)
	}
	if err = s.UpsertEvidenceAudio(ctx, models.SourceEpisode, ep, rel, "wav", int64(len(audio)), hash); err != nil {
		t.Fatal(err)
	}
	snap, err := s.FreezeSourceSnapshot(ctx, models.SourceEpisode, ep)
	if err != nil {
		t.Fatal(err)
	}
	one, err := s.CreateLearningExcerpt(ctx, snap.ID, []string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	two, err := s.CreateLearningExcerpt(ctx, snap.ID, []string{"c"})
	if err != nil {
		t.Fatal(err)
	}
	dj, err := s.CreateDJPlan(ctx, &models.DJPlan{SourceType: models.SourceEpisode, SourceID: ep, HighlightVersion: 1, InputSnapshotJSON: `{"source_snapshot_id":"` + snap.ID + `"}`, Items: []models.DJPlanItem{{Kind: models.DJItemEvidence, Start: 5, End: 10, SegmentIDs: []string{"a"}}}})
	if err != nil {
		t.Fatal(err)
	}
	queue, err := s.GetListeningQueue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range []store.ListeningQueueChange{{Action: "add", SourceType: models.SourceEpisode, SourceID: ep, Mode: "excerpt", ExcerptID: one.ID}, {Action: "add", SourceType: models.SourceEpisode, SourceID: ep, Mode: "original"}, {Action: "add", SourceType: models.SourceEpisode, SourceID: ep, Mode: "dj", PlanID: dj.ID, PlanVersion: dj.Version}, {Action: "add", SourceType: models.SourceEpisode, SourceID: ep, Mode: "excerpt", ExcerptID: two.ID}} {
		queue, err = s.ChangeListeningQueue(ctx, queue.Revision, change)
		if err != nil {
			t.Fatal(err)
		}
	}
	order := []string{queue.Items[3].ID, queue.Items[1].ID, queue.Items[0].ID, queue.Items[2].ID}
	queue, err = s.ChangeListeningQueue(ctx, queue.Revision, store.ListeningQueueChange{Action: "reorder", Order: order})
	if err != nil {
		t.Fatal(err)
	}
	queue, err = s.ChangeListeningQueue(ctx, queue.Revision, store.ListeningQueueChange{Action: "play", ItemID: order[2]})
	if err != nil {
		t.Fatal(err)
	}
	for _, progress := range []*models.ListeningProgress{{SourceType: models.SourceEpisode, SourceID: ep, Mode: "original", AudioSHA256: hash, ItemOffsetSeconds: 17, Speed: 1}, {SourceType: models.SourceEpisode, SourceID: ep, Mode: "dj", AudioSHA256: hash, PlanID: dj.ID, PlanVersion: dj.Version, ItemPosition: 1, ItemOffsetSeconds: 7, Speed: 1.25}, {SourceType: models.SourceEpisode, SourceID: ep, Mode: "excerpt", AudioSHA256: hash, ExcerptID: one.ID, SnapshotID: snap.ID, ItemOffsetSeconds: 12, Speed: 1.5}, {SourceType: models.SourceEpisode, SourceID: ep, Mode: "excerpt", AudioSHA256: hash, ExcerptID: two.ID, SnapshotID: snap.ID, ItemOffsetSeconds: 23, Speed: 0.75}} {
		if _, err = s.SaveListeningProgressCAS(ctx, progress, 0); err != nil {
			t.Fatal(err)
		}
	}
	question, err := s.CreateLearningQuestion(ctx, store.LearningQuestion{Body: "区间中的条件是否充分？", Goal: "找到反例"})
	if err != nil {
		t.Fatal(err)
	}
	question, err = s.ChangeLearningQuestion(ctx, question.ID, question.Revision, store.LearningQuestionChange{Action: "link", Link: provider.LearningQuestionLink{Kind: "evidence", ObjectID: snap.ID}})
	if err != nil {
		t.Fatal(err)
	}
	gap, err := s.CreateEvidenceGap(ctx, "question", question.ID, question.Revision, "counterexample", "owner", "还需要反例", "仅两个冻结区间")
	if err != nil {
		t.Fatal(err)
	}
	gap, err = s.ChangeEvidenceGap(ctx, "question", question.ID, gap.ID, question.Revision, gap.Revision, "helpful", "第一区间有帮助")
	if err != nil {
		t.Fatal(err)
	}
	gap, err = s.ChangeEvidenceGap(ctx, "question", question.ID, gap.ID, question.Revision, gap.Revision, "insufficient", "第二次仍不足")
	if err != nil {
		t.Fatal(err)
	}
	history, err := s.ListEvidenceGapOperations(ctx, gap.ID)
	if err != nil || len(history) != 2 {
		t.Fatal(history, err)
	}
	execution, err := s.GetJobExecution(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "learning-v2.tar.gz")
	manifest, err := Create(ctx, s, evidenceDir, archive)
	if err != nil || manifest.Version != 2 {
		t.Fatal(manifest, err)
	}
	dst := filepath.Join(t.TempDir(), "restored")
	restoredManifest, err := Restore(ctx, archive, dst, false)
	if err != nil || restoredManifest.Version != 2 {
		t.Fatal(restoredManifest, err)
	}
	restored, err := store.Open(filepath.Join(dst, dbFileName))
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	restoredAudio, err := os.ReadFile(filepath.Join(dst, "evidence", rel))
	if err != nil || !bytes.Equal(audio, restoredAudio) {
		t.Fatal("original bytes not restored", err)
	}
	actualQueue, err := restored.GetListeningQueue(ctx)
	if err != nil || !reflect.DeepEqual(queue, actualQueue) {
		t.Fatal("queue identity/order/selection changed", actualQueue, err)
	}
	for _, entry := range actualQueue.Items {
		if !entry.Available {
			t.Fatal("frozen entry unavailable", entry)
		}
		identity, err := restored.CheckListeningIdentity(ctx, entry)
		if err != nil || !identity.Available {
			t.Fatal(identity, err)
		}
	}
	for _, e := range []*models.LearningExcerpt{one, two} {
		actual, err := restored.GetLearningExcerpt(ctx, e.ID)
		if err != nil || !reflect.DeepEqual(e, actual) {
			t.Fatal(actual, err)
		}
		if _, err = restored.CheckLearningExcerpt(ctx, actual); err != nil {
			t.Fatal(err)
		}
	}
	for _, check := range []struct {
		mode, id        string
		position, speed float64
	}{{"original", "", 17, 1}, {"dj", "", 7, 1.25}, {"excerpt", one.ID, 12, 1.5}, {"excerpt", two.ID, 23, 0.75}} {
		var p *models.ListeningProgress
		if check.mode == "excerpt" {
			p, err = restored.GetLearningExcerptProgress(ctx, models.SourceEpisode, ep, check.id)
		} else {
			p, err = restored.GetListeningProgressMode(ctx, models.SourceEpisode, ep, check.mode)
		}
		if err != nil || p.ItemOffsetSeconds != check.position || p.Speed != check.speed || p.Revision != 1 || p.Mode != check.mode {
			t.Fatal("progress identity mixed", p, err)
		}
	}
	actualGaps, err := restored.ListEvidenceGaps(ctx, "question", question.ID)
	if err != nil || len(actualGaps) != 1 || !reflect.DeepEqual(gap, actualGaps[0]) {
		t.Fatal(actualGaps, err)
	}
	actualHistory, err := restored.ListEvidenceGapOperations(ctx, gap.ID)
	if err != nil || !reflect.DeepEqual(history, actualHistory) {
		t.Fatal(actualHistory, err)
	}
	actualExecution, err := restored.GetJobExecution(ctx, job.ID)
	if err != nil || !reflect.DeepEqual(execution, actualExecution) {
		t.Fatal("frozen paid execution changed", actualExecution, err)
	}
	assertPaid := func() {
		t.Helper()
		var count, input, output, cost int
		if err := restored.DB.QueryRowContext(ctx, `SELECT count(*),sum(input_units),sum(output_units),sum(estimated_cost) FROM usage_records WHERE receipt_id=? AND attempt_id=? AND provider='fixture-provider' AND model='frozen-transcriber'`, job.ID+":remote", job.ID).Scan(&count, &input, &output, &cost); err != nil || count != 1 || input != 30 || output != 3 || cost != 37 {
			t.Fatal("paid ledger changed", count, input, output, cost, err)
		}
	}
	assertNoWork := func() {
		t.Helper()
		var jobs, artifacts, plans int
		for query, target := range map[string]*int{`SELECT count(*) FROM processing_jobs`: &jobs, `SELECT count(*) FROM artifact_versions`: &artifacts, `SELECT count(*) FROM dj_plans`: &plans} {
			if err := restored.DB.QueryRowContext(ctx, query).Scan(target); err != nil {
				t.Fatal(err)
			}
		}
		if jobs != 1 || artifacts != 1 || plans != 1 {
			t.Fatal("restore/read manufactured work", jobs, artifacts, plans)
		}
	}
	assertPaid()
	assertNoWork()
	replacedAudio := append([]byte(nil), audio...)
	replacedAudio[len(replacedAudio)-1] = 1
	if err = os.WriteFile(filepath.Join(dst, "evidence", rel), replacedAudio, 0600); err != nil {
		t.Fatal(err)
	}
	replacedHash := sha256.Sum256(replacedAudio)
	if err = restored.UpsertEvidenceAudio(ctx, models.SourceEpisode, ep, rel, "wav", int64(len(replacedAudio)), hex.EncodeToString(replacedHash[:])); err != nil {
		t.Fatal(err)
	}
	for _, e := range []*models.LearningExcerpt{one, two} {
		if _, err = restored.CheckLearningExcerpt(ctx, e); !errors.Is(err, store.ErrConflict) {
			t.Fatal("replacement accepted", err)
		}
	}
	assertPaid()
	assertNoWork()
	if err = os.WriteFile(filepath.Join(dst, "evidence", rel), audio, 0600); err != nil {
		t.Fatal(err)
	}
	if err = restored.UpsertEvidenceAudio(ctx, models.SourceEpisode, ep, rel, "wav", int64(len(audio)), hash); err != nil {
		t.Fatal(err)
	}
	purger := jobqueue.NewWorker(restored, nil, filepath.Join(dst, "tmp"), filepath.Join(dst, "evidence"), filepath.Join(dst, "narrations"))
	if err = purger.PurgeSource(ctx, models.SourceEpisode, ep); err != nil {
		t.Fatal(err)
	}
	for _, e := range []*models.LearningExcerpt{one, two} {
		if _, err = restored.CheckLearningExcerpt(ctx, e); !errors.Is(err, store.ErrSnapshotInvalidated) {
			t.Fatal("purged snapshot playable", err)
		}
		if _, err = restored.GetLearningExcerptProgress(ctx, models.SourceEpisode, ep, e.ID); !errors.Is(err, store.ErrNotFound) {
			t.Fatal("purged progress retained", err)
		}
	}
	if _, err = os.Stat(filepath.Join(dst, "evidence", rel)); !os.IsNotExist(err) {
		t.Error("purge kept physical evidence", err)
	}
	purgedQueue, err := restored.GetListeningQueue(ctx)
	if err != nil || purgedQueue.CurrentItemID != "" {
		t.Fatal(purgedQueue, err)
	}
	for _, entry := range purgedQueue.Items {
		if entry.Available {
			t.Fatal("purged source playable", entry)
		}
	}
	assertPaid()
	purgedGaps, err := restored.ListEvidenceGaps(ctx, "question", question.ID)
	if err != nil || len(purgedGaps) != 0 {
		t.Fatal("source-dependent gap survived purge", purgedGaps, err)
	}
	purgedHistory, err := restored.ListEvidenceGapOperations(ctx, gap.ID)
	if err != nil || len(purgedHistory) != 0 {
		t.Fatal("source-dependent gap body/history survived purge", purgedHistory, err)
	}
	// JSON snapshot bytes were preserved before purge, rather than rebuilt using defaults.
	if !json.Valid([]byte(execution.InputSnapshotJSON)) || execution.InputSnapshotJSON != frozen {
		t.Fatal(execution)
	}
}
