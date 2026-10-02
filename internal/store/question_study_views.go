package store

import "context"

// ListQuestionStudySessions These bounded projections never return unreviewed model bodies.
func (s *Store) ListQuestionStudySessions(ctx context.Context, questionID string) ([]QuestionStudySession, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,question_id,revision,created_at,updated_at FROM question_study_sessions WHERE question_id=? ORDER BY updated_at DESC,id LIMIT 50`, questionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []QuestionStudySession{}
	for rows.Next() {
		var v QuestionStudySession
		if err = rows.Scan(&v.ID, &v.QuestionID, &v.Revision, &v.CreatedAt, &v.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// QuestionStudyTurnStatuses 返回不含未检查正文的有界轮状态。
func (s *Store) QuestionStudyTurnStatuses(ctx context.Context, sessionID string) ([]QuestionStudyTurn, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,ordinal,state,COALESCE(generation_job_id,''),COALESCE(check_job_id,'') FROM question_study_turns WHERE session_id=? AND purged=0 ORDER BY ordinal LIMIT 12`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []QuestionStudyTurn{}
	for rows.Next() {
		var v QuestionStudyTurn
		if err = rows.Scan(&v.ID, &v.Ordinal, &v.State, &v.GenerationJobID, &v.CheckJobID); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
