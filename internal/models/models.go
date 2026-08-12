// Package models 定义跨包共享的领域类型（领域词汇见 CONTEXT.md）。
//
// 涵盖：多态来源（SourceType：episode/upload）、处理任务与状态机（JobType/JobStatus）、
// 播客/单集/上传、证据音频、不可变产物版本、Purge、实例设置与 Citation/Reference
// 关系种类。本包不依赖任何其它 internal 包，是领域层的稳定地基。
package models

// SourceType 多态来源抽象。
type SourceType string

const (
	// SourceEpisode 来源类型：播客单集。
	SourceEpisode SourceType = "episode"
	// SourceUpload 来源类型：手动上传。
	SourceUpload SourceType = "upload"
)

// JobType 处理任务类型。
type JobType string

const (
	// JobTranscribe 处理任务类型：转录。
	JobTranscribe JobType = "transcribe"
	// JobAnalyze 处理任务类型：分析生成知识卡片。
	JobAnalyze JobType = "analyze"
)

// JobStatus 任务状态机：queued → running → succeeded | failed
type JobStatus string

const (
	// StatusQueued 任务状态：排队中。
	StatusQueued JobStatus = "queued"
	// StatusRunning 任务状态：处理中。
	StatusRunning JobStatus = "running"
	// StatusSucceeded 任务状态：成功。
	StatusSucceeded JobStatus = "succeeded"
	// StatusFailed 任务状态：失败。
	StatusFailed JobStatus = "failed"
)

// EpisodeProcessingStatus 单集处理状态：unprocessed → queued → transcribing → transcribed → analyzing → processed | failed
type EpisodeProcessingStatus string

