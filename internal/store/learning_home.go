package store

import "context"

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
