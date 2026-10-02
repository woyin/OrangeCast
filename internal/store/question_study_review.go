package store

import (
	"context"
	"encoding/json"

	"github.com/woyin/orangecast/internal/provider"
)

// CommitQuestionStudyReview Only a validated check can publish a response. Source permissions and stop
// controls are checked in the same transaction as the visible history write.
func (s *Store) CommitQuestionStudyReview(ctx context.Context, jobID string, in QuestionStudyJobInput, review provider.QuestionStudyReview) error {
	if in.Stage != "review" || in.Answer == nil {
		return ErrInvalidEditorialState
	}
	if err := provider.ValidateQuestionStudyAnswer(in.Scope, *in.Answer); err != nil {
		return err
	}
	rawReview, err := json.Marshal(review)
	if err != nil {
		return err
	}
	if _, err = provider.ParseQuestionStudyReview(*in.Answer, string(rawReview)); err != nil {
		return err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = checkRunControl(ctx, tx, jobID); err != nil {
		return err
	}
	if err = checkQuestionStudyDependencies(ctx, tx, in.TurnID, in.Config.Provider); err != nil {
		return err
	}
	if err = checkQuestionStudyScope(ctx, tx, in.Scope, in.Config.Provider); err != nil {
		return err
	}
	state, accepted := "blocked", ""
	if review.Verdict == "accept" {
		state = "accepted"
		raw, _ := json.Marshal(in.Answer)
		accepted = string(raw)
	} else if review.Verdict == "insufficient" {
		state = "insufficient"
	}
	result, err := tx.ExecContext(ctx, `UPDATE question_study_turns SET state=?,accepted_json=?,updated_at=datetime('now') WHERE id=? AND check_job_id=? AND purged=0 AND state!='accepted'`, state, accepted, in.TurnID, jobID)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return ErrConflict
	}
	if _, err = tx.ExecContext(ctx, `UPDATE processing_jobs SET result_json=?,result_state='complete' WHERE id=?`, string(rawReview), jobID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE question_study_sessions SET revision=revision+1,updated_at=datetime('now') WHERE id=(SELECT session_id FROM question_study_turns WHERE id=?)`, in.TurnID); err != nil {
		return err
	}
	return tx.Commit()
}
