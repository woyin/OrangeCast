package store

import (
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/models"
	"testing"
	"time"
)

func offlineTestSource(t *testing.T, s *Store) (string, string) {
	t.Helper()
	id := seedSnapshotEpisode(t, s)
	seedSnapshotTranscript(t, s, models.SourceEpisode, id, "原文")
	snap, err := s.FreezeSourceSnapshot(t.Context(), models.SourceEpisode, id)
	if err != nil {
		t.Fatal(err)
	}
	return id, snap.ID
}
func TestOfflineDeviceAuthorization(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	session := "hashed-owner-session-one"
	if _, err := s.GetOfflineDevice(ctx, session, "11111111-1111-4111-8111-111111111111"); !errors.Is(err, ErrOfflineRevoked) {
		t.Fatal(err)
	}
	d, err := s.EnableOfflineDevice(ctx, session, "11111111-1111-4111-8111-111111111111")
	if err != nil {
		t.Fatal(err)
	}
	if d.ExpiresAt-d.IssuedAt != 86400 {
		t.Fatal("ttl")
	}
	if _, err = s.ValidateOfflineDevice(ctx, "other-session", d.Namespace); !errors.Is(err, ErrOfflineRevoked) {
		t.Fatal(err)
	}
	if err = s.BeginOfflineInstance(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ValidateOfflineDevice(ctx, session, d.Namespace); !errors.Is(err, ErrOfflineRevoked) {
		t.Fatal(err)
	}
	d, err = s.EnableOfflineDevice(ctx, session, "11111111-1111-4111-8111-111111111111")
	if err != nil {
		t.Fatal(err)
	}
	s.DB.Exec(`UPDATE offline_devices SET expires_at=0 WHERE namespace=?`, d.Namespace)
	if _, err = s.ValidateOfflineDevice(ctx, session, d.Namespace); !errors.Is(err, ErrOfflineExpired) {
		t.Fatal(err)
	}
}
func TestOfflineOperationAtomicReplayAndConflict(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	id, snap := offlineTestSource(t, s)
	session := "hashed-owner-session-one"
	d, err := s.EnableOfflineDevice(ctx, session, "11111111-1111-4111-8111-111111111111")
	if err != nil {
		t.Fatal(err)
	}
	offlineTestPack(t, s, session, d.Namespace, id, snap)
	note := models.OwnerNote{Kind: "source_note", Content: "笔记", CitationsJSON: `["seg-0001"]`}
	raw, _ := json.Marshal(note)
	op := OfflineOperation{SchemaVersion: 1, Namespace: d.Namespace, EditedAt: time.Now().Unix(), UUID: uuid.NewString(), Kind: "owner_note", SourceType: models.SourceEpisode, SourceID: id, SnapshotID: snap, Payload: raw}
	op.PayloadHash = OfflineOperationHash(op)
	receipt, err := s.ApplyOfflineOperation(ctx, session, d.Namespace, op)
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.ApplyOfflineOperation(ctx, session, d.Namespace, op)
	if err != nil || *again != *receipt {
		t.Fatalf("replay %+v %v", again, err)
	}
	notes, err := s.ListOwnerNotes(ctx, models.SourceEpisode, id)
	if err != nil || len(notes) != 1 {
		t.Fatalf("notes %d %v", len(notes), err)
	}
	op.Payload = json.RawMessage(`{}`)
	op.PayloadHash = OfflineOperationHash(op)
	if _, err = s.ApplyOfflineOperation(ctx, session, d.Namespace, op); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	note.ID = receipt.ObjectID
	note.Content = "更新笔记"
	raw, _ = json.Marshal(note)
	op.UUID = uuid.NewString()
	op.ExpectedRevision = 1
	op.Payload = raw
	op.PayloadHash = OfflineOperationHash(op)
	updated, err := s.ApplyOfflineOperation(ctx, session, d.Namespace, op)
	if err != nil || updated.Revision != 2 {
		t.Fatalf("update %+v %v", updated, err)
	}
	op.UUID = uuid.NewString()
	op.PayloadHash = OfflineOperationHash(op)
	if _, err = s.ApplyOfflineOperation(ctx, session, d.Namespace, op); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
}
func TestOfflineOperationReceiptFailureRollsBack(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	id, snap := offlineTestSource(t, s)
	session := "hashed-owner-session-one"
	d, _ := s.EnableOfflineDevice(ctx, session, "11111111-1111-4111-8111-111111111111")
	offlineTestPack(t, s, session, d.Namespace, id, snap)
	s.DB.Exec(`CREATE TRIGGER fail_offline_receipt BEFORE INSERT ON offline_operation_receipts BEGIN SELECT RAISE(ABORT,'receipt failed');END`)
	raw, _ := json.Marshal(models.OwnerNote{Kind: "source_note", Content: "不可部分保存", CitationsJSON: `["seg-0001"]`})
	op := OfflineOperation{SchemaVersion: 1, Namespace: d.Namespace, EditedAt: time.Now().Unix(), UUID: uuid.NewString(), Kind: "owner_note", SourceType: models.SourceEpisode, SourceID: id, SnapshotID: snap, Payload: raw}
	op.PayloadHash = OfflineOperationHash(op)
	if _, err := s.ApplyOfflineOperation(ctx, session, d.Namespace, op); err == nil {
		t.Fatal("expected receipt failure")
	}
	notes, _ := s.ListOwnerNotes(ctx, models.SourceEpisode, id)
	if len(notes) != 0 {
		t.Fatal("partial mutation")
	}
}
func TestOfflineOperationRejectsChangedSource(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	id, snap := offlineTestSource(t, s)
	session := "hashed-owner-session-one"
	d, _ := s.EnableOfflineDevice(ctx, session, "11111111-1111-4111-8111-111111111111")
	offlineTestPack(t, s, session, d.Namespace, id, snap)
	seedSnapshotTranscript(t, s, models.SourceEpisode, id, "新原文")
	op := OfflineOperation{SchemaVersion: 1, Namespace: d.Namespace, EditedAt: time.Now().Unix(), UUID: uuid.NewString(), Kind: "organizing_draft", SourceType: models.SourceEpisode, SourceID: id, SnapshotID: snap, Payload: json.RawMessage(`{"content":"本地文字应保留"}`)}
	op.PayloadHash = OfflineOperationHash(op)
	if _, err := s.ApplyOfflineOperation(ctx, session, d.Namespace, op); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
}

func offlineTestPack(t *testing.T, s *Store, session, ns, id, snap string) {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{"pack_id": uuid.NewString(), "files": []any{}})
	_, err := s.SaveOfflinePack(t.Context(), session, ns, string(raw), []OfflineSource{{SourceType: models.SourceEpisode, SourceID: id, SnapshotID: snap}})
	if err != nil {
		t.Fatal(err)
	}
}
func TestOfflinePackAggregateQuotaAndEmpty(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	session := "hashed-owner-session-one"
	d, err := s.EnableOfflineDevice(ctx, session, "11111111-1111-4111-8111-111111111111")
	if err != nil {
		t.Fatal(err)
	}
	save := func(size int64) (string, error) {
		raw, _ := json.Marshal(map[string]any{"pack_id": uuid.NewString(), "files": []any{map[string]any{"size": size}}})
		return s.SaveOfflinePack(ctx, session, d.Namespace, string(raw), nil)
	}
	first, err := save(300 << 20)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = save(201 << 20); !errors.Is(err, ErrInvalidEditorialState) {
		t.Fatal(err)
	}
	if _, err = s.GetOfflinePack(ctx, session, d.Namespace, first); err != nil {
		t.Fatal(err)
	}
	if _, err = save(0); err != nil {
		t.Fatal(err)
	}
	if err = s.RevokeOfflineDevice(ctx, session, d.Namespace); err != nil {
		t.Fatal(err)
	}
	if _, err = s.GetOfflinePack(ctx, session, d.Namespace, first); !errors.Is(err, ErrOfflineRevoked) {
		t.Fatal(err)
	}
}
func TestOfflineOrganizingDraftCASAndScope(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	id, snap := offlineTestSource(t, s)
	session := "hashed-owner-session-one"
	d, _ := s.EnableOfflineDevice(ctx, session, "11111111-1111-4111-8111-111111111111")
	op := OfflineOperation{SchemaVersion: 1, Namespace: d.Namespace, EditedAt: time.Now().Unix(), UUID: uuid.NewString(), Kind: "organizing_draft", SourceType: models.SourceEpisode, SourceID: id, SnapshotID: snap, Payload: json.RawMessage(`{"content":"整理文字"}`)}
	op.PayloadHash = OfflineOperationHash(op)
	if _, err := s.ApplyOfflineOperation(ctx, session, d.Namespace, op); !errors.Is(err, ErrOfflineRevoked) {
		t.Fatalf("not downloaded %v", err)
	}
	offlineTestPack(t, s, session, d.Namespace, id, snap)
	r, err := s.ApplyOfflineOperation(ctx, session, d.Namespace, op)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"id": r.ObjectID, "content": "更改"})
	op.Payload = raw
	op.ExpectedRevision = 1
	op.UUID = uuid.NewString()
	op.PayloadHash = OfflineOperationHash(op)
	r, err = s.ApplyOfflineOperation(ctx, session, d.Namespace, op)
	if err != nil || r.Revision != 2 {
		t.Fatalf("CAS %+v %v", r, err)
	}
	op.UUID = uuid.NewString()
	op.PayloadHash = OfflineOperationHash(op)
	if _, err = s.ApplyOfflineOperation(ctx, session, d.Namespace, op); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	var content string
	if err = s.DB.QueryRow(`SELECT content FROM offline_organizing_drafts WHERE id=?`, r.ObjectID).Scan(&content); err != nil || content != "更改" {
		t.Fatal(content, err)
	}
	list, err := s.ListOfflineOrganizingDrafts(ctx)
	if err != nil || len(list) != 1 || !list[0].SourceAvailable {
		t.Fatalf("list %+v %v", list, err)
	}
	if _, err = s.DB.Exec(`UPDATE source_snapshots SET status='purged' WHERE id=?`, snap); err != nil {
		t.Fatal(err)
	}
	kept, err := s.GetOfflineOrganizingDraft(ctx, r.ObjectID)
	if err != nil || kept.SourceAvailable || kept.Content != "更改" {
		t.Fatalf("purged owner text %+v %v", kept, err)
	}
}

