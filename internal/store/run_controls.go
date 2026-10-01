package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// ErrRunControlled blocks new calls and application, while preserving response facts.
var ErrRunControlled = errors.New("Owner已暂停方向或请求停止；原调用、响应和用量保留")

// LearningRunLanes lists durable learning categories shared by filtering and the control panel.
func LearningRunLanes() []string {
	return []string{"knowledge", "updates", "review", "voice", "study", "index", "export"}
}

func validRunLane(lane string) bool {
	if lane == "" {
		return true
	}
	for _, known := range LearningRunLanes() {
		if lane == known {
			return true
		}
	}
	return false
}

// RunControl is the Owner's persistent admission decision, independent of execution state.
type RunControl struct {
	Kind, Target, Reason, UpdatedAt string
	Paused                          bool
	Revision                        int
}

const runAllowedSQL = `stop_requested=0 AND NOT EXISTS(SELECT 1 FROM run_controls c WHERE c.paused=1 AND ((c.kind='lane' AND c.target=processing_jobs.run_lane) OR (c.kind='direction' AND c.target=processing_jobs.source_type||':'||processing_jobs.source_id)))`

func checkRunControl(ctx context.Context, q reviewReader, id string) error {
	var allowed bool
	err := q.QueryRowContext(ctx, `SELECT `+runAllowedSQL+` FROM processing_jobs WHERE id=?`, id).Scan(&allowed)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if !allowed {
		return ErrRunControlled
	}
	return nil
}

// CheckRunControl checks the current boundary without rewriting the frozen request.
func (s *Store) CheckRunControl(ctx context.Context, id string) error {
	return checkRunControl(ctx, s.DB, id)
}

