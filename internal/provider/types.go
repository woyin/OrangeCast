package provider

// Segment 转录时间戳段，带稳定标识（ADR-0008）。
// Citation 必须引用 Segment.ID，时间范围由程序从 Segment 解析，不能由 AI 估算。
type Segment struct {
	ID    string  `json:"id"`
	Start float64 `json:"start"` // 秒
	End   float64 `json:"end"`
	Text  string  `json:"text"`
}

// TranscriptResult 转录输出：纯文本 + 分段（调用方负责分配稳定 Segment.ID）。
type TranscriptResult struct {
	Language string    `json:"language"`
	Text     string    `json:"text"`
	Segments []Segment `json:"segments"`
}

// CitedText 带 Citation 的文本（摘要等）。
type CitedText struct {
	Text      string   `json:"text"`
	Citations []string `json:"citations"` // Segment ID 列表
}

// KeyPoint 带 Citation 的要点。
type KeyPoint struct {
	Content     string   `json:"content"`
	Description string   `json:"description"`
	Citations   []string `json:"citations"`
}

// Chapter 带 Citation 的章节。时间范围由程序从被引用 Segment 解析。
type Chapter struct {
	Title     string   `json:"title"`
	Gist      string   `json:"gist"`
	Citations []string `json:"citations"`
}

// Quote 带 Citation 的金句。金句文本必须逐字来自被引用 Segment（程序校验）。
type Quote struct {
	Text      string   `json:"text"`
	Citations []string `json:"citations"`
}

// KnowledgeCard AI 分析的结构化产物（Evidence-first，ADR-0008）。
// 所有内容项都携带 Citation；无 Citation 的项在保存前被校验/省略/拒绝。
type KnowledgeCard struct {
	Title              string     `json:"title"`
	Summary            CitedText  `json:"summary"`
	KeyPoints          []KeyPoint `json:"keyPoints"`
	Chapters           []Chapter  `json:"chapters"`
	Quotes             []Quote    `json:"quotes"`
	Tags               []string   `json:"tags"`
	SuggestedQuestions []string   `json:"suggestedQuestions"`
}

// QAResult 问答输出。引用来自实际检索到的 Segment（Phase 7 收紧：无可靠 Citation 时拒答）。
type QAResult struct {
	Answer  string   `json:"answer"`
	Sources []Source `json:"sources"`
}

// Source 引用来源（转录片段 + 时间戳）。
type Source struct {
	SegmentID string  `json:"segmentId,omitempty"`
	Content   string  `json:"content"`
	Start     float64 `json:"start"`
	End       float64 `json:"end"`
}

// TranscriptionProvider 音频转录接口。
type TranscriptionProvider interface {
	Transcribe(filePath string) (*TranscriptResult, error)
	Name() string
}

// AnalysisProvider 内容分析接口，生成 KnowledgeCard。
// segments 提供稳定 Segment.ID，模型必须用 ID 表达 Citation（不能自行估算时间戳）。
type AnalysisProvider interface {
	Analyze(transcript string, segments []Segment) (*KnowledgeCard, error)
	Name() string
}

// QAProvider 单期问答接口（RAG：基于检索到的片段回答并返回引用）。
type QAProvider interface {
	Answer(question string, segments []Segment) (*QAResult, error)
	Name() string
}

// ArticleMaterial is one approved KeyPoint available to the Writer. It carries no model-only facts.
type ArticleMaterial struct {
	KeyPointID  string   `json:"keyPointId"`
	SourceID    string   `json:"sourceId"`
	SourceTitle string   `json:"sourceTitle"`
	Content     string   `json:"content"`
	Description string   `json:"description"`
	Citations   []string `json:"citations"`
}

// ArticleWritingRequest is the confirmed Brief plus its explicitly selected evidence material.
type ArticleWritingRequest struct {
	Title             string            `json:"title"`
	Thesis            string            `json:"thesis"`
	Audience          string            `json:"audience"`
	Outline           string            `json:"outline"`
	Style             string            `json:"style"`
	TargetLength      *int              `json:"targetLength,omitempty"`
	SourceAttribution string            `json:"sourceAttribution"`
	Materials         []ArticleMaterial `json:"materials"`
}

