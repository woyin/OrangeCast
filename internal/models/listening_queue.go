package models

// ListeningQueue is the Owner's versioned order, current entry and opt-in autoplay.
type ListeningQueue struct {
	Revision      int64                `json:"revision"`
	CurrentItemID string               `json:"current_item_id"`
	Autoplay      bool                 `json:"autoplay"`
	Items         []ListeningQueueItem `json:"items"`
}

// ListeningQueueItem binds original audio or an immutable DJ plan to a source.
// Unfrozen remote audio is visible; a known changed hash never silently resumes.
type ListeningQueueItem struct {
	ID          string     `json:"id"`
	SourceType  SourceType `json:"source_type"`
	SourceID    string     `json:"source_id"`
	Mode        string     `json:"mode"`
	PlanID      string     `json:"plan_id"`
	PlanVersion int        `json:"plan_version"`
	AudioSHA256 string     `json:"audio_sha256"`
	Title       string     `json:"title"`
	Position    int        `json:"position"`
	Available   bool       `json:"available"`
	Unfrozen    bool       `json:"unfrozen"`
	Reason      string     `json:"reason"`
}
