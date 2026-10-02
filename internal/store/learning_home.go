package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/google/uuid"
)

// LearningPreferences records explicit choices; projection reads never create a session.
type LearningPreferences struct {
	CurrentQuestionID string
	ReflectionPrompt  bool
	Revision          int
}

// GetLearningPreferences reads the singleton installed by the migration.
func (s *Store) GetLearningPreferences(ctx context.Context) (LearningPreferences, error) {
	var p LearningPreferences
	err := s.DB.QueryRowContext(ctx, `SELECT COALESCE(current_question_id,''),reflection_prompt,revision FROM learning_preferences WHERE id=1`).Scan(&p.CurrentQuestionID, &p.ReflectionPrompt, &p.Revision)
	return p, err
}

// SaveLearningPreferences uses payload-bound replay and CAS, including an active question check.
func (s *Store) SaveLearningPreferences(ctx context.Context, p LearningPreferences, key string) error {
	if _, err := uuid.Parse(key); err != nil || p.Revision < 1 {
		return ErrInvalidEditorialState
	}
	raw, _ := json.Marshal(p)
	hash := fmt.Sprintf("%x", sha256.Sum256(raw))
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var previous string
	err = tx.QueryRowContext(ctx, `SELECT payload_hash FROM learning_preference_actions WHERE request_key=?`, key).Scan(&previous)
	if err == nil {
		if previous != hash {
			return ErrConflict
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if p.CurrentQuestionID != "" {
		q, e := scanLearningQuestion(tx.QueryRowContext(ctx, `SELECT `+questionColumns+` FROM learning_questions WHERE id=?`, p.CurrentQuestionID))
		if e != nil {
			return e
		}
		if q.Status != "active" {
			return ErrConflict
		}
	}
	res, err := tx.ExecContext(ctx, `UPDATE learning_preferences SET current_question_id=NULLIF(?,''),reflection_prompt=?,revision=revision+1,updated_at=datetime('now') WHERE id=1 AND revision=?`, p.CurrentQuestionID, p.ReflectionPrompt, p.Revision)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrConflict
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO learning_preference_actions(request_key,payload_hash)VALUES(?,?)`, key, hash); err != nil {
		return err
	}
	return tx.Commit()
}

// LearningAction is a deterministic next step, not a score of Owner mastery.
type LearningAction struct{ Kind, Title, Reason, Href string }

// LearningNextActions returns at most three read-only actions in explicit priority order.
func (s *Store) LearningNextActions(ctx context.Context, now time.Time) ([]LearningAction, error) {
	out := []LearningAction{}
	active, err := s.ActiveReviewSession(ctx)
	if err != nil {
		return nil, err
	}
	if active != nil {
		out = append(out, LearningAction{"review", "继续短回顾", "有尚未完成的明确会话", "/review/daily"})
	} else {
		due, e := s.DueReviewCount(ctx, now)
		if e != nil {
			return nil, e
		}
		if due > 0 {
			out = append(out, LearningAction{"review", "回答今日到期题目", fmt.Sprintf("今日到期%d题", due), "/review/daily"})
		}
	}
	p, err := s.GetLearningPreferences(ctx)
	if err != nil {
		return nil, err
	}
	var reflectionID string
	err = s.DB.QueryRowContext(ctx, `SELECT id FROM listening_reflections WHERE state='draft' AND expires_at>datetime('now') ORDER BY updated_at DESC,id LIMIT 1`).Scan(&reflectionID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if err == nil {
		out = append(out, LearningAction{"reflection", "继续听后整理", "有尚未保存的私人整理草稿", "/listening-reflections/" + url.PathEscape(reflectionID)})
	}
	if p.CurrentQuestionID != "" {
		q, e := s.GetLearningQuestion(ctx, p.CurrentQuestionID)
		if e != nil && !errors.Is(e, ErrNotFound) {
			return nil, e
		}
		if e == nil && q.Status == "active" {
			gaps, e := s.ListEvidenceGaps(ctx, "question", q.ID)
			if e != nil {
				return nil, e
			}
			count := 0
			origins := map[string]bool{}
			for _, g := range gaps {
				if g.State == "pending" || g.State == "insufficient" {
					count++
					origins[g.Origin] = true
				}
			}
			if count > 0 {
				provenance := "我的记录"
				if origins["model"] {
					provenance = "含模型建议，需我判断"
				} else if origins["program"] {
					provenance = "含程序记录的材料限制"
				}
				out = append(out, LearningAction{"gap", "补充当前问题的材料", fmt.Sprintf("当前范围有%d项待处理缺口；%s，未检验全库", count, provenance), "/questions/" + url.PathEscape(q.ID) + "/gaps"})
			} else {
				out = append(out, LearningAction{"question", q.Body, "你选定的当前学习问题", "/questions/" + url.PathEscape(q.ID)})
			}
		}
	}
	listening, err := s.RecentListening(ctx)
	if err != nil {
		return nil, err
	}
	if len(listening) > 0 {
		v := listening[0]
		href := "/sources/" + url.PathEscape(v.SourceType) + "/" + url.PathEscape(v.SourceID)
		if v.Mode == "dj" {
			href += "/dj"
		}
		out = append(out, LearningAction{"listen", "继续听：" + v.Title, "最近一次收听；保留原音或DJ进度", href})
	}
	if len(out) < 3 {
		var id, title string
		e := s.DB.QueryRowContext(ctx, `SELECT id,title FROM knowledge_articles WHERE status IN('needs_review','failed','insufficient') ORDER BY updated_at DESC,id LIMIT 1`).Scan(&id, &title)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return nil, e
		}
		if e == nil {
			if title == "" {
				title = "文章发现"
			}
			out = append(out, LearningAction{"article", "处理：" + title, "有待审或需要处理的文章", "/knowledge-articles/" + url.PathEscape(id)})
		}
	}
	if len(out) == 0 {
		out = append(out, LearningAction{"start", "选择一集开始听", "收听时可以留一句自己的理解", "/podcasts"}, LearningAction{"question", "确定一个想弄懂的问题", "暂时没有材料也可以先记下目标", "/questions"})
	}
	if len(out) > 3 {
		out = out[:3]
	}
	return out, nil
}

// ListeningResume identifies a saved mode and its human-readable source.
type ListeningResume struct {
	SourceType, SourceID, Title, Mode, UpdatedAt string
	Position                                     float64
}

// RecentListening returns resumable rows independently for original and DJ modes.
func (s *Store) RecentListening(ctx context.Context) ([]ListeningResume, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT p.source_type,p.source_id,COALESCE(e.title,u.original_filename,p.source_id),p.mode,p.item_offset_seconds,p.updated_at FROM listening_progress p LEFT JOIN episodes e ON p.source_type='episode' AND e.id=p.source_id LEFT JOIN uploads u ON p.source_type='upload' AND u.id=p.source_id ORDER BY p.updated_at DESC LIMIT 5`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ListeningResume
	for rows.Next() {
		var v ListeningResume
		if err := rows.Scan(&v.SourceType, &v.SourceID, &v.Title, &v.Mode, &v.Position, &v.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
