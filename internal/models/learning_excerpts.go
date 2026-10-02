package models

// LearningExcerpt freezes a contiguous transcript window and its original audio.
// Positions remain absolute source seconds, including notes and saved progress.
type LearningExcerpt struct {
	ID           string     `json:"id"`
	SourceType   SourceType `json:"source_type"`
	SourceID     string     `json:"source_id"`
	SnapshotID   string     `json:"snapshot_id"`
	AudioSHA256  string     `json:"audio_sha256"`
	SegmentIDs   []string   `json:"segment_ids"`
	StartSeconds float64    `json:"start_seconds"`
	EndSeconds   float64    `json:"end_seconds"`
	CreatedAt    string     `json:"created_at"`
}
