package models

// SourceType 多态来源抽象。
type SourceType string

const (
	SourceEpisode SourceType = "episode"
	SourceUpload  SourceType = "upload"
)

// JobType 处理任务类型。
type JobType string

const (
	JobTranscribe JobType = "transcribe"
	JobAnalyze    JobType = "analyze"
)

// JobStatus 任务状态机：queued → running → succeeded | failed
type JobStatus string

const (
	StatusQueued    JobStatus = "queued"
	StatusRunning   JobStatus = "running"
	StatusSucceeded JobStatus = "succeeded"
	StatusFailed    JobStatus = "failed"
)

// EpisodeProcessingStatus 单集处理状态：unprocessed → queued → transcribing → transcribed → analyzing → processed | failed
type EpisodeProcessingStatus string

const (
	StatusUnprocessed  EpisodeProcessingStatus = "unprocessed"
	StatusQueuedEp     EpisodeProcessingStatus = "queued"
	StatusTranscribing EpisodeProcessingStatus = "transcribing"
	StatusTranscribed  EpisodeProcessingStatus = "transcribed"
	StatusAnalyzing    EpisodeProcessingStatus = "analyzing"
	StatusProcessed    EpisodeProcessingStatus = "processed"
	StatusFailedEp     EpisodeProcessingStatus = "failed"
)

type User struct {
	ID           string
	Email        string
	PasswordHash string
	CreatedAt    string
}

type Podcast struct {
	ID            string
	UserID        string
	FeedURL       string
	Title         string
	Description   string
	ImageURL      string
	LastFetchedAt *string
	CreatedAt     string
}

type Episode struct {
	ID                string
	UserID            string
	PodcastID         string
	GUID              string
	Title             string
	Description       string
	AudioURL          string
	DurationSeconds   *int
	PublishedAt       *string
	ProcessingStatus  EpisodeProcessingStatus
	CreatedAt         string
}

type Upload struct {
	ID                string
	UserID            string
	OriginalFilename  string
	ContentType       string
	SizeBytes         int64
	DurationSeconds   *int
	ProcessingStatus  EpisodeProcessingStatus
	CreatedAt         string
}

type Transcript struct {
	ID            string
	UserID        string
	SourceType    SourceType
	SourceID      string
	Language      *string
	PlainText     string
	SegmentsJSON  string
	CreatedAt     string
}

type Analysis struct {
	ID           string
	UserID       string
	SourceType   SourceType
	SourceID     string
	Title        string
	Summary      string
	ContentJSON  string
	CreatedAt    string
}

type ProcessingJob struct {
	ID           string
	UserID       string
	SourceType   SourceType
	SourceID     string
	JobType      JobType
	Status       JobStatus
	AttemptCount int
	LastError    *string
	CreatedAt    string
	UpdatedAt    string
}

type Settings struct {
	UserID            string
	ActiveProvider    string // groq | openai
	TranscriptionModel *string
	AnalysisModel      *string
	QAModel            *string
}