// ArticleEvidence maps one generated excerpt to approved KeyPoint IDs.
type ArticleEvidence struct {
	Kind        string   `json:"kind"`
	Excerpt     string   `json:"excerpt"`
	KeyPointIDs []string `json:"keyPointIds"`
}

// ArticleWritingResult contains a Markdown draft and its required semantic evidence map.
type ArticleWritingResult struct {
	Title        string            `json:"title"`
	Markdown     string            `json:"markdown"`
	EvidenceMaps []ArticleEvidence `json:"evidenceMaps"`
}

// ArticleWriterProvider creates one evidence-mapped article from an Owner-confirmed brief.
type ArticleWriterProvider interface {
	WriteArticle(request ArticleWritingRequest) (*ArticleWritingResult, error)
	Name() string
}

// ScoutTheme is a confirmed editorial topic with only its scoped, usable evidence material.
type ScoutTheme struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Materials   []ArticleMaterial `json:"materials"`
}

// ScoutRequest supplies a profile's confirmed themes to the topic-discovery role.
type ScoutRequest struct {
	Audience string       `json:"audience"`
	Voice    string       `json:"voice"`
	Themes   []ScoutTheme `json:"themes"`
}

// ScoutProposal is a candidate topic that remains proposed until the Owner accepts it.
type ScoutProposal struct {
	Kind                 string   `json:"kind"`
	Title                string   `json:"title"`
	Thesis               string   `json:"thesis"`
	Audience             string   `json:"audience"`
	Rationale            string   `json:"rationale"`
	CandidateKeyPointIDs []string `json:"candidateKeyPointIds"`
}

// ScoutResult is deliberately separate from persisted ArticleProposal to validate generated material IDs first.
type ScoutResult struct {
	Proposals []ScoutProposal `json:"proposals"`
}

// ScoutProvider discovers candidate articles from confirmed cross-episode themes.
type ScoutProvider interface {
	Scout(request ScoutRequest) (*ScoutResult, error)
	Name() string
}

// EvidenceReviewItem is one semantic claim and its approved source material.
type EvidenceReviewItem struct {
	Kind      string            `json:"kind"`
	Excerpt   string            `json:"excerpt"`
	Materials []ArticleMaterial `json:"materials"`
}

// EvidenceReviewRequest is an exact revision with resolved evidence mappings.
type EvidenceReviewRequest struct {
	Title    string               `json:"title"`
	Markdown string               `json:"markdown"`
	Items    []EvidenceReviewItem `json:"items"`
}

// EvidenceReviewResult is an independent evidence-gate decision for one immutable revision.
type EvidenceReviewResult struct {
	Status string   `json:"status"`
	Issues []string `json:"issues"`
}

// EvidenceReviewerProvider reviews mappings independently of Writer.
type EvidenceReviewerProvider interface {
	ReviewEvidence(request EvidenceReviewRequest) (*EvidenceReviewResult, error)
	Name() string
}

// ProviderBundle 一个 provider 的全套实现。
type ProviderBundle struct {
	Transcription    TranscriptionProvider
	Analysis         AnalysisProvider
	QA               QAProvider
	Writer           ArticleWriterProvider
	Scout            ScoutProvider
	EvidenceReviewer EvidenceReviewerProvider
	Highlight        HighlightProvider
	Paraphrase       ParaphraseProvider
	StudyChat        StudyChatProvider
	RefChecker       ReferenceChecker
	Narration        NarrationProvider
}

// Highlight AI 判断的"最值得听"的连续音频区间（ADR-0016）。
// Citation 是一组 Segment ID 的集合，程序取 min(start)–max(end) 算时间范围。
type Highlight struct {
	ID        string   `json:"id"`        // 稳定 ID（Citation 集合的 hash，刷新后保持关联 Gist/Narration，ADR-0019）
	Gist      string   `json:"gist"`      // AI 生成的"为什么这段值得听"说明（非逐字原文）
	Citations []string `json:"citations"` // Segment ID 列表
}

// HighlightSet 一个 Source 的全部高光片段。
type HighlightSet struct {
	Highlights []Highlight `json:"highlights"`
}

// ParaphraseResult 复述讲解输出（GeneratedDerivative，ADR-0018）。
// Text 是 AI 重新组织的讲解（非逐字原文），允许类比/举例/拆解。
// ReferenceSegmentIDs 是它所参考的 Segment ID（由调用方传入，AI 不可自行编造时间）。
type ParaphraseResult struct {
	Text                string   `json:"text"`
	ReferenceSegmentIDs []string `json:"referenceSegmentIds"`
}

