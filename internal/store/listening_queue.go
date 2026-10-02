package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/models"
)

// ListeningQueueChange is an explicit queue edit, guarded by an order revision.
type ListeningQueueChange struct {
	ExcerptID   string            `json:"excerpt_id"`
	Action      string            `json:"action"` // add | play | remove | clear | move_last | reorder | autoplay | stop
	ItemID      string            `json:"item_id"`
	SourceType  models.SourceType `json:"source_type"`
	SourceID    string            `json:"source_id"`
	Mode        string            `json:"mode"`
	PlanID      string            `json:"plan_id"`
	PlanVersion int               `json:"plan_version"`
	Order       []string          `json:"order"`
	Autoplay    *bool             `json:"autoplay"`
}

const listeningQueueRows = `SELECT q.id,q.source_type,q.source_id,q.mode,q.plan_id,q.plan_version,q.audio_sha256,
 COALESCE(e.title,u.original_filename,q.title),q.position,q.invalid_reason,
 COALESCE(e.id,u.id,''),COALESCE(e.archived_at,u.archived_at,''),COALESCE(e.audio_url,''),
 COALESCE(a.status,''),COALESCE(a.sha256,''),COALESCE(p.id,''),COALESCE(p.version,0),COALESCE(CASE WHEN json_valid(p.input_snapshot_json) THEN json_extract(p.input_snapshot_json,'$.source_snapshot_id') ELSE '' END,''),COALESCE(snap.audio_sha256,''),q.excerpt_id,COALESCE(x.snapshot_id,''),COALESCE(x.segment_ids_json,'[]'),COALESCE(x.start_seconds,0),COALESCE(x.end_seconds,0),COALESCE(xs.status,''),COALESCE(xs.audio_sha256,''),COALESCE(xv.id,''),COALESCE(xs.content_version,0)
 FROM listening_queue_entries q
 LEFT JOIN learning_excerpts x ON x.id=q.excerpt_id AND x.source_type=q.source_type AND x.source_id=q.source_id
 LEFT JOIN source_snapshots xs ON xs.id=x.snapshot_id
 LEFT JOIN artifact_versions xv ON xv.id=xs.content_version_id
 LEFT JOIN episodes e ON q.source_type='episode' AND e.id=q.source_id
 LEFT JOIN uploads u ON q.source_type='upload' AND u.id=q.source_id
 LEFT JOIN evidence_audio a ON a.source_type=q.source_type AND a.source_id=q.source_id
 LEFT JOIN dj_plans p ON p.id=q.plan_id AND p.source_type=q.source_type AND p.source_id=q.source_id
 LEFT JOIN source_snapshots snap ON snap.id=CASE WHEN json_valid(p.input_snapshot_json) THEN json_extract(p.input_snapshot_json,'$.source_snapshot_id') ELSE '' END`

