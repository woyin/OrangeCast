package provider

import "context"

// EpisodeDigest（单集精读文）provider 契约（ADR-0023）。
// 正文是四类 DigestBlock 的封闭集合：转述 / AI 展开 / 笔记 / 引用事实。
// 类型身份在生成时确定，程序化门禁（ValidateDigestBlocks）在代码层强制不变量。

// DigestKeyPoint 本集已验证的 KeyPoint，供转述块引用其 Segment。
type DigestKeyPoint struct {
	ID         string
	Content    string
	Citations  []string
	Origin     string
	Production string
}

// DigestOwnerNote Owner 已写下的笔记（SourceNote / OwnerReflection / Annotation 投影）。
// 笔记块只许重排、不许改写；NoteID 用于门禁回溯原文。
type DigestOwnerNote struct {
	NoteID string
	Text   string
	Kind   string // source_note | owner_reflection | annotation
}

// DigestWritingRequest 封闭集合的全部输入素材。
// FactGapBudget 限制一次生成最多触发检索的事实缺口数；0 表示不允许联网补源。
type DigestWritingRequest struct {
	SourceTitle       string
	SourceSummary     string
	Segments          []Segment
	KeyPoints         []DigestKeyPoint
	Notes             []DigestOwnerNote
	FactGapBudget     int
	TargetLengthChars int
}

// DigestBlockTypeStrings 与 models.DigestBlockType 一一对应的字符串常量。
// provider 层不 import models，避免反向依赖；仓储层负责与 models 值互转。
const (
	DigestBlockParaphraseStr  = "paraphrase"
	DigestBlockAIExpansionStr = "ai_expansion"
	DigestBlockNoteStr        = "note"
	DigestBlockCitedFactStr   = "cited_fact"
)

// DigestBlockDraft 生成出的内容块草稿（保存前须经 ValidateDigestBlocks 门禁）。
type DigestBlockDraft struct {
	Type           string
	Text           string
	Citations      []string
	TargetSourceID string
	NoteID         string
}

// DigestWritingResult ComposeDraft 的输出：标题 + 内容块（不含引用事实）+ 事实缺口清单。
type DigestWritingResult struct {
	Title    string
	Blocks   []DigestBlockDraft
	FactGaps []string
	Usage    TaskUsage
}

// DigestWriterProvider 从封闭集合素材生成单集精读文长文版（一次成稿，引用事实由 WeaveFacts 补织）。
type DigestWriterProvider interface {
	// ComposeDraft 基于本集素材生成标题、转述/AI 展开/笔记块与事实缺口。
	ComposeDigest(ctx context.Context, req DigestWritingRequest) (*DigestWritingResult, error)
	// WeaveFacts 把检索落源的材料补织为引用事实块；只能追加 cited_fact 块，不得改动已有块。
	WeaveDigestFacts(ctx context.Context, req DigestWeaveRequest) (*DigestWeaveResult, error)
}

// DigestDocument 检索落源后的 Document 摘要 + 其 Segment（供引用事实块引用）。
type DigestDocument struct {
	DocumentID string
	Title      string
	URL        string
	Segments   []Segment // DocumentSegments 投影（ID 稳定）
}

// DigestWeaveRequest 第二阶段输入：已过门禁的长文块（不可变）+ 落源材料 + 对应缺口。
type DigestWeaveRequest struct {
	Title       string
	Blocks      []DigestBlockDraft
	FactGaps    []string
	Documents   []DigestDocument
	TargetChars int
}

// DigestWeaveResult 第二阶段输出：仅追加的 cited_fact 块（门禁校验引用必须落在给定 Document Segment 内）。
type DigestWeaveResult struct {
	Blocks []DigestBlockDraft
	Usage  TaskUsage
}

// DigestSearchResult 一条检索结果；Title 可空（落源后以页面标题补全）。
type DigestSearchResult struct {
	URL     string
	Title   string
	Snippet string
}

// SourceSearchProvider 受限联网检索（SourceSearch，ADR-0023 §3）：只产出 URL 候选，
// 摘要永不直接入文；落源由调用方经 fetchWebDocument 管道完成。
type SourceSearchProvider interface {
	Search(ctx context.Context, query string) ([]DigestSearchResult, error)
}

// DigestRewriteRequest 受约束改写（F3）输入：已过门禁的长文块 + 渠道规格。
type DigestRewriteRequest struct {
	Channel            string
	Blocks             []DigestBlockDraft
	MaxChars           int
	MaxHashtags        int
	HashtagSuggestions []string
}

// DigestRewriteResult 渠道语气版本；须经 G2 门禁 + 渠道格式校验后落库。
type DigestRewriteResult struct {
	Text  string
	Usage TaskUsage
}

// DigestRewriteProvider 把长文版改写为渠道语气版本；输入只有已过门禁的长文块集合。
type DigestRewriteProvider interface {
	RewriteDigest(ctx context.Context, req DigestRewriteRequest) (*DigestRewriteResult, error)
}