// ParaphraseProvider 复述讲解接口（GeneratedDerivative，ADR-0018 R2）。
// 基于给定参考 Segment 重讲 Owner 未理解的部分；输出明确标注为 AI 生成、非原文。
// 它不挂 Citation、不要求逐字忠实——与 EvidenceQA（CitedDerivative）形成对照。
type ParaphraseProvider interface {
	Paraphrase(question string, referenceSegments []Segment) (*ParaphraseResult, error)
	Name() string
}

// StudyChatMessage 一轮学习对话的消息（GeneratedDerivative，ADR-0018）。
type StudyChatMessage struct {
	Role                string   `json:"role"` // user | assistant
	Content             string   `json:"content"`
	ReferenceSegmentIDs []string `json:"referenceSegmentIds,omitempty"` // assistant 回答所参考的 Segment（Reference，非 Citation）
	Suppressed          bool     `json:"suppressed,omitempty"`          // 因 ReferenceCheck 失败被抑制时为 true（仅内部记录，不呈现）
}

// StudyChatResult 学习对话一轮的输出。
type StudyChatResult struct {
	Answer            *StudyChatMessage // 生成的回答；ScopeTethered 或 ReferenceCheck 失败时为 nil
	ScopeFeedback     string            // 硬约束一失败（无法关联任何 Segment）的可见反馈；非空表示不生成
	ReferenceRejected bool              // 硬约束二失败（ReferenceCheck 判定主题不锚定）的可见反馈
}

// StudyChatProvider 学习对话接口（GeneratedDerivative，ADR-0018 R3）。
// 输入：Owner 问题 + 历史 + 当前 Source 可检索的 Segment；模型自选参考 Segment 并生成回答。
// 调用方负责硬约束一（无 Reference 不生成）与硬约束二（ReferenceCheck）。
type StudyChatProvider interface {
	StudyChatAnswer(question string, history []StudyChatMessage, candidates []Segment) (*StudyChatResult, error)
	Name() string
}

// ReferenceCheckResult 主题锚定校验结果（ADR-0018 R3 硬约束二）。
type ReferenceCheckResult struct {
	Related bool   // 回答主题是否扎根于 Reference 段所讨论内容
	Reason  string // 可选说明
}

// ReferenceChecker 主题锚定校验器接口（独立步骤，不参与生成）。
// 输入三元组（问题 + 回答 + Reference 段文本），只判相关、不判逐字忠实。
type ReferenceChecker interface {
	CheckReference(question, answer string, referenceSegments []Segment) (ReferenceCheckResult, error)
	Name() string
}

// NarrationResult 一次 TTS 合成的结果（ADR-0019）。
type NarrationResult struct {
	AudioPath       string  // 合成出的 wav 文件绝对路径
	DurationSeconds float64 // 合成音频时长
	CharCount       int     // 合成文本的字符数
	Voice           string  // 实际使用的音色标识
	Model           string  // 引擎+模型标识
}

// NarrationProvider 将文字合成为解说音轨（GeneratedDerivative 的音频形态，ADR-0019）。
// 默认实现是自托管的免费 TTS（Kokoro CLI），付费 TTS 作为可选实现（按 ADR-0009 单次授权）。
// Narration 永不读可核验内容（Summary/KeyPoint/Quote），只读 GeneratedDerivative（Gist）。
type NarrationProvider interface {
	// Synthesize 将 text 合成为 wav，写入 outPath（调用方提供，确保目录存在）。
	// voice 为空时用 Provider 默认音色。返回合成结果含时长/字符数。
	Synthesize(text, voice, outPath string) (*NarrationResult, error)
	// Available 探测引擎是否可用（如 kokoro 二进制是否安装）；不可用时 worker 跳过合成、不阻塞。
	Available() bool
	Name() string
}

// ConfigurableProvider 可按外部配置指定模型名（ADR-0009 扩展：每任务配 Provider+Model）。
type ConfigurableProvider interface {
	// WithModel 返回一个使用指定模型名的新实例（不修改原实例）。
	WithModel(model string) interface{}
}