func scanListeningQueueItem(row interface{ Scan(...any) error }) (models.ListeningQueueItem, error) {
	var item models.ListeningQueueItem
	var invalid, exists, archived, remote, status, hash, plan string
	var planVersion int
	var frozenID, frozenAudio, segmentJSON, excerptStatus, excerptAudio, excerptVersion string
	err := row.Scan(&item.ID, &item.SourceType, &item.SourceID, &item.Mode, &item.PlanID, &item.PlanVersion, &item.AudioSHA256, &item.Title, &item.Position, &invalid, &exists, &archived, &remote, &status, &hash, &plan, &planVersion, &frozenID, &frozenAudio, &item.ExcerptID, &item.SnapshotID, &segmentJSON, &item.StartSeconds, &item.EndSeconds, &excerptStatus, &excerptAudio, &excerptVersion, &item.SnapshotVersion)
	if err != nil {
		return item, err
	}
	if json.Unmarshal([]byte(segmentJSON), &item.SegmentIDs) != nil {
		return item, ErrInvalidEditorialState
	}
	item.Unfrozen = item.AudioSHA256 == ""
	switch {
	case invalid != "":
		item.Reason = invalid
	case exists == "":
		item.Reason = "来源已删除"
	case archived != "":
		item.Reason = "来源已归档"
	case item.Mode == "excerpt" && (item.SnapshotID == "" || excerptStatus == "purged" || excerptVersion == "" || excerptAudio == "" || excerptAudio != hash || status != "ready" || item.AudioSHA256 != excerptAudio):
		item.Reason = "补听区间的快照或冻结原音已失效"
	case item.Mode == "dj" && (plan == "" || planVersion != item.PlanVersion):
		item.Reason = "冻结DJ清单不可用"
	case item.Mode == "dj" && frozenID != "" && (frozenAudio == "" || frozenAudio != hash):
		item.Reason = "DJ冻结原音已变化或身份无法确认"
	case status != "" && status != "ready":
		item.Reason = "原音文件不可用"
	case item.AudioSHA256 != "" && hash != item.AudioSHA256:
		item.Reason = "原音已变化，旧位置不能用于新音频"
	case item.Mode == "dj" && status != "ready":
		item.Reason = "DJ所需原音不可用"
	case status == "" && (item.SourceType != "episode" || remote == ""):
		item.Reason = "没有可播放音频"
	default:
		item.Available = true
		if item.Mode == "dj" && frozenID == "" {
			item.Unfrozen = true
		}
		if item.Unfrozen {
			item.Reason = "音频身份未冻结，恢复时需确认位置"
		}
	}
	return item, nil
}

