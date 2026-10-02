package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"strings"
)

// UnderstandingHead separates saving an immutable answer from explicitly choosing it.
type UnderstandingHead struct {
	QuestionID        string
	Revision          int
	CurrentSnapshotID string
}

// UnderstandingReference is a frozen Reference, never a source Citation.
type UnderstandingReference struct {
	Kind, ObjectID, SourceType, SourceID, Body string
	Version                                    int
	Purged, Changed                            bool
}

// UnderstandingSnapshot is an Owner answer, including when no source exists.
type UnderstandingSnapshot struct {
	ID, QuestionID, ParentID, Answer, Uncertainty, NextStep, ModelDataPolicy, CreatedAt string
	Version                                                                             int
	ApprovedProviders                                                                   []string
	References                                                                          []UnderstandingReference
}

// SaveUnderstandingCommand freezes Owner content, exact References, both CAS revisions and an idempotent request identity.
type SaveUnderstandingCommand struct {
	QuestionID, ParentID, RequestKey, Answer, Uncertainty, NextStep, ModelDataPolicy string
	QuestionRevision, HeadRevision                                                   int
	ApprovedProviders                                                                []string
	References                                                                       []UnderstandingReference
}

// UnderstandingHead reads the selection revision; an existing question with no saved answer has revision zero.
func (s *Store) UnderstandingHead(ctx context.Context, id string) (UnderstandingHead, error) {
	h := UnderstandingHead{QuestionID: id}
	err := s.DB.QueryRowContext(ctx, `SELECT revision,current_snapshot_id FROM understanding_heads WHERE question_id=?`, id).Scan(&h.Revision, &h.CurrentSnapshotID)
	if errors.Is(err, sql.ErrNoRows) {
		_, err = s.GetLearningQuestion(ctx, id)
	}
	return h, err
}
func scanUnderstanding(row interface{ Scan(...any) error }) (*UnderstandingSnapshot, error) {
	v := &UnderstandingSnapshot{}
	var allowed string
	err := row.Scan(&v.ID, &v.QuestionID, &v.Version, &v.ParentID, &v.Answer, &v.Uncertainty, &v.NextStep, &v.ModelDataPolicy, &allowed, &v.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err == nil {
		err = json.Unmarshal([]byte(allowed), &v.ApprovedProviders)
	}
	return v, err
}

const understandingColumns = `id,question_id,version,parent_id,answer,uncertainty,next_step,model_data_policy,approved_providers_json,created_at`

// GetUnderstandingSnapshot reads an exact immutable answer and its possibly redacted Reference projections.
func (s *Store) GetUnderstandingSnapshot(ctx context.Context, id string) (*UnderstandingSnapshot, error) {
	v, e := scanUnderstanding(s.DB.QueryRowContext(ctx, `SELECT `+understandingColumns+` FROM understanding_snapshots WHERE id=?`, id))
	if e != nil {
		return nil, e
	}
	rows, e := s.DB.QueryContext(ctx, `SELECT kind,object_id,version,source_type,source_id,body,purged FROM understanding_references WHERE snapshot_id=? ORDER BY ordinal`, id)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	v.References = []UnderstandingReference{}
	for rows.Next() {
		var r UnderstandingReference
		if e = rows.Scan(&r.Kind, &r.ObjectID, &r.Version, &r.SourceType, &r.SourceID, &r.Body, &r.Purged); e != nil {
			return nil, e
		}
		v.References = append(v.References, r)
	}
	if e = rows.Err(); e != nil {
		return nil, e
	}
	rows.Close()
	for i := range v.References {
		r := &v.References[i]
		if r.Purged {
			continue
		}
		var version int
		var body string
		var e error
		switch r.Kind {
		case "note":
			e = s.DB.QueryRowContext(ctx, `SELECT revision,content FROM owner_notes WHERE id=?`, r.ObjectID).Scan(&version, &body)
			r.Changed = e != nil || version != r.Version
		case "keypoint":
			e = s.DB.QueryRowContext(ctx, `SELECT card_version,content FROM keypoint_index WHERE id=?`, r.ObjectID).Scan(&version, &body)
			r.Changed = e != nil || version != r.Version || body != r.Body
		case "article":
			e = s.DB.QueryRowContext(ctx, `SELECT working_revision FROM knowledge_articles WHERE id=?`, r.ObjectID).Scan(&version)
			r.Changed = e != nil || version != r.Version
		case "source":
			column := "current_transcript_version"
			if r.SourceType == "document" {
				column = "version"
			}
			e = s.DB.QueryRowContext(ctx, `SELECT COALESCE(`+column+`,0) FROM `+sourceTable(models.SourceType(r.SourceType))+` WHERE id=?`, r.SourceID).Scan(&version)
			r.Changed = e != nil || version != r.Version
		}
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return nil, e
		}
	}
	return v, nil
}

