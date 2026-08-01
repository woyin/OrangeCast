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

// User 单例 Owner 凭据。实例只能被认领一次（ADR-0003）。
type User struct {
	ID           string
	Email        string
	PasswordHash string
	CreatedAt    string
}

// Podcast 播客订阅。单 Owner 实例内所有内容天然属于实例，不携带 user_id（ADR-0007）。
type Podcast struct {
	ID            string
	FeedURL       string
	Title         string
	Description   string
	ImageURL      string
	LastFetchedAt *string
	CreatedAt     string
}

type Episode struct {
	ID               string
	PodcastID        string
	GUID             string
	Title            string
	Description      string
	AudioURL         string
	DurationSeconds  *int
	PublishedAt      *string
	ProcessingStatus EpisodeProcessingStatus
	CreatedAt        string
}

type Upload struct {
	ID               string
	OriginalFilename string
	ContentType      string
	SizeBytes        int64
	DurationSeconds  *int
	ProcessingStatus EpisodeProcessingStatus
	CreatedAt        string
}

type Transcript struct {
	ID           string
	SourceType   SourceType
	SourceID     string
	Language     *string
	PlainText    string
	SegmentsJSON string
	CreatedAt    string
}

type Analysis struct {
	ID          string
	SourceType  SourceType
	SourceID    string
	Title       string
	Summary     string
	ContentJSON string
	CreatedAt   string
}

// EvidenceAudio 为每个 Source 长期保存、可独立播放的标准化音频（ADR-0005）。
// Citation 与播放器只依赖它，不依赖外部音频地址。
type EvidenceAudio struct {
	ID         string
	SourceType SourceType
	SourceID   string
	RelPath    string
	Format     string
	SizeBytes  int64
	SHA256     string
	Status     string // ready | missing
	CreatedAt  string
	UpdatedAt  string
}

type ProcessingJob struct {
	ID           string
	SourceType   SourceType
	SourceID     string
	JobType      JobType
	Status       JobStatus
	AttemptCount int
	LastError    *string
	LeaseUntil   *string
	HeartbeatAt  *string
	CreatedAt    string
	UpdatedAt    string
}

// ArtifactVersion 不可变产物版本（ADR-0011）：Transcript 或 KnowledgeCard。
// 重新处理不覆盖历史版本；Source 显式指向当前采用的版本。
type ArtifactVersion struct {
	ID            string
	SourceType    SourceType
	SourceID      string
	Kind          string // transcript | knowledge_card
	Version       int
	Provider      string
	Model         string
	PromptVersion string
	JobID         string
	Payload       string // JSON（TranscriptPayload 或 KnowledgeCard）
	CreatedAt     string
}

// Purge 一次可恢复的完整删除意图（ADR-0012）。
type Purge struct {
	ID         string
	SourceType SourceType
	SourceID   string
	Status     string // pending | done
	CreatedAt  string
}

// Settings 实例级设置（单例，id=1）。ADR-0009 移除全局 active_provider：
// Groq 是默认零成本 Provider；付费 Provider 仅按单次 ProcessingJob 尝试显式授权。
type Settings struct {
	TranscriptionModel *string
	AnalysisModel      *string
	QAModel            *string
}