// GetListeningQueue returns the complete bounded queue without model calls.
func (s *Store) GetListeningQueue(ctx context.Context) (*models.ListeningQueue, error) {
	q := &models.ListeningQueue{Items: []models.ListeningQueueItem{}}
	if err := s.DB.QueryRowContext(ctx, `SELECT revision,current_item_id,autoplay FROM listening_queue_state WHERE id=1`).Scan(&q.Revision, &q.CurrentItemID, &q.Autoplay); err != nil {
		return nil, err
	}
	rows, err := s.DB.QueryContext(ctx, listeningQueueRows+` ORDER BY q.position,q.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		item, err := scanListeningQueueItem(rows)
		if err != nil {
			return nil, err
		}
		q.Items = append(q.Items, item)
	}
	return q, rows.Err()
}

// ChangeListeningQueue atomically applies explicit edits. Duplicate add reuses
// the existing identity and position even when the caller's revision is stale.
// Other edits require an exact revision; none delete sources or enqueue models.
func (s *Store) ChangeListeningQueue(ctx context.Context, expected int64, change ListeningQueueChange) (*models.ListeningQueue, error) {
	if expected < 0 || len(change.Order) > 500 || len(change.ItemID) > 200 || len(change.SourceID) > 200 || len(change.PlanID) > 200 || len(change.ExcerptID) > 200 {
		return nil, ErrInvalidEditorialState
	}
	var excerpt *models.LearningExcerpt
	if change.Action == "add" && change.Mode == "excerpt" {
		var e error
		excerpt, e = s.GetLearningExcerpt(ctx, change.ExcerptID)
		if e != nil {
			return nil, e
		}
		if excerpt.SourceType != change.SourceType || excerpt.SourceID != change.SourceID || change.PlanID != "" || change.PlanVersion != 0 {
			return nil, ErrInvalidEditorialState
		}
		if _, e = s.CheckLearningExcerpt(ctx, excerpt); e != nil {
			return nil, e
		}
	} else if change.Action == "add" && change.ExcerptID != "" {
		return nil, ErrInvalidEditorialState
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var revision int64
	if err := tx.QueryRowContext(ctx, `SELECT revision FROM listening_queue_state WHERE id=1`).Scan(&revision); err != nil {
		return nil, err
	}
	if change.Action == "add" {
		var existing string
		err := tx.QueryRowContext(ctx, `SELECT id FROM listening_queue_entries WHERE source_type=? AND source_id=? AND mode=? AND plan_id=? AND plan_version=? AND excerpt_id=?`, change.SourceType, change.SourceID, change.Mode, change.PlanID, change.PlanVersion, change.ExcerptID).Scan(&existing)
		if err == nil {
			tx.Rollback()
			return s.GetListeningQueue(ctx)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
	}
	if revision != expected {
		return nil, ErrConflict
	}
	switch change.Action {
	case "add":
		if (change.SourceType != models.SourceEpisode && change.SourceType != models.SourceUpload) || strings.TrimSpace(change.SourceID) == "" || (change.Mode != "original" && change.Mode != "dj" && change.Mode != "excerpt") {
			return nil, ErrInvalidEditorialState
		}
		if change.Mode == "original" && (change.PlanID != "" || change.PlanVersion != 0) {
			return nil, ErrInvalidEditorialState
		}
		if change.Mode == "dj" && (change.PlanID == "" || change.PlanVersion < 1) {
			return nil, ErrInvalidEditorialState
		}
		var title, archived, hash, status string
		query := `SELECT title,COALESCE(archived_at,'') FROM episodes WHERE id=?`
		if change.SourceType == models.SourceUpload {
			query = `SELECT original_filename,COALESCE(archived_at,'') FROM uploads WHERE id=?`
		}
		if err := tx.QueryRowContext(ctx, query, change.SourceID).Scan(&title, &archived); errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		} else if err != nil {
			return nil, err
		}
		if archived != "" {
			return nil, fmt.Errorf("%w: 来源已归档", ErrInvalidEditorialState)
		}
		err := tx.QueryRowContext(ctx, `SELECT sha256,status FROM evidence_audio WHERE source_type=? AND source_id=?`, change.SourceType, change.SourceID).Scan(&hash, &status)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		if change.Mode == "excerpt" && (excerpt == nil || hash != excerpt.AudioSHA256 || status != "ready") {
			return nil, ErrConflict
		}
		if change.Mode == "dj" {
			var version int
			if err := tx.QueryRowContext(ctx, `SELECT version FROM dj_plans WHERE id=? AND source_type=? AND source_id=?`, change.PlanID, change.SourceType, change.SourceID).Scan(&version); errors.Is(err, sql.ErrNoRows) {
				return nil, ErrNotFound
			} else if err != nil {
				return nil, err
			}
			if version != change.PlanVersion {
				return nil, ErrConflict
			}
		}
		var count, position int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(MAX(position),0)+1 FROM listening_queue_entries`).Scan(&count, &position); err != nil {
			return nil, err
		}
		if count >= 500 {
			return nil, fmt.Errorf("%w: 收听队列最多500项，请移除已听条目", ErrInvalidEditorialState)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO listening_queue_entries(id,source_type,source_id,mode,plan_id,plan_version,audio_sha256,title,position,excerpt_id)VALUES(?,?,?,?,?,?,?,?,?,?)`, uuid.NewString(), change.SourceType, change.SourceID, change.Mode, change.PlanID, change.PlanVersion, hash, title, position, change.ExcerptID); err != nil {
			return nil, err
		}
	case "play":
		item, err := scanListeningQueueItem(tx.QueryRowContext(ctx, listeningQueueRows+` WHERE q.id=?`, change.ItemID))
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		} else if err != nil {
			return nil, err
		}
		if !item.Available {
			return nil, fmt.Errorf("%w: %s", ErrInvalidEditorialState, item.Reason)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE listening_queue_state SET current_item_id=? WHERE id=1`, item.ID); err != nil {
			return nil, err
		}
	case "remove", "move_last":
		statement := `DELETE FROM listening_queue_entries WHERE id=?`
		if change.Action == "move_last" {
			statement = `UPDATE listening_queue_entries SET position=(SELECT COALESCE(MAX(position),0)+1 FROM listening_queue_entries) WHERE id=?`
		}
		res, err := tx.ExecContext(ctx, statement, change.ItemID)
		if err != nil {
			return nil, err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil, ErrNotFound
		}
		if change.Action == "remove" {
			if _, err := tx.ExecContext(ctx, `UPDATE listening_queue_state SET current_item_id='' WHERE current_item_id=?`, change.ItemID); err != nil {
				return nil, err
			}
		}
	case "clear":
		if _, err := tx.ExecContext(ctx, `DELETE FROM listening_queue_entries`); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE listening_queue_state SET current_item_id='' WHERE id=1`); err != nil {
			return nil, err
		}
	case "stop":
		if _, err := tx.ExecContext(ctx, `UPDATE listening_queue_state SET current_item_id='' WHERE id=1`); err != nil {
			return nil, err
		}
	case "autoplay":
		if change.Autoplay == nil {
			return nil, ErrInvalidEditorialState
		}
		if _, err := tx.ExecContext(ctx, `UPDATE listening_queue_state SET autoplay=? WHERE id=1`, *change.Autoplay); err != nil {
			return nil, err
		}
	case "reorder":
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM listening_queue_entries`).Scan(&count); err != nil {
			return nil, err
		}
		if len(change.Order) != count {
			return nil, ErrConflict
		}
		seen := map[string]bool{}
		for i, id := range change.Order {
			if seen[id] || id == "" {
				return nil, ErrInvalidEditorialState
			}
			seen[id] = true
			res, err := tx.ExecContext(ctx, `UPDATE listening_queue_entries SET position=? WHERE id=?`, i+1, id)
			if err != nil {
				return nil, err
			}
			if n, _ := res.RowsAffected(); n != 1 {
				return nil, ErrConflict
			}
		}
	default:
		return nil, ErrInvalidEditorialState
	}
	res, err := tx.ExecContext(ctx, `UPDATE listening_queue_state SET revision=revision+1 WHERE id=1 AND revision=?`, expected)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return nil, ErrConflict
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetListeningQueue(ctx)
}