// GetCurrentUnderstanding reads only the explicitly selected snapshot; saving alone never changes that choice.
func (s *Store) GetCurrentUnderstanding(ctx context.Context, id string) (*UnderstandingSnapshot, error) {
	h, e := s.UnderstandingHead(ctx, id)
	if e != nil {
		return nil, e
	}
	if h.CurrentSnapshotID == "" {
		return nil, ErrNotFound
	}
	return s.GetUnderstandingSnapshot(ctx, h.CurrentSnapshotID)
}

// HistoryUnderstanding reads at most fifty versions, descending before an optional exclusive version cursor.
func (s *Store) HistoryUnderstanding(ctx context.Context, id string, before, limit int) ([]*UnderstandingSnapshot, error) {
	if limit < 1 || limit > 50 {
		limit = 20
	}
	rows, e := s.DB.QueryContext(ctx, `SELECT id FROM understanding_snapshots WHERE question_id=? AND (?=0 OR version<?) ORDER BY version DESC LIMIT ?`, id, before, before, limit)
	if e != nil {
		return nil, e
	}
	var ids []string
	for rows.Next() {
		var x string
		if e = rows.Scan(&x); e != nil {
			rows.Close()
			return nil, e
		}
		ids = append(ids, x)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	out := []*UnderstandingSnapshot{}
	for _, x := range ids {
		v, e := s.GetUnderstandingSnapshot(ctx, x)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, nil
}

func resolveUnderstandingReference(ctx context.Context, tx *sql.Tx, r UnderstandingReference) (UnderstandingReference, error) {
	if r.Version < 0 || (r.Kind != "keypoint" && r.Version < 1) {
		return r, ErrInvalidEditorialState
	}
	var err error
	switch r.Kind {
	case "note":
		err = tx.QueryRowContext(ctx, `SELECT source_type,source_id,content FROM owner_note_revisions WHERE note_id=? AND revision=?`, r.ObjectID, r.Version).Scan(&r.SourceType, &r.SourceID, &r.Body)
	case "keypoint":
		var version int
		err = tx.QueryRowContext(ctx, `SELECT source_type,source_id,content,card_version FROM keypoint_index WHERE id=? AND stale_at IS NULL`, r.ObjectID).Scan(&r.SourceType, &r.SourceID, &r.Body, &version)
		if err == nil && version != r.Version {
			return r, ErrConflict
		}
	case "evidence":
		err = tx.QueryRowContext(ctx, `SELECT source_type,source_id,title FROM source_snapshots WHERE id=? AND content_version=? AND status!='purged'`, r.ObjectID, r.Version).Scan(&r.SourceType, &r.SourceID, &r.Body)
	case "article":
		err = tx.QueryRowContext(ctx, `SELECT title||char(10)||COALESCE((SELECT group_concat(json_extract(b.value,'$.text'),char(10)) FROM json_each(blocks_json) b),'') FROM knowledge_article_revisions WHERE article_id=? AND revision=? AND evidence_status='valid'`, r.ObjectID, r.Version).Scan(&r.Body)
		r.SourceID = ""
		r.SourceType = ""
	case "source":
		parts := strings.SplitN(r.ObjectID, ":", 2)
		if len(parts) != 2 || !validSourceType(models.SourceType(parts[0])) || parts[1] == "" {
			return r, ErrInvalidEditorialState
		}
		r.SourceType, r.SourceID = parts[0], parts[1]
		if r.SourceType == "document" {
			err = tx.QueryRowContext(ctx, `SELECT title FROM documents WHERE id=? AND version=?`, r.SourceID, r.Version).Scan(&r.Body)
		} else {
			titleColumn := "title"
			if r.SourceType == "upload" {
				titleColumn = "original_filename"
			}
			err = tx.QueryRowContext(ctx, `SELECT src.`+titleColumn+` FROM `+sourceTable(models.SourceType(r.SourceType))+` src WHERE src.id=? AND EXISTS(SELECT 1 FROM artifact_versions av WHERE av.source_type=? AND av.source_id=src.id AND av.kind='transcript' AND av.version=?)`, r.SourceID, r.SourceType, r.Version).Scan(&r.Body)
		}
	default:
		return r, ErrInvalidEditorialState
	}
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	if err == nil && r.SourceID != "" {
		err = checkQuestionSource(ctx, tx, r.SourceType, r.SourceID)
	}
	r.Purged = false
	r.Changed = false
	return r, err
}

// SaveUnderstanding applies idempotent CAS in one transaction. It never resolves the question or selects current.
func (s *Store) SaveUnderstanding(ctx context.Context, c SaveUnderstandingCommand) (*UnderstandingSnapshot, error) {
	c.Answer = strings.TrimSpace(c.Answer)
	if c.ModelDataPolicy == "" {
		c.ModelDataPolicy = "local_only"
	}
	if _, e := uuid.Parse(c.RequestKey); e != nil || c.Answer == "" || len(c.Answer) > 40000 || len(c.Uncertainty) > 10000 || len(c.NextStep) > 10000 || len(c.References) > 100 || !validModelDataPolicy(models.ModelDataPolicy(c.ModelDataPolicy)) {
		return nil, ErrInvalidEditorialState
	}
	if len(c.QuestionID) > 200 || len(c.ParentID) > 200 || len(c.ApprovedProviders) > 100 {
		return nil, ErrInvalidEditorialState
	}
	seenReferences := map[string]bool{}
	for _, r := range c.References {
		if len(r.ObjectID) > 300 || len(r.Kind) > 40 || r.Version < 0 || (r.Kind != "keypoint" && r.Version < 1) {
			return nil, ErrInvalidEditorialState
		}
		raw, _ := json.Marshal([]any{r.Kind, r.ObjectID, r.Version})
		if seenReferences[string(raw)] {
			return nil, ErrInvalidEditorialState
		}
		seenReferences[string(raw)] = true
	}
	for i := range c.References {
		c.References[i].Body = ""
		c.References[i].SourceID = ""
		c.References[i].SourceType = ""
		c.References[i].Purged = false
		c.References[i].Changed = false
	}
	if c.ModelDataPolicy == "approved_providers_only" && len(c.ApprovedProviders) == 0 {
		return nil, ErrInvalidEditorialState
	}
	for i := range c.ApprovedProviders {
		c.ApprovedProviders[i] = strings.ToLower(strings.TrimSpace(c.ApprovedProviders[i]))
		if c.ApprovedProviders[i] == "" || len(c.ApprovedProviders[i]) > 100 {
			return nil, ErrInvalidEditorialState
		}
	}
	raw, _ := json.Marshal(c)
	sum := sha256.Sum256(raw)
	hash := hex.EncodeToString(sum[:])
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	var existing, oldhash string
	e = tx.QueryRowContext(ctx, `SELECT id,payload_hash FROM understanding_snapshots WHERE request_key=?`, c.RequestKey).Scan(&existing, &oldhash)
	if e == nil {
		if oldhash != hash {
			return nil, ErrConflict
		}
		tx.Rollback()
		return s.GetUnderstandingSnapshot(ctx, existing)
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return nil, e
	}
	var qr int
	if e = tx.QueryRowContext(ctx, `SELECT revision FROM learning_questions WHERE id=?`, c.QuestionID).Scan(&qr); errors.Is(e, sql.ErrNoRows) {
		return nil, ErrNotFound
	} else if e != nil {
		return nil, e
	}
	if qr != c.QuestionRevision {
		return nil, ErrConflict
	}
	if _, e = tx.ExecContext(ctx, `INSERT OR IGNORE INTO understanding_heads(question_id)VALUES(?)`, c.QuestionID); e != nil {
		return nil, e
	}
	var hr, version int
	var current string
	if e = tx.QueryRowContext(ctx, `SELECT revision,current_snapshot_id FROM understanding_heads WHERE question_id=?`, c.QuestionID).Scan(&hr, &current); e != nil {
		return nil, e
	}
	if hr != c.HeadRevision {
		return nil, ErrConflict
	}
	if c.ParentID != "" {
		var p string
		if e = tx.QueryRowContext(ctx, `SELECT question_id FROM understanding_snapshots WHERE id=?`, c.ParentID).Scan(&p); e != nil || p != c.QuestionID {
			return nil, ErrConflict
		}
	}
	if e = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(version),0)+1 FROM understanding_snapshots WHERE question_id=?`, c.QuestionID).Scan(&version); e != nil {
		return nil, e
	}
	refs := make([]UnderstandingReference, len(c.References))
	for i, r := range c.References {
		refs[i], e = resolveUnderstandingReference(ctx, tx, r)
		if e != nil {
			return nil, e
		}
	}
	id := uuid.NewString()
	allowed, _ := json.Marshal(c.ApprovedProviders)
	if _, e = tx.ExecContext(ctx, `INSERT INTO understanding_snapshots(id,question_id,version,parent_id,answer,uncertainty,next_step,model_data_policy,approved_providers_json,request_key,payload_hash)VALUES(?,?,?,?,?,?,?,?,?,?,?)`, id, c.QuestionID, version, c.ParentID, c.Answer, c.Uncertainty, c.NextStep, c.ModelDataPolicy, string(allowed), c.RequestKey, hash); e != nil {
		return nil, e
	}
	for i, r := range refs {
		if _, e = tx.ExecContext(ctx, `INSERT INTO understanding_references(snapshot_id,ordinal,kind,object_id,version,source_type,source_id,body)VALUES(?,?,?,?,?,?,?,?)`, id, i, r.Kind, r.ObjectID, r.Version, r.SourceType, r.SourceID, r.Body); e != nil {
			return nil, e
		}
	}
	if _, e = tx.ExecContext(ctx, `UPDATE understanding_heads SET revision=revision+1,updated_at=datetime('now') WHERE question_id=?`, c.QuestionID); e != nil {
		return nil, e
	}
	if e = tx.Commit(); e != nil {
		return nil, e
	}
	return s.GetUnderstandingSnapshot(ctx, id)
}

// ChooseCurrentUnderstanding selects an existing version with question/head CAS and same-payload UUID replay.
func (s *Store) ChooseCurrentUnderstanding(ctx context.Context, id, snapshot string, questionRev, headRev int, requestKey string) error {
	if _, e := uuid.Parse(requestKey); e != nil {
		return ErrInvalidEditorialState
	}
	raw, _ := json.Marshal([]any{id, snapshot, questionRev, headRev})
	sum := sha256.Sum256(raw)
	hash := hex.EncodeToString(sum[:])
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var prior string
	e = tx.QueryRowContext(ctx, `SELECT payload_hash FROM understanding_choose_requests WHERE request_key=?`, requestKey).Scan(&prior)
	if e == nil {
		if prior != hash {
			return ErrConflict
		}
		return nil
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return e
	}
	var qr int
	if e = tx.QueryRowContext(ctx, `SELECT revision FROM learning_questions WHERE id=?`, id).Scan(&qr); e != nil {
		return e
	}
	if qr != questionRev {
		return ErrConflict
	}
	res, e := tx.ExecContext(ctx, `UPDATE understanding_heads SET current_snapshot_id=?,revision=revision+1,updated_at=datetime('now') WHERE question_id=? AND revision=? AND EXISTS(SELECT 1 FROM understanding_snapshots WHERE id=? AND question_id=?)`, snapshot, id, headRev, snapshot, id)
	if e != nil {
		return e
	}
	n, e := res.RowsAffected()
	if e != nil {
		return e
	}
	if n != 1 {
		return ErrConflict
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO understanding_choose_requests(request_key,payload_hash,question_id,snapshot_id)VALUES(?,?,?,?)`, requestKey, hash, id, snapshot); e != nil {
		return e
	}
	return tx.Commit()
}

