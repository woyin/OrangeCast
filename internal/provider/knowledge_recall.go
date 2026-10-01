package provider

// KnowledgeRecallCandidate is a local program record, never a model verdict.
// State distinguishes admitted, skipped after reading and not_read metadata.
type KnowledgeRecallCandidate struct {
	MaterialID  string  `json:"material_id"`
	SourceType  string  `json:"source_type"`
	SourceID    string  `json:"source_id"`
	Title       string  `json:"title"`
	State       string  `json:"state"`
	Reason      string  `json:"reason"`
	LexicalClue string  `json:"lexical_clue,omitempty"`
	Rank        float64 `json:"rank"`
}

// KnowledgeRecallCoverage prevents bounded search from implying library exhaustion.
type KnowledgeRecallCoverage struct {
	Method        string `json:"method"`
	TotalMatches  int    `json:"total_matches"`
	MetadataCount int    `json:"metadata_count"`
	ReadCount     int    `json:"read_count"`
	AdmittedCount int    `json:"admitted_count"`
	LimitReached  bool   `json:"limit_reached"`
	Reason        string `json:"reason"`
}

// KnowledgeEvidenceSegment carries complete cited text and real position.
type KnowledgeEvidenceSegment struct {
	SegmentID string  `json:"segment_id"`
	Text      string  `json:"text"`
	Position  float64 `json:"position"`
}