// CheckListeningIdentity reads availability of a current session independently
// of queue membership, retaining its original audio/plan identity.
func (s *Store) CheckListeningIdentity(ctx context.Context, identity models.ListeningQueueItem) (models.ListeningQueueItem, error) {
	if identity.Mode == "excerpt" {
		if len(identity.ExcerptID) > 200 || len(identity.SourceID) > 200 || len(identity.AudioSHA256) > 128 {
			return identity, ErrInvalidEditorialState
		}
		e, err := s.GetLearningExcerpt(ctx, identity.ExcerptID)
		if err != nil {
			return identity, err
		}
		if e.SourceType != identity.SourceType || e.SourceID != identity.SourceID || e.AudioSHA256 != identity.AudioSHA256 || identity.PlanID != "" || identity.PlanVersion != 0 {
			return identity, ErrInvalidEditorialState
		}
		snap, err := s.GetSourceSnapshot(ctx, e.SnapshotID)
		if err != nil {
			return identity, err
		}
		identity.SnapshotVersion = snap.ContentVersion
		identity.SnapshotID = e.SnapshotID
		identity.StartSeconds = e.StartSeconds
		identity.EndSeconds = e.EndSeconds
		identity.SegmentIDs = e.SegmentIDs
		_, err = s.CheckLearningExcerpt(ctx, e)
		identity.Available = err == nil
		if err != nil {
			identity.Reason = "补听区间的快照或冻结原音已失效"
		}
		return identity, nil
	}
	if identity.ExcerptID != "" {
		return identity, ErrInvalidEditorialState
	}
	if (identity.SourceType != models.SourceEpisode && identity.SourceType != models.SourceUpload) || identity.SourceID == "" || len(identity.SourceID) > 200 || len(identity.AudioSHA256) > 128 || len(identity.PlanID) > 200 || (identity.Mode != "original" && identity.Mode != "dj") || identity.PlanVersion < 0 || (identity.Mode == "original" && (identity.PlanID != "" || identity.PlanVersion != 0)) || (identity.Mode == "dj" && (identity.PlanID == "" || identity.PlanVersion < 1)) {
		return identity, ErrInvalidEditorialState
	}
	query := strings.Replace(listeningQueueRows, "FROM listening_queue_entries q", `FROM (SELECT '' AS id,? AS source_type,? AS source_id,? AS mode,? AS plan_id,? AS plan_version,? AS audio_sha256,'' AS title,0 AS position,'' AS invalid_reason,'' AS excerpt_id) q`, 1)
	return scanListeningQueueItem(s.DB.QueryRowContext(ctx, query, identity.SourceType, identity.SourceID, identity.Mode, identity.PlanID, identity.PlanVersion, identity.AudioSHA256))
}
