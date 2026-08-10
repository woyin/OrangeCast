// Package models 定义跨包共享的领域类型（领域词汇见 CONTEXT.md）。
//
// 涵盖：多态来源（SourceType：episode/upload）、处理任务与状态机（JobType/JobStatus）、
// 播客/单集/上传、证据音频、不可变产物版本、Purge、实例设置与 Citation/Reference
// 关系种类。本包不依赖任何其它 internal 包，是领域层的稳定地基。
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

// Episode 来自 RSS feed 的单集。可被选入队转为 Source；AudioURL 是外部链接，
// 真正持久化的是其 EvidenceAudio。ProcessingStatus 反映处理管线的阶段。
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

// Upload 用户主动上传的音频文件，与 Episode 同构但来源不同。
// 原始文件先落 tempDir/uploads/<id>，转码为标准化 EvidenceAudio 后删除（ADR-0005）。
type Upload struct {
	ID               string
	OriginalFilename string
	ContentType      string
	SizeBytes        int64
	DurationSeconds  *int
	ProcessingStatus EpisodeProcessingStatus
	CreatedAt        string
}

// Transcript 原始转录文本与其分段 JSON 的快照（已被 ArtifactVersion 替代为持久形态，
// 仅保留给仍按扁平表读取的旧路径）。新代码应优先使用 ArtifactVersion。
type Transcript struct {
	ID           string
	SourceType   SourceType
	SourceID     string
	Language     *string
	PlainText    string
	SegmentsJSON string
	CreatedAt    string
}

// Analysis 分析结果快照（已被 ArtifactVersion 替代为持久形态）。
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
	Status     string // ready | missing；missing 表示元数据已写但文件缺失（恢复/迁移用）
	CreatedAt  string
	UpdatedAt  string
}

// ProcessingJob 可恢复任务队列的单条任务（ADR-0006）：SQLite 持久化、租约 + 心跳，
// 进程重启后未完成的 running 任务会被重新认领。JobType 决定处理管线（转录/分析）。
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
	TranscriptionModel    *string
	AnalysisModel         *string
	HighlightModel        *string
	QAModel               *string
	TranscriptionProvider *string
	AnalysisProvider      *string
	HighlightProvider     *string
	QAProvider            *string
	GroqAPIKey            *string
	GroqBaseURL           *string
	OpenAIAPIKey          *string
	OpenAIBaseURL         *string
}

// RelationKind 显式区分一个 Segment 关联是 Citation 还是 Reference（ADR-0018 R1）。
//
// Citation：可逐字核验的强关系（CitedDerivative）。
// Reference：仅表示参考的弱关系（GeneratedDerivative），不声称忠实。
// 二者语义不同且不可互换；宿主实体类别与 RelationKind 必须配对正确，写入层校验。
type RelationKind string

const (
	// RelationCitation 可核验的强关系。
	RelationCitation RelationKind = "citation"
	// RelationReference 仅表示参考的弱关系，不声称忠实。
	RelationReference RelationKind = "reference"
)