const (
	// StatusUnprocessed 单集处理状态：未处理。
	StatusUnprocessed EpisodeProcessingStatus = "unprocessed"
	// StatusQueuedEp 单集处理状态：已入队。
	StatusQueuedEp EpisodeProcessingStatus = "queued"
	// StatusTranscribing 单集处理状态：转录中。
	StatusTranscribing EpisodeProcessingStatus = "transcribing"
	// StatusTranscribed 单集处理状态：已转录，待分析。
	StatusTranscribed EpisodeProcessingStatus = "transcribed"
	// StatusAnalyzing 单集处理状态：分析中。
	StatusAnalyzing EpisodeProcessingStatus = "analyzing"
	// StatusProcessed 单集处理状态：处理完成。
	StatusProcessed EpisodeProcessingStatus = "processed"
	// StatusFailedEp 单集处理状态：失败。
	StatusFailedEp EpisodeProcessingStatus = "failed"
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

// ModelDataPolicy 限制一个 Source 的内容可以发送给哪些 AI Provider。
// 它独立于 EditorialProfile 的 SourceScope：前者约束数据流向，后者约束发布用途。
type ModelDataPolicy string

const (
	// ModelDataExternalAllowed 允许发送到已配置的外部 Provider。
	ModelDataExternalAllowed ModelDataPolicy = "external_allowed"
	// ModelDataApprovedProvidersOnly 仅允许发送到 Owner 明确批准的 Provider。
	ModelDataApprovedProvidersOnly ModelDataPolicy = "approved_providers_only"
	// ModelDataLocalOnly 禁止将内容发送到外部 Provider。
	ModelDataLocalOnly ModelDataPolicy = "local_only"
)

// EditorialProfile 一个内容品牌的长期编辑约束。
type EditorialProfile struct {
	ID                 string
	Name               string
	TargetAudience     string
	Voice              string
	StyleGuide         string
	SourceAttribution  string
	MonthlyBudgetCents *int64
	CreatedAt          string
	UpdatedAt          string
}

// ArticleProposal 是 Scout 为某个 EditorialProfile 发现的候选选题。
type ArticleProposal struct {
	ID                 string
	EditorialProfileID string
	Kind               string // fresh | evergreen | follow_up
	Status             string // proposed | accepted | parked | rejected | merged
	Title              string
	Thesis             string
	Audience           string
	Rationale          string
	CandidateKeyPoints string // JSON array
	CreatedAt          string
	UpdatedAt          string
}

// ArticleBrief 是 Owner 审核后才可进入写作的选材和结构契约。
type ArticleBrief struct {
	ID           string
	ProposalID   string
	Status       string // draft | confirmed | superseded
	Thesis       string
	Audience     string
	Outline      string
	MaterialPlan string // JSON
	ConflictPlan string // JSON
	Style        string
	TargetLength *int
	ConfirmedAt  *string
	CreatedAt    string
	UpdatedAt    string
}

// ArticleDraft 是持续编辑对象，当前版本由 CurrentRevisionID 指向。
type ArticleDraft struct {
	ID                 string
	EditorialProfileID string
	BriefID            string
	Title              string
	CurrentRevisionID  *string
	Status             string // drafting | reviewing | ready | blocked | archived
	CreatedAt          string
	UpdatedAt          string
}

// ArticleRevision 是一篇文章某时刻的不可变内容快照。
type ArticleRevision struct {
	ID            string
	DraftID       string
	Version       int
	Title         string
	Markdown      string
	Origin        string // writer | evidence_reviewer | style_editor | owner | ai_edit
	Provider      *string
	Model         *string
	PromptVersion *string
	CostCents     *int64
	CreatedAt     string
}

// EvidenceMapKind 表示文章表达和素材之间的语义关系。
type EvidenceMapKind string

const (
	// EvidenceQuoted 逐字引语，必须精确匹配原文。
	EvidenceQuoted EvidenceMapKind = "quoted"
	// EvidenceParaphrased 忠实转述一个 KeyPoint。
	EvidenceParaphrased EvidenceMapKind = "paraphrased"
	// EvidenceSynthesized 基于多个 KeyPoint 的生成性综合。
	EvidenceSynthesized EvidenceMapKind = "synthesized"
	// EvidenceRhetorical 不含事实主张的修辞内容。
	EvidenceRhetorical EvidenceMapKind = "rhetorical"
)

// EvidenceMap 将一个 ArticleRevision 中的表达与其 KeyPoint 依据关联。
type EvidenceMap struct {
	ID          string
	RevisionID  string
	Kind        EvidenceMapKind
	Excerpt     string
	KeyPointIDs string // JSON array
	CreatedAt   string
}

// ArticleReview 是某个 Revision 的独立审校结果。
type ArticleReview struct {
	ID         string
	RevisionID string
	Kind       string // evidence | style
	Status     string // passed | failed | advisory
	IssuesJSON string
	Provider   *string
	Model      *string
	CreatedAt  string
}

// EditorialFeedback 是 Owner 对内容生产对象的显式编辑判断。
type EditorialFeedback struct {
	ID                 string
	EditorialProfileID string
	EntityType         string
	EntityID           string
	Action             string
	Reason             string
	DetailsJSON        string
	CreatedAt          string
}

// KeyPointOrigin identifies whether a KeyPoint was generated or curated by the Owner.
type KeyPointOrigin string

const (
	// KeyPointAutomatic is generated from a KnowledgeCard.
	KeyPointAutomatic KeyPointOrigin = "automatic"
	// KeyPointManual is selected and written directly by the Owner.
	KeyPointManual KeyPointOrigin = "manual"
	// KeyPointEdited is an Owner-edited derivative of an automatic KeyPoint.
	KeyPointEdited KeyPointOrigin = "edited"
)

// KeyPointProductionStatus tracks a reusable idea through the content-production inbox.
type KeyPointProductionStatus string

const (
	// KeyPointInbox has not yet been curated for production.
	KeyPointInbox KeyPointProductionStatus = "inbox"
	// KeyPointShortlisted is suitable for a future proposal.
	KeyPointShortlisted KeyPointProductionStatus = "shortlisted"
	// KeyPointUsed has participated in an article workflow.
	KeyPointUsed KeyPointProductionStatus = "used"
	// KeyPointDismissed is intentionally excluded from content production.
	KeyPointDismissed KeyPointProductionStatus = "dismissed"
)
