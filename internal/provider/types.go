package provider

// Segment 转录时间戳段，供播放器联动（第 5 题全三层联动所需）。
type Segment struct {
	Start float64 `json:"start"` // 秒
	End   float64 `json:"end"`
	Text  string  `json:"text"`
}

// TranscriptResult 转录输出：纯文本 + 分段。
type TranscriptResult struct {
	Language string    `json:"language"`
	Text     string    `json:"text"`
	Segments []Segment `json:"segments"`
}

// KnowledgeCard AI 分析的结构化产物（对标 Podwise 的 summary/takeaways/outline/...）。
// LLM 必须按此 schema 输出；Groq 路径走容错解析 + struct 校验（第 10 题决定）。
type KnowledgeCard struct {
	Title             string         `json:"title"`
	Summary           string         `json:"summary"`
	KeyPoints         []KeyPoint     `json:"keyPoints"`
	Chapters          []Chapter      `json:"chapters"`
	Quotes            []Quote        `json:"quotes"`
	Tags              []string       `json:"tags"`
	SuggestedQuestions []string      `json:"suggestedQuestions"`
}

type KeyPoint struct {
	Content     string `json:"content"`
	Description string `json:"description"`
}

// Chapter 章节大纲，带时间戳——播放器 C 层联动（点章节跳转）所需。
type Chapter struct {
	Title     string  `json:"title"`
	StartTime float64 `json:"startTime"`
	EndTime   float64 `json:"endTime"`
	Gist      string  `json:"gist"`
}

// Quote 金句，带时间戳——播放器 C 层联动所需。
type Quote struct {
	Text      string  `json:"text"`
	StartTime float64 `json:"startTime"`
	EndTime   float64 `json:"endTime"`
}

// QAResult 问答输出。Podwise 的 RAG 带引用；当前实现返回答案 + 相关片段引用。
type QAResult struct {
	Answer     string   `json:"answer"`
	Sources    []Source `json:"sources"`
}

// Source 引用来源（转录片段 + 时间戳）。
type Source struct {
	Content string  `json:"content"`
	Start   float64 `json:"start"`
	End     float64 `json:"end"`
}

// TranscriptionProvider 音频转录接口。
type TranscriptionProvider interface {
	// Transcribe 从本地音频文件路径转录。音频已临时落盘，由调用方负责清理。
	Transcribe(filePath string) (*TranscriptResult, error)
	// Name 返回 provider 标识（groq / openai），用于 usage 记录。
	Name() string
}

// AnalysisProvider 内容分析接口，生成 KnowledgeCard。
type AnalysisProvider interface {
	Analyze(transcript string) (*KnowledgeCard, error)
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
}