// UnderstandingMaySend enforces the Owner snapshot policy AND every frozen Reference source.
func (s *Store) UnderstandingMaySend(ctx context.Context, id string, version int, name string) (bool, error) {
	var allowed bool
	e := s.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM knowledge_search_docs d WHERE d.object_id=? AND d.revision=? AND `+understandingSendSQL("d", ":understanding_provider", false)+`)`, id, version, sql.Named("understanding_provider", name)).Scan(&allowed)
	return allowed, e
}

// UnderstandingKnowledgeMaterial returns an exact Owner material only after its own and every Reference policy permits the Provider.
func (s *Store) UnderstandingKnowledgeMaterial(ctx context.Context, id, name string) (*provider.KnowledgeMaterial, error) {
	v, e := s.GetUnderstandingSnapshot(ctx, id)
	if e != nil {
		return nil, e
	}
	ok, e := s.UnderstandingMaySend(ctx, id, v.Version, name)
	if e != nil || !ok {
		return nil, e
	}
	m := &provider.KnowledgeMaterial{ID: id, Kind: "understanding", Version: v.Version, SourceTitle: "Owner个人理解（Reference不是Citation）", Content: v.Answer, Description: "仍不确定：" + v.Uncertainty + "\n下一步：" + v.NextStep, NoPosition: true, Citations: []string{}}
	for _, r := range v.References {
		m.UnderstandingReferences = append(m.UnderstandingReferences, provider.KnowledgeUnderstandingReference{Kind: r.Kind, ObjectID: r.ObjectID, Version: r.Version, SourceType: r.SourceType, SourceID: r.SourceID, Body: r.Body, Purged: r.Purged})
	}
	return m, nil
}

// ReadUnderstandingReference resolves an exact Reference for an explicit Owner editor import.
func (s *Store) ReadUnderstandingReference(ctx context.Context, r UnderstandingReference) (UnderstandingReference, error) {
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return r, e
	}
	defer tx.Rollback()
	v, e := resolveUnderstandingReference(ctx, tx, r)
	if e != nil {
		return r, e
	}
	return v, tx.Commit()
}

// ReadCurrentSourceUnderstandingReference reads an existing exact source version for an explicit Reference, without creating a Source or Snapshot.
func (s *Store) ReadCurrentSourceUnderstandingReference(ctx context.Context, kind, id string) (UnderstandingReference, error) {
	r := UnderstandingReference{Kind: "source", ObjectID: kind + ":" + id}
	if !validSourceType(models.SourceType(kind)) {
		return r, ErrInvalidEditorialState
	}
	column := "current_transcript_version"
	if kind == "document" {
		column = "version"
	}
	if e := s.DB.QueryRowContext(ctx, `SELECT COALESCE(`+column+`,0) FROM `+sourceTable(models.SourceType(kind))+` WHERE id=? AND archived_at IS NULL`, id).Scan(&r.Version); e != nil {
		return r, e
	}
	return s.ReadUnderstandingReference(ctx, r)
}