// GetRunControl returns a virtual unpaused first revision for a valid new direction.
func (s *Store) GetRunControl(ctx context.Context, kind, target string) (RunControl, error) {
	c := RunControl{Kind: kind, Target: target, Revision: 1}
	if kind != "lane" && kind != "direction" {
		return c, ErrInvalidEditorialState
	}
	err := s.DB.QueryRowContext(ctx, `SELECT paused,revision,reason,updated_at FROM run_controls WHERE kind=? AND target=?`, kind, target).Scan(&c.Paused, &c.Revision, &c.Reason, &c.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) && kind == "direction" {
		var exists bool
		err = s.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM processing_jobs WHERE run_lane!='' AND source_type||':'||source_id=?)`, target).Scan(&exists)
		if err == nil && !exists {
			err = ErrNotFound
		}
		return c, err
	}
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return c, err
}

// ChangeRunControl atomically competes with claims and application, with exact replay and CAS.
func (s *Store) ChangeRunControl(ctx context.Context, kind, target, action, reason, request string, expected, priority int) error {
	if _, err := uuid.Parse(request); err != nil {
		return ErrInvalidEditorialState
	}
	reason = strings.TrimSpace(reason)
	if expected < 1 || reason == "" || len([]rune(reason)) > 1000 || priority < -10 || priority > 10 {
		return ErrInvalidEditorialState
	}
	if kind == "job" {
		if action != "stop" && action != "priority" && action != "resume_queue" {
			return ErrInvalidEditorialState
		}
	} else if kind == "lane" || kind == "direction" {
		if action != "pause" && action != "resume" {
			return ErrInvalidEditorialState
		}
	} else {
		return ErrInvalidEditorialState
	}
	raw, _ := json.Marshal([]any{kind, target, action, reason, expected, priority})
	hash := fmt.Sprintf("%x", sha256.Sum256(raw))
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var old string
	err = tx.QueryRowContext(ctx, `SELECT payload_hash FROM run_control_actions WHERE request_key=?`, request).Scan(&old)
	if err == nil {
		if old != hash {
			return ErrConflict
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if kind == "job" {
		var status, lane string
		var rev int
		var stopped bool
		if err = tx.QueryRowContext(ctx, `SELECT status,run_lane,control_revision,stop_requested FROM processing_jobs WHERE id=?`, target).Scan(&status, &lane, &rev, &stopped); errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		if lane == "" || rev != expected {
			return ErrConflict
		}
		if action == "priority" {
			if status != "queued" || stopped {
				return ErrConflict
			}
			_, err = tx.ExecContext(ctx, `UPDATE processing_jobs SET priority=?,control_revision=control_revision+1 WHERE id=?`, priority, target)
		} else if action == "resume_queue" {
			var remote bool
			if err = tx.QueryRowContext(ctx, `SELECT remote_call_started FROM processing_jobs WHERE id=?`, target).Scan(&remote); err != nil {
				return err
			}
			if status != "queued" || !stopped || remote {
				return ErrConflict
			}
			_, err = tx.ExecContext(ctx, `UPDATE processing_jobs SET stop_requested=0,control_revision=control_revision+1 WHERE id=?`, target)
		} else {
			if status != "queued" && status != "running" {
				return ErrConflict
			}
			_, err = tx.ExecContext(ctx, `UPDATE processing_jobs SET stop_requested=1,control_revision=control_revision+1 WHERE id=?`, target)
		}
	} else {
		if kind == "direction" {
			var exists bool
			if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM processing_jobs WHERE run_lane!='' AND source_type||':'||source_id=?)`, target).Scan(&exists); err != nil {
				return err
			}
			if !exists {
				return ErrNotFound
			}
		}
		var rev int
		err = tx.QueryRowContext(ctx, `SELECT revision FROM run_controls WHERE kind=? AND target=?`, kind, target).Scan(&rev)
		if errors.Is(err, sql.ErrNoRows) && kind == "direction" {
			rev = 1
			_, err = tx.ExecContext(ctx, `INSERT INTO run_controls(kind,target)VALUES(?,?)`, kind, target)
		}
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if rev != expected {
			return ErrConflict
		}
		_, err = tx.ExecContext(ctx, `UPDATE run_controls SET paused=?,reason=?,revision=revision+1,updated_at=datetime('now') WHERE kind=? AND target=?`, action == "pause", reason, kind, target)
	}
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO run_control_actions(request_key,kind,target,action,payload_hash,reason,revision)VALUES(?,?,?,?,?,?,?)`, request, kind, target, action, hash, reason, expected+1); err != nil {
		return err
	}
	return tx.Commit()
}

// RunRecord contains only recorded metadata; authenticated URLs and raw checkpoints stay private.
type RunRecord struct {
	UnmeasuredReceipts                                                                                           int
	ID, SourceType, SourceID, Lane, Status, Stage, Provider, Model, Version, CreatedAt, UpdatedAt, Error, Intent string
	Revision, Priority, InputUnits, OutputUnits, Receipts, UnknownReceipts                                       int
	Stopped, Paused, RemoteStarted, HasCheckpoint                                                                bool
	KnownCost                                                                                                    float64
	ResultState, BlockReason, NextAction                                                                         string
}

// ListRuns reads bounded metadata without loading frozen text or modifying execution.
func (s *Store) ListRuns(ctx context.Context, status, lane string, offset int) ([]RunRecord, bool, error) {
	if offset < 0 || offset > 1000000 {
		return nil, false, ErrInvalidEditorialState
	}
	if !validRunLane(lane) {
		return nil, false, ErrInvalidEditorialState
	}
	switch status {
	case "", "queued", "running", "failed", "succeeded", "paused", "stopped":
	default:
		return nil, false, ErrInvalidEditorialState
	}
	filter := `run_lane!=''`
	args := []any{}
	if lane != "" {
		filter += ` AND run_lane=?`
		args = append(args, lane)
	}
	switch status {
	case "paused":
		filter += ` AND NOT (` + runAllowedSQL + `) AND stop_requested=0`
	case "stopped":
		filter += ` AND stop_requested=1`
	case "":
	default:
		filter += ` AND status=?`
		args = append(args, status)
	}
	args = append(args, 51, offset)
	rows, err := s.DB.QueryContext(ctx, `SELECT id,source_type,source_id,run_lane,status,COALESCE(json_extract(CASE WHEN json_valid(input_snapshot_json) THEN input_snapshot_json ELSE '{}' END,'$.stage'),job_type),COALESCE(configured_provider,''),COALESCE(configured_model,''),COALESCE(config_version,''),created_at,updated_at,COALESCE(last_error,''),COALESCE(intent_id,''),control_revision,priority,stop_requested,remote_call_started,checkpoint_json!='',result_state,NOT (`+runAllowedSQL+`) AND stop_requested=0 FROM processing_jobs WHERE `+filter+` ORDER BY created_at DESC,id DESC LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, false, err
	}
	var out []RunRecord
	for rows.Next() {
		var v RunRecord
		if err = rows.Scan(&v.ID, &v.SourceType, &v.SourceID, &v.Lane, &v.Status, &v.Stage, &v.Provider, &v.Model, &v.Version, &v.CreatedAt, &v.UpdatedAt, &v.Error, &v.Intent, &v.Revision, &v.Priority, &v.Stopped, &v.RemoteStarted, &v.HasCheckpoint, &v.ResultState, &v.Paused); err != nil {
			rows.Close()
			return nil, false, err
		}
		out = append(out, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, false, err
	}
	next := len(out) > 50
	if next {
		out = out[:50]
	}
	for i := range out {
		v := &out[i]
		if err = s.DB.QueryRowContext(ctx, `SELECT COALESCE(SUM(input_units),0),COALESCE(SUM(output_units),0),COALESCE(SUM(estimated_cost),0),COUNT(*),COALESCE(SUM(estimated_cost IS NULL),0),COALESCE(SUM(units_known=0),0) FROM usage_records WHERE attempt_id=? OR attempt_id=?`, v.ID, v.ID+":response").Scan(&v.InputUnits, &v.OutputUnits, &v.KnownCost, &v.Receipts, &v.UnknownReceipts, &v.UnmeasuredReceipts); err != nil {
			return nil, false, err
		}
		v.NextAction = "等待现有队列领取与资格检查"
		if v.Status == "succeeded" {
			v.NextAction = "完成；新阶段由独立任务记录"
		}
		if v.Status == "failed" {
			v.NextAction = "核对错误后，从原任务页面明确重试"
			v.BlockReason = v.Error
		}
		if v.Status == "running" {
			v.NextAction = "当前阶段执行中；等待响应保存"
		}
		if v.Stopped {
			v.BlockReason = "Owner已请求停止"
			v.NextAction = "保留原调用事实；不自动应用迟到响应，需明确核对恢复"
		} else if v.Paused {
			v.BlockReason = "Owner已暂停该方向或任务类别"
			v.NextAction = "恢复只解除暂停，不自动重放未知调用"
		}
		if v.ResultState == "unknown" {
			v.BlockReason = "远端结果未知"
			v.NextAction = "核对远端调用后明确重试，可能再次计费"
		}
	}
	return out, next, nil
}

