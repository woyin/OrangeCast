package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/models"
)

// OfflineOperation identifies one explicit foreground mutation. Its complete
// payload, CAS revision, namespace and anchors are bound by PayloadHash.
type OfflineOperation struct {
	SchemaVersion    int               `json:"schema_version"`
	Namespace        string            `json:"namespace"`
	EditedAt         int64             `json:"edited_at"`
	UUID             string            `json:"uuid"`
	Kind             string            `json:"kind"`
	PayloadHash      string            `json:"payload_hash"`
	ExpectedRevision int64             `json:"expected_revision"`
	SourceType       models.SourceType `json:"source_type"`
	SourceID         string            `json:"source_id"`
	SnapshotID       string            `json:"snapshot_id"`
	Payload          json.RawMessage   `json:"payload"`
}

// OfflineReceipt proves a committed mutation for one UUID/hash. Replaying a lost
// acknowledgement returns the same result instead of performing a second write.
type OfflineReceipt struct {
	UUID     string `json:"uuid"`
	Status   string `json:"status"`
	ObjectID string `json:"object_id"`
	Revision int64  `json:"revision"`
}

// OfflineOperationHash binds the entire command including CAS and source identity.
func OfflineOperationHash(op OfflineOperation) string {
	op.PayloadHash = ""
	raw, _ := json.Marshal(op)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// ApplyOfflineOperation validates the granted pack scope and existing business
// constraints, then commits the note/draft/independent progress and its deduplication
// receipt in one transaction. Conflicts leave browser text untouched; no AI runs.
func (s *Store) ApplyOfflineOperation(ctx context.Context, session, namespace string, op OfflineOperation) (*OfflineReceipt, error) {
	if op.SchemaVersion != 1 || op.Namespace != namespace || op.EditedAt <= 0 || op.EditedAt > time.Now().Unix()+300 || uuid.Validate(op.UUID) != nil || len(op.Payload) == 0 || len(op.Payload) > 65536 || !json.Valid(op.Payload) || op.ExpectedRevision < 0 || op.PayloadHash != OfflineOperationHash(op) {
		return nil, ErrInvalidEditorialState
	}
	if _, err := s.ValidateOfflineDevice(ctx, session, namespace); err != nil {
		return nil, err
	}
	// Replayed accepted commands do not prepare or mutate business objects again.
	var priorHash, prior string
	err := s.DB.QueryRowContext(ctx, `SELECT payload_hash,receipt_json FROM offline_operation_receipts WHERE namespace=? AND uuid=?`, namespace, op.UUID).Scan(&priorHash, &prior)
	if err == nil {
		if priorHash != op.PayloadHash {
			return nil, ErrConflict
		}
		var r OfflineReceipt
		err = json.Unmarshal([]byte(prior), &r)
		return &r, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	var note *models.OwnerNote
	var progress models.ListeningProgress
	var excerpt *models.LearningExcerpt
	var draft struct {
		ID      string `json:"id"`
		Content string `json:"content"`
	}
	switch op.Kind {
	case "owner_note":
		var input models.OwnerNote
		if json.Unmarshal(op.Payload, &input) != nil {
			return nil, ErrInvalidEditorialState
		}
		if utf8.RuneCountInString(input.Content) > 20000 {
			return nil, ErrInvalidEditorialState
		}
		input.SourceType = string(op.SourceType)
		input.SourceID = op.SourceID
		var current *models.OwnerNote
		if op.ExpectedRevision > 0 {
			current, err = s.GetOwnerNote(ctx, input.ID)
			if err != nil {
				return nil, err
			}
			if current.SourceType != string(op.SourceType) || current.SourceID != op.SourceID || int64(current.Revision) != op.ExpectedRevision {
				return nil, ErrConflict
			}
			input.Kind = current.Kind
		}
		note, err = s.prepareOwnerNote(ctx, input)
		if err != nil {
			return nil, err
		}
		if current != nil {
			note.ID = current.ID
		}
		var anchor models.NoteAnchor
		if json.Unmarshal([]byte(note.AnchorJSON), &anchor) != nil || anchor.SnapshotID != op.SnapshotID {
			return nil, ErrConflict
		}
	case "original_progress":
		if json.Unmarshal(op.Payload, &progress) != nil {
			return nil, ErrInvalidEditorialState
		}
		if progress.Mode != "" && progress.Mode != "original" || progress.ExcerptID != "" {
			return nil, ErrInvalidEditorialState
		}
		progress.SourceType = op.SourceType
		progress.SourceID = op.SourceID
		progress.Mode = "original"
		if progress.PlanID != "" || progress.PlanVersion != 0 || progress.ItemPosition != 0 || progress.HighlightID != "" || progress.AudioSHA256 == "" || progress.ItemOffsetSeconds < 0 || math.IsNaN(progress.ItemOffsetSeconds) || math.IsInf(progress.ItemOffsetSeconds, 0) || math.IsNaN(progress.Speed) || math.IsInf(progress.Speed, 0) || progress.Speed < 0.75 || progress.Speed > 2 {
			return nil, ErrInvalidEditorialState
		}
		if err = s.checkProgressAudio(ctx, &progress); err != nil {
			return nil, err
		}
		progress.ID = uuid.NewString()
	case "excerpt_progress":
		if json.Unmarshal(op.Payload, &progress) != nil || progress.Mode != "excerpt" || progress.ExcerptID == "" || progress.PlanID != "" || progress.PlanVersion != 0 || progress.ItemPosition != 0 || progress.HighlightID != "" || progress.AudioSHA256 == "" || math.IsNaN(progress.ItemOffsetSeconds) || math.IsInf(progress.ItemOffsetSeconds, 0) || math.IsNaN(progress.Speed) || math.IsInf(progress.Speed, 0) || progress.Speed < 0.75 || progress.Speed > 2 {
			return nil, ErrInvalidEditorialState
		}
		excerpt, err = s.GetLearningExcerpt(ctx, progress.ExcerptID)
		if err != nil {
			return nil, err
		}
		if excerpt.SourceType != op.SourceType || excerpt.SourceID != op.SourceID || excerpt.SnapshotID != op.SnapshotID || excerpt.AudioSHA256 != progress.AudioSHA256 {
			return nil, ErrConflict
		}
		if progress.ItemOffsetSeconds < excerpt.StartSeconds || progress.ItemOffsetSeconds > excerpt.EndSeconds {
			return nil, ErrInvalidEditorialState
		}
		if _, err = s.CheckLearningExcerpt(ctx, excerpt); err != nil {
			return nil, err
		}
		progress.ID = uuid.NewString()
	case "organizing_draft":
		if json.Unmarshal(op.Payload, &draft) != nil || strings.TrimSpace(draft.Content) == "" || utf8.RuneCountInString(draft.Content) > 20000 {
			return nil, ErrInvalidEditorialState
		}
		if op.ExpectedRevision == 0 {
			draft.ID = uuid.NewString()
		} else if uuid.Validate(draft.ID) != nil {
			return nil, ErrInvalidEditorialState
		}
	default:
		return nil, ErrInvalidEditorialState
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var authorized int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM offline_devices d JOIN offline_epoch e ON e.epoch=d.epoch WHERE d.namespace=? AND d.session_hash=? AND d.revoked=0 AND d.issued_at<=? AND d.expires_at>?`, namespace, session, time.Now().Unix(), time.Now().Unix()).Scan(&authorized); err != nil {
		return nil, err
	}
	if authorized != 1 {
		return nil, ErrOfflineRevoked
	}
	if err = checkOfflinePackScopeTx(ctx, tx, namespace, op); err != nil {
		return nil, err
	}
	if err = checkOfflineSourceTx(ctx, tx, op); err != nil {
		return nil, err
	}
	if excerpt != nil {
		var snapshot, sha, segments string
		var start, end float64
		if err = tx.QueryRowContext(ctx, `SELECT snapshot_id,audio_sha256,segment_ids_json,start_seconds,end_seconds FROM learning_excerpts WHERE id=? AND source_type=? AND source_id=?`, excerpt.ID, op.SourceType, op.SourceID).Scan(&snapshot, &sha, &segments, &start, &end); err != nil {
			return nil, ErrConflict
		}
		ids, _ := json.Marshal(excerpt.SegmentIDs)
		if snapshot != excerpt.SnapshotID || sha != excerpt.AudioSHA256 || segments != string(ids) || start != excerpt.StartSeconds || end != excerpt.EndSeconds {
			return nil, ErrConflict
		}
	}
	// A concurrent replay also sees the same receipt within the write transaction.
	err = tx.QueryRowContext(ctx, `SELECT payload_hash,receipt_json FROM offline_operation_receipts WHERE namespace=? AND uuid=?`, namespace, op.UUID).Scan(&priorHash, &prior)
	if err == nil {
		if priorHash != op.PayloadHash {
			return nil, ErrConflict
		}
		var r OfflineReceipt
		err = json.Unmarshal([]byte(prior), &r)
		return &r, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	r := &OfflineReceipt{UUID: op.UUID, Status: "ack", Revision: op.ExpectedRevision + 1}
	var result sql.Result
	switch op.Kind {
	case "owner_note":
		r.ObjectID = note.ID
		if op.ExpectedRevision == 0 {
			result, err = tx.ExecContext(ctx, `INSERT INTO owner_notes(id,source_type,source_id,kind,content,citations_json,references_json,anchor_json)VALUES(?,?,?,?,?,?,?,?)`, note.ID, note.SourceType, note.SourceID, note.Kind, note.Content, note.CitationsJSON, note.ReferencesJSON, note.AnchorJSON)
		} else {
			result, err = tx.ExecContext(ctx, `UPDATE owner_notes SET content=?,citations_json=?,references_json=?,anchor_json=?,revision=revision+1,updated_at=datetime('now') WHERE id=? AND source_type=? AND source_id=? AND kind=? AND revision=?`, note.Content, note.CitationsJSON, note.ReferencesJSON, note.AnchorJSON, note.ID, note.SourceType, note.SourceID, note.Kind, op.ExpectedRevision)
		}
	case "organizing_draft":
		r.ObjectID = draft.ID
		if op.ExpectedRevision == 0 {
			result, err = tx.ExecContext(ctx, `INSERT INTO offline_organizing_drafts(id,namespace,source_type,source_id,snapshot_id,content)VALUES(?,?,?,?,?,?)`, draft.ID, namespace, op.SourceType, op.SourceID, op.SnapshotID, draft.Content)
		} else {
			result, err = tx.ExecContext(ctx, `UPDATE offline_organizing_drafts SET content=?,revision=revision+1,updated_at=datetime('now') WHERE id=? AND namespace=? AND source_type=? AND source_id=? AND snapshot_id=? AND revision=?`, draft.Content, draft.ID, namespace, op.SourceType, op.SourceID, op.SnapshotID, op.ExpectedRevision)
		}
	case "excerpt_progress":
		r.ObjectID = progress.ID
		if op.ExpectedRevision == 0 {
			result, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO learning_excerpt_progress(id,excerpt_id,item_offset_seconds,speed,seq)VALUES(?,?,?,?,?)`, progress.ID, excerpt.ID, progress.ItemOffsetSeconds, progress.Speed, progress.Seq)
		} else {
			result, err = tx.ExecContext(ctx, `UPDATE learning_excerpt_progress SET item_offset_seconds=?,speed=?,seq=?,revision=revision+1,updated_at=datetime('now') WHERE excerpt_id=? AND revision=?`, progress.ItemOffsetSeconds, progress.Speed, progress.Seq, excerpt.ID, op.ExpectedRevision)
			if err == nil {
				err = tx.QueryRowContext(ctx, `SELECT id FROM learning_excerpt_progress WHERE excerpt_id=?`, excerpt.ID).Scan(&r.ObjectID)
			}
		}
	case "original_progress":
		r.ObjectID = progress.ID
		if op.ExpectedRevision == 0 {
			result, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO listening_progress(id,source_type,source_id,mode,item_offset_seconds,speed,seq,audio_sha256)VALUES(?,?,?,'original',?,?,?,?)`, progress.ID, op.SourceType, op.SourceID, progress.ItemOffsetSeconds, progress.Speed, progress.Seq, progress.AudioSHA256)
		} else {
			result, err = tx.ExecContext(ctx, `UPDATE listening_progress SET item_offset_seconds=?,speed=?,seq=?,audio_sha256=?,revision=revision+1,updated_at=datetime('now') WHERE source_type=? AND source_id=? AND mode='original' AND revision=? AND audio_sha256=?`, progress.ItemOffsetSeconds, progress.Speed, progress.Seq, progress.AudioSHA256, op.SourceType, op.SourceID, op.ExpectedRevision, progress.AudioSHA256)
			if err == nil {
				err = tx.QueryRowContext(ctx, `SELECT id FROM listening_progress WHERE source_type=? AND source_id=? AND mode='original'`, op.SourceType, op.SourceID).Scan(&r.ObjectID)
			}
		}
	}
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrConflict
	}
	if err != nil {
		return nil, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	if n != 1 {
		return nil, ErrConflict
	}
	raw, _ := json.Marshal(r)
	if _, err = tx.ExecContext(ctx, `INSERT INTO offline_operation_receipts VALUES(?,?,?,?)`, namespace, op.UUID, op.PayloadHash, string(raw)); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return r, nil
}
func checkOfflineSourceTx(ctx context.Context, tx *sql.Tx, op OfflineOperation) error {
	if !validSourceType(op.SourceType) || op.SourceID == "" || op.SnapshotID == "" {
		return ErrInvalidEditorialState
	}
	var version int
	var sha, kind string
	err := tx.QueryRowContext(ctx, `SELECT content_version,audio_sha256,kind FROM source_snapshots WHERE id=? AND source_type=? AND source_id=? AND status='active'`, op.SnapshotID, op.SourceType, op.SourceID).Scan(&version, &sha, &kind)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrSnapshotInvalidated
		}
		return err
	}
	var current int
	switch op.SourceType {
	case models.SourceEpisode:
		err = tx.QueryRowContext(ctx, `SELECT current_transcript_version FROM episodes WHERE id=?`, op.SourceID).Scan(&current)
	case models.SourceUpload:
		err = tx.QueryRowContext(ctx, `SELECT current_transcript_version FROM uploads WHERE id=?`, op.SourceID).Scan(&current)
	case models.SourceDocument:
		err = tx.QueryRowContext(ctx, `SELECT version FROM documents WHERE id=?`, op.SourceID).Scan(&current)
	}
	if err != nil {
		return ErrSnapshotInvalidated
	}
	if current != version {
		return ErrConflict
	}
	var purging int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM purges WHERE source_type=? AND source_id=? AND status='pending'`, op.SourceType, op.SourceID).Scan(&purging); err != nil {
		return err
	}
	if purging > 0 {
		return ErrSnapshotInvalidated
	}
	if (op.Kind == "original_progress" || op.Kind == "excerpt_progress") && (kind != "audio" || sha == "") {
		return ErrConflict
	}
	if kind == "audio" && sha != "" {
		var ready int
		if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM evidence_audio WHERE source_type=? AND source_id=? AND sha256=? AND status='ready'`, op.SourceType, op.SourceID, sha).Scan(&ready); err != nil {
			return err
		}
		if op.Kind == "original_progress" || op.Kind == "excerpt_progress" {
			var p models.ListeningProgress
			if json.Unmarshal(op.Payload, &p) != nil || p.AudioSHA256 != sha {
				return ErrConflict
			}
		}
		if ready != 1 {
			return ErrConflict
		}
	}
	return nil
}

func checkOfflinePackScopeTx(ctx context.Context, tx *sql.Tx, namespace string, op OfflineOperation) error {
	rows, err := tx.QueryContext(ctx, `SELECT sources_json,manifest_json FROM offline_packs WHERE namespace=?`, namespace)
	if err != nil {
		return err
	}
	defer rows.Close()
	matched := false
	for rows.Next() {
		var raw, manifest string
		if err = rows.Scan(&raw, &manifest); err != nil {
			return err
		}
		var sources []OfflineSource
		if err = json.Unmarshal([]byte(raw), &sources); err != nil {
			return err
		}
		sourceMatch := false
		for _, source := range sources {
			if source.SourceType == op.SourceType && source.SourceID == op.SourceID && source.SnapshotID == op.SnapshotID {
				sourceMatch = true
			}
		}
		if !sourceMatch {
			continue
		}
		if op.Kind != "excerpt_progress" {
			matched = true
			continue
		}
		var progress models.ListeningProgress
		var m struct {
			Excerpts []struct {
				ExcerptID    string            `json:"excerpt_id"`
				SourceType   models.SourceType `json:"source_type"`
				SourceID     string            `json:"source_id"`
				SnapshotID   string            `json:"snapshot_id"`
				AudioSHA256  string            `json:"audio_sha256"`
				SegmentIDs   []string          `json:"segment_ids"`
				StartSeconds float64           `json:"start_seconds"`
				EndSeconds   float64           `json:"end_seconds"`
			} `json:"excerpts"`
		}
		if json.Unmarshal(op.Payload, &progress) != nil || json.Unmarshal([]byte(manifest), &m) != nil {
			return ErrInvalidEditorialState
		}
		for _, e := range m.Excerpts {
			if e.ExcerptID != progress.ExcerptID || e.SourceType != op.SourceType || e.SourceID != op.SourceID || e.SnapshotID != op.SnapshotID || e.AudioSHA256 != progress.AudioSHA256 {
				continue
			}
			var snap, sha, ids string
			var start, end float64
			if err = tx.QueryRowContext(ctx, `SELECT snapshot_id,audio_sha256,segment_ids_json,start_seconds,end_seconds FROM learning_excerpts WHERE id=? AND source_type=? AND source_id=?`, e.ExcerptID, op.SourceType, op.SourceID).Scan(&snap, &sha, &ids, &start, &end); err != nil {
				return ErrConflict
			}
			expectedIDs, _ := json.Marshal(e.SegmentIDs)
			if snap != e.SnapshotID || sha != e.AudioSHA256 || ids != string(expectedIDs) || start != e.StartSeconds || end != e.EndSeconds {
				return ErrConflict
			}
			matched = true
		}
	}
	if err = rows.Err(); err != nil {
		return err
	}
	if !matched {
		return ErrOfflineRevoked
	}
	return nil
}

// OfflineOrganizingDraft is Owner-authored text. Source deletion invalidates its
// anchor without deleting the Owner's thoughts or moving them to another source.
type OfflineOrganizingDraft struct {
	ID              string            `json:"id"`
	SourceType      models.SourceType `json:"source_type"`
	SourceID        string            `json:"source_id"`
	SnapshotID      string            `json:"snapshot_id"`
	Content         string            `json:"content"`
	Revision        int64             `json:"revision"`
	UpdatedAt       string            `json:"updated_at"`
	SourceAvailable bool              `json:"source_available"`
}

// ListOfflineOrganizingDrafts exposes committed Owner text, separately marking
// unavailable source anchors. It never promotes a draft to an AI-generated article.
func (s *Store) ListOfflineOrganizingDrafts(ctx context.Context) ([]OfflineOrganizingDraft, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,source_type,source_id,snapshot_id,content,revision,updated_at FROM offline_organizing_drafts ORDER BY updated_at DESC,id LIMIT 500`)
	if err != nil {
		return nil, err
	}
	out := []OfflineOrganizingDraft{}
	for rows.Next() {
		var d OfflineOrganizingDraft
		if err = rows.Scan(&d.ID, &d.SourceType, &d.SourceID, &d.SnapshotID, &d.Content, &d.Revision, &d.UpdatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, d)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].SourceAvailable = s.offlineDraftSourceAvailable(ctx, out[i])
	}
	return out, nil
}

// GetOfflineOrganizingDraft returns a committed draft and fresh source availability;
// purging its source retains the Owner text without inventing replacement evidence.
func (s *Store) GetOfflineOrganizingDraft(ctx context.Context, id string) (*OfflineOrganizingDraft, error) {
	d := &OfflineOrganizingDraft{}
	err := s.DB.QueryRowContext(ctx, `SELECT id,source_type,source_id,snapshot_id,content,revision,updated_at FROM offline_organizing_drafts WHERE id=?`, id).Scan(&d.ID, &d.SourceType, &d.SourceID, &d.SnapshotID, &d.Content, &d.Revision, &d.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	d.SourceAvailable = s.offlineDraftSourceAvailable(ctx, *d)
	return d, nil
}
func (s *Store) offlineDraftSourceAvailable(ctx context.Context, d OfflineOrganizingDraft) bool {
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return false
	}
	defer tx.Rollback()
	return checkOfflineSourceTx(ctx, tx, OfflineOperation{SourceType: d.SourceType, SourceID: d.SourceID, SnapshotID: d.SnapshotID}) == nil
}
