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

// ProviderBundle 一个 provider 的全套实现。
type ProviderBundle struct {
	Transcription TranscriptionProvider
	Analysis      AnalysisProvider
	QA            QAProvider
	Highlight     HighlightProvider
}

// Highlight AI 判断的"最值得听"的连续音频区间（ADR-0016）。
// Citation 是一组 Segment ID 的集合，程序取 min(start)–max(end) 算时间范围。
type Highlight struct {
	Gist      string   `json:"gist"`      // AI 生成的"为什么这段值得听"说明（非逐字原文）
	Citations []string `json:"citations"` // Segment ID 列表
}

// HighlightSet 一个 Source 的全部高光片段。
type HighlightSet struct {
	Highlights []Highlight `json:"highlights"`
}