// RunCostSummary distinguishes a known subtotal from unknown calls, across all recorded personal runs.
type RunCostSummary struct {
	KnownCost    float64
	UnknownTasks int
}

// GetRunCostSummary does not turn missing receipts or prices into zero cost.
func (s *Store) GetRunCostSummary(ctx context.Context) (RunCostSummary, error) {
	var v RunCostSummary
	// Keep receipts visible after SourcePurge removes a derived job. Source ASR
	// shares the audio ledger; old unlinked receipts remain explicitly unknown.
	scope := `(u.operation LIKE 'knowledge_article_%' OR u.operation IN('weekly_review','transcription') OR EXISTS(SELECT 1 FROM processing_jobs j WHERE j.run_lane!='' AND (j.id=u.attempt_id OR j.id||':response'=u.attempt_id)))`
	err := s.DB.QueryRowContext(ctx, `SELECT COALESCE(SUM(u.estimated_cost),0) FROM usage_records u WHERE `+scope).Scan(&v.KnownCost)
	if err != nil {
		return v, err
	}
	err = s.DB.QueryRowContext(ctx, `SELECT count(*) FROM (
 SELECT CASE WHEN u.attempt_id='' THEN 'legacy:'||u.id WHEN u.attempt_id LIKE '%:response' THEN substr(u.attempt_id,1,length(u.attempt_id)-9) ELSE u.attempt_id END AS identity FROM usage_records u WHERE u.estimated_cost IS NULL AND `+scope+`
 UNION SELECT j.id FROM processing_jobs j WHERE j.run_lane!='' AND j.remote_call_started=1 AND NOT EXISTS(SELECT 1 FROM usage_records u WHERE u.attempt_id=j.id OR u.attempt_id=j.id||':response')
 )`).Scan(&v.UnknownTasks)
	return v, err
}

// RunControlAction is an immutable Owner decision with its target revision.
type RunControlAction struct {
	Kind, Target, Action, Reason, CreatedAt string
	Revision                                int
}

// ListRunControlActions reads bounded actions for a task and its direction.
func (s *Store) ListRunControlActions(ctx context.Context, id, direction string, offset int) ([]RunControlAction, bool, error) {
	if offset < 0 || offset > 1000000 {
		return nil, false, ErrInvalidEditorialState
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT kind,target,action,reason,revision,created_at FROM run_control_actions WHERE (kind='job' AND target=?) OR(kind='direction' AND target=?) ORDER BY created_at DESC,rowid DESC LIMIT 51 OFFSET ?`, id, direction, offset)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	var out []RunControlAction
	for rows.Next() {
		var v RunControlAction
		if err = rows.Scan(&v.Kind, &v.Target, &v.Action, &v.Reason, &v.Revision, &v.CreatedAt); err != nil {
			return nil, false, err
		}
		out = append(out, v)
	}
	next := len(out) > 50
	if next {
		out = out[:50]
	}
	return out, next, rows.Err()
}

// RunUsage keeps supplier units and unknown prices separate.
type RunUsage struct {
	Operation, Provider, Model, UnitKind string
	InputUnits, OutputUnits              int
	AudioSeconds, CostCents              float64
	CostKnown                            bool
	UnitsKnown                           bool
}

// ListRunUsage returns recorded receipts for the exact paid identity of a task.
func (s *Store) ListRunUsage(ctx context.Context, id string) ([]RunUsage, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT operation,provider,model,input_units,output_units,COALESCE(unit_kind,'text_tokens'),COALESCE(audio_seconds,0),estimated_cost,units_known FROM usage_records WHERE attempt_id=? OR attempt_id=? ORDER BY created_at,id LIMIT 100`, id, id+":response")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RunUsage
	for rows.Next() {
		var v RunUsage
		var cost sql.NullFloat64
		if err = rows.Scan(&v.Operation, &v.Provider, &v.Model, &v.InputUnits, &v.OutputUnits, &v.UnitKind, &v.AudioSeconds, &cost, &v.UnitsKnown); err != nil {
			return nil, err
		}
		v.CostCents, v.CostKnown = cost.Float64, cost.Valid
		out = append(out, v)
	}
	return out, rows.Err()
}