func TestOfflineOriginalProgressCASAudioAndReplay(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	id := seedSnapshotEpisode(t, s)
	seedSnapshotTranscript(t, s, models.SourceEpisode, id, "原文")
	if err := s.UpsertEvidenceAudio(ctx, models.SourceEpisode, id, "evidence/original.mp3", "mp3", 100, "sha-original"); err != nil {
		t.Fatal(err)
	}
	snap, err := s.FreezeSourceSnapshot(ctx, models.SourceEpisode, id)
	if err != nil {
		t.Fatal(err)
	}
	session := "hashed-owner-session-one"
	d, _ := s.EnableOfflineDevice(ctx, session, "11111111-1111-4111-8111-111111111111")
	manifest, _ := json.Marshal(map[string]any{"pack_id": uuid.NewString(), "files": []any{}})
	if _, err = s.SaveOfflinePack(ctx, session, d.Namespace, string(manifest), []OfflineSource{{SourceType: models.SourceEpisode, SourceID: id, SnapshotID: snap.ID, AudioSHA256: "sha-original"}}); err != nil {
		t.Fatal(err)
	}
	p := models.ListeningProgress{Mode: "original", AudioSHA256: "sha-original", ItemOffsetSeconds: 3, Speed: 1, Seq: 1}
	raw, _ := json.Marshal(p)
	op := OfflineOperation{SchemaVersion: 1, Namespace: d.Namespace, EditedAt: time.Now().Unix(), UUID: uuid.NewString(), Kind: "original_progress", SourceType: models.SourceEpisode, SourceID: id, SnapshotID: snap.ID, Payload: raw}
	op.PayloadHash = OfflineOperationHash(op)
	r, err := s.ApplyOfflineOperation(ctx, session, d.Namespace, op)
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.ApplyOfflineOperation(ctx, session, d.Namespace, op)
	if err != nil || *r != *again {
		t.Fatal(again, err)
	}
	p.ItemOffsetSeconds = 7
	p.Seq = 2
	op.Payload, _ = json.Marshal(p)
	op.UUID = uuid.NewString()
	op.ExpectedRevision = 1
	op.PayloadHash = OfflineOperationHash(op)
	if _, err = s.ApplyOfflineOperation(ctx, session, d.Namespace, op); err != nil {
		t.Fatal(err)
	}
	op.UUID = uuid.NewString()
	op.PayloadHash = OfflineOperationHash(op)
	if _, err = s.ApplyOfflineOperation(ctx, session, d.Namespace, op); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	stored, err := s.GetListeningProgressMode(ctx, models.SourceEpisode, id, "original")
	if err != nil || stored.Revision != 2 || stored.ItemOffsetSeconds != 7 {
		t.Fatal(stored, err)
	}
	s.UpsertEvidenceAudio(ctx, models.SourceEpisode, id, "evidence/new.mp3", "mp3", 100, "sha-new")
	op.UUID = uuid.NewString()
	op.ExpectedRevision = 2
	op.PayloadHash = OfflineOperationHash(op)
	if _, err = s.ApplyOfflineOperation(ctx, session, d.Namespace, op); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	if _, err = s.GetOfflinePack(ctx, session, d.Namespace, stringsFromJSONPackID(t, manifest)); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
}
func stringsFromJSONPackID(t *testing.T, raw []byte) string {
	t.Helper()
	var m struct {
		ID string `json:"pack_id"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m.ID
}
func TestOfflinePackOnlineValidationAndExplicitClear(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	id, snap := offlineTestSource(t, s)
	session := "hashed-owner-session-one"
	d, _ := s.EnableOfflineDevice(ctx, session, "11111111-1111-4111-8111-111111111111")
	offlineTestPack(t, s, session, d.Namespace, id, snap)
	if err := s.ValidateOfflinePackSources(ctx, session, d.Namespace); err != nil {
		t.Fatal(err)
	}
	seedSnapshotTranscript(t, s, models.SourceEpisode, id, "新版本")
	if err := s.ValidateOfflinePackSources(ctx, session, d.Namespace); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	if err := s.DeleteOfflinePacks(ctx, session, d.Namespace); err != nil {
		t.Fatal(err)
	}
	if err := s.ValidateOfflinePackSources(ctx, session, d.Namespace); err != nil {
		t.Fatal(err)
	}
}
func TestOfflineArticleOnlyPackRejectsAuthoritativeEvidenceChange(t *testing.T) {
	s, article, _, noteID := readyKnowledgeRevisionFixture(t)
	ctx := t.Context()
	session := "hashed-owner-session-one"
	d, err := s.EnableOfflineDevice(ctx, session, "11111111-1111-4111-8111-111111111111")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"pack_id": uuid.NewString(), "files": []any{map[string]any{"kind": "article", "object_id": article.ID, "revision": article.PassedRevision, "size": 100}}})
	pack, err := s.SaveOfflinePack(ctx, session, d.Namespace, string(raw), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ValidateOfflinePackSources(ctx, session, d.Namespace); err != nil {
		t.Fatal(err)
	}
	note, err := s.GetOwnerNote(ctx, noteID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.UpdateOwnerNote(ctx, noteID, "新的理解撤回旧材料", note.CitationsJSON, note.ReferencesJSON, note.Revision); err != nil {
		t.Fatal(err)
	}
	if err = s.ValidateOfflinePackSources(ctx, session, d.Namespace); !errors.Is(err, ErrConflict) {
		t.Fatalf("article-only stale evidence status %v", err)
	}
	if _, err = s.GetOfflinePack(ctx, session, d.Namespace, pack); !errors.Is(err, ErrConflict) {
		t.Fatalf("article-only stale pack read %v", err)
	}
	if err = s.DeleteOfflinePacks(ctx, session, d.Namespace); err != nil {
		t.Fatal(err)
	}
	if err = s.ValidateOfflinePackSources(ctx, session, d.Namespace); err != nil {
		t.Fatal(err)
	}
}
func TestOfflineArticleOnlyPackRejectsPolicyAndReviewRevocation(t *testing.T) {
	for _, change := range []string{"policy", "review"} {
		t.Run(change, func(t *testing.T) {
			s, article, req, _ := readyKnowledgeRevisionFixture(t)
			ctx := t.Context()
			session := "hashed-owner-session-one"
			d, err := s.EnableOfflineDevice(ctx, session, "11111111-1111-4111-8111-111111111111")
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(map[string]any{"pack_id": uuid.NewString(), "files": []any{map[string]any{"kind": "article", "object_id": article.ID, "revision": article.PassedRevision, "size": 100}}})
			if _, err = s.SaveOfflinePack(ctx, session, d.Namespace, string(raw), nil); err != nil {
				t.Fatal(err)
			}
			if err = s.ValidateOfflinePackSources(ctx, session, d.Namespace); err != nil {
				t.Fatal(err)
			}
			if change == "policy" {
				_, err = s.DB.Exec(`UPDATE episodes SET model_data_policy='local_only' WHERE id=?`, req.Materials[0].SourceID)
			} else {
				_, err = s.DB.Exec(`UPDATE knowledge_article_reviews SET passed=0 WHERE article_id=? AND revision=?`, article.ID, article.PassedRevision)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err = s.ValidateOfflinePackSources(ctx, session, d.Namespace); !errors.Is(err, ErrConflict) {
				t.Fatal(err)
			}
		})
	}
}
func TestOfflineExcerptProgressFrozenCASReplayAndIndependence(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	id, snap := seedLearningExcerpt(t, s)
	e, err := s.CreateLearningExcerpt(ctx, snap, []string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	session := "hashed-owner-session-one"
	d, _ := s.EnableOfflineDevice(ctx, session, "11111111-1111-4111-8111-111111111111")
	metadata := map[string]any{"excerpt_id": e.ID, "source_type": e.SourceType, "source_id": e.SourceID, "snapshot_id": e.SnapshotID, "audio_sha256": e.AudioSHA256, "segment_ids": e.SegmentIDs, "start_seconds": e.StartSeconds, "end_seconds": e.EndSeconds}
	manifest, _ := json.Marshal(map[string]any{"pack_id": uuid.NewString(), "files": []any{}, "excerpts": []any{metadata}})
	if _, err = s.SaveOfflinePack(ctx, session, d.Namespace, string(manifest), []OfflineSource{{SourceType: models.SourceEpisode, SourceID: id, SnapshotID: snap, AudioSHA256: e.AudioSHA256}}); err != nil {
		t.Fatal(err)
	}
	p := models.ListeningProgress{Mode: "excerpt", ExcerptID: e.ID, AudioSHA256: e.AudioSHA256, ItemOffsetSeconds: 25, Speed: 1, Seq: 1}
	raw, _ := json.Marshal(p)
	op := OfflineOperation{SchemaVersion: 1, Namespace: d.Namespace, EditedAt: time.Now().Unix(), UUID: uuid.NewString(), Kind: "excerpt_progress", SourceType: models.SourceEpisode, SourceID: id, SnapshotID: snap, Payload: raw}
	op.ExpectedRevision = 1
	op.PayloadHash = OfflineOperationHash(op)
	if _, err = s.ApplyOfflineOperation(ctx, session, d.Namespace, op); !errors.Is(err, ErrConflict) {
		t.Fatal("missing progress CAS", err)
	}
	op.ExpectedRevision = 0
	op.PayloadHash = OfflineOperationHash(op)
	r, err := s.ApplyOfflineOperation(ctx, session, d.Namespace, op)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := s.ApplyOfflineOperation(ctx, session, d.Namespace, op)
	if err != nil || *r != *replay {
		t.Fatal(replay, err)
	}
	if _, err = s.GetListeningProgressMode(ctx, models.SourceEpisode, id, "original"); !errors.Is(err, ErrNotFound) {
		t.Fatal("excerpt polluted full original", err)
	}
	if _, err = s.SaveListeningProgressCAS(ctx, &models.ListeningProgress{Mode: "original", SourceType: models.SourceEpisode, SourceID: id, AudioSHA256: e.AudioSHA256, ItemOffsetSeconds: 5, Speed: 1, Seq: 1}, 0); err != nil {
		t.Fatal(err)
	}
	p.ItemOffsetSeconds = 28
	p.Seq = 2
	op.Payload, _ = json.Marshal(p)
	op.UUID = uuid.NewString()
	op.ExpectedRevision = 1
	op.PayloadHash = OfflineOperationHash(op)
	r, err = s.ApplyOfflineOperation(ctx, session, d.Namespace, op)
	if err != nil || r.Revision != 2 {
		t.Fatal(r, err)
	}
	op.UUID = uuid.NewString()
	op.PayloadHash = OfflineOperationHash(op)
	if _, err = s.ApplyOfflineOperation(ctx, session, d.Namespace, op); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	original, err := s.GetListeningProgressMode(ctx, models.SourceEpisode, id, "original")
	if err != nil || original.ItemOffsetSeconds != 5 || original.Revision != 1 {
		t.Fatal("excerpt changed original", original, err)
	}
	p.ItemOffsetSeconds = 41
	op.Payload, _ = json.Marshal(p)
	op.ExpectedRevision = 2
	op.UUID = uuid.NewString()
	op.PayloadHash = OfflineOperationHash(op)
	if _, err = s.ApplyOfflineOperation(ctx, session, d.Namespace, op); !errors.Is(err, ErrInvalidEditorialState) {
		t.Fatal("outside interval", err)
	}
	p.ItemOffsetSeconds = 29
	op.Payload, _ = json.Marshal(p)
	op.PayloadHash = OfflineOperationHash(op)
	if _, err = s.DB.Exec(`CREATE TRIGGER fail_excerpt_receipt BEFORE INSERT ON offline_operation_receipts BEGIN SELECT RAISE(ABORT,'receipt failed');END`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ApplyOfflineOperation(ctx, session, d.Namespace, op); err == nil {
		t.Fatal("receipt failure accepted")
	}
	kept, err := s.GetLearningExcerptProgress(ctx, models.SourceEpisode, id, e.ID)
	if err != nil || kept.Revision != 2 || kept.ItemOffsetSeconds != 28 {
		t.Fatal("partial excerpt write", kept, err)
	}
	if _, err = s.DB.Exec(`DROP TRIGGER fail_excerpt_receipt`); err != nil {
		t.Fatal(err)
	}
	if err = s.UpsertEvidenceAudio(ctx, models.SourceEpisode, id, "replaced.mp3", "mp3", 100, "new-sha"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ApplyOfflineOperation(ctx, session, d.Namespace, op); !errors.Is(err, ErrConflict) {
		t.Fatal("replaced audio", err)
	}
}
func TestOfflineExcerptProgressRequiresManifestExactWindow(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	id, snap := seedLearningExcerpt(t, s)
	e, err := s.CreateLearningExcerpt(ctx, snap, []string{"a"})
	if err != nil {
		t.Fatal(err)
	}
	session := "hashed-owner-session-one"
	d, _ := s.EnableOfflineDevice(ctx, session, "11111111-1111-4111-8111-111111111111")
	manifest, _ := json.Marshal(map[string]any{"pack_id": uuid.NewString(), "files": []any{}, "excerpts": []any{map[string]any{"excerpt_id": e.ID, "source_type": e.SourceType, "source_id": e.SourceID, "snapshot_id": e.SnapshotID, "audio_sha256": e.AudioSHA256, "segment_ids": []string{"fake"}, "start_seconds": e.StartSeconds, "end_seconds": e.EndSeconds}}})
	if _, err = s.SaveOfflinePack(ctx, session, d.Namespace, string(manifest), []OfflineSource{{SourceType: models.SourceEpisode, SourceID: id, SnapshotID: snap, AudioSHA256: e.AudioSHA256}}); err != nil {
		t.Fatal(err)
	}
	p := models.ListeningProgress{Mode: "excerpt", ExcerptID: e.ID, AudioSHA256: e.AudioSHA256, ItemOffsetSeconds: 25, Speed: 1, Seq: 1}
	raw, _ := json.Marshal(p)
	op := OfflineOperation{SchemaVersion: 1, Namespace: d.Namespace, EditedAt: time.Now().Unix(), UUID: uuid.NewString(), Kind: "excerpt_progress", SourceType: models.SourceEpisode, SourceID: id, SnapshotID: snap, Payload: raw}
	op.PayloadHash = OfflineOperationHash(op)
	if _, err = s.ApplyOfflineOperation(ctx, session, d.Namespace, op); !errors.Is(err, ErrConflict) {
		t.Fatal("forged frozen segments", err)
	}
	if _, err = s.GetLearningExcerptProgress(ctx, models.SourceEpisode, id, e.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}
