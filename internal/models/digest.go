package models

// EpisodeDigest 单集精读文（ADR-0023）：以单个 Source 为锚点、一键生成的成文衍生产物。
// 正文是四类 DigestBlock 的封闭集合；不承担 OwnerClaim 纪律，以构造时打标 + 程序化门禁
// 替代主张审校。每次生成为不可变修订（version 递增），重新生成不覆盖历史。
type EpisodeDigest struct {
	ID            string
	SourceType    SourceType
	SourceID      string
	Version       int
	Title         string
	Degraded      bool
	Provider      string
	Model         string
	PromptVersion string
	CreatedAt     string

	// 修订血缘（G03）：parent 指向被调整修订，reason 记录调整原因，
	// source_snapshot_id 关联 B01 来源快照（旧修订为空 = legacy）。
	ParentDigestID   string
	Reason           string
	SourceSnapshotID string
}

// DigestBlockType 精读文内容块的类型身份（构造时确定，不是事后推断）。
type DigestBlockType string

const (
	// DigestBlockParaphrase 本集转述（CitedDerivative，挂本集 Citation）。
	DigestBlockParaphrase DigestBlockType = "paraphrase"
	// DigestBlockAIExpansion AI 展开（GeneratedDerivative，逐段标注且不含硬事实）。
	DigestBlockAIExpansion DigestBlockType = "ai_expansion"
	// DigestBlockNote Owner 既有笔记（只许重排，不许改写）。
	DigestBlockNote DigestBlockType = "note"
	// DigestBlockCitedFact 引用事实（挂检索落源 Document 的 Citation）。
	DigestBlockCitedFact DigestBlockType = "cited_fact"
)

// DigestBlock EpisodeDigest 的构造单元（ADR-0023 §2）。
// TargetSourceID 为空表示引用本集 Source；非空必须是经 SourceSearch 沉淀的 Document Source。
type DigestBlock struct {
	ID             string
	DigestID       string
	Position       int
	Type           DigestBlockType
	Text           string
	Citations      []string
	TargetSourceID string
	NoteID         string
	CreatedAt      string
}

// DigestSearchSource ⑥b 侧栏行：一次 SourceSearch 落源的 Document Source 及其确认状态。
type DigestSearchSource struct {
	ID         string
	DigestID   string
	Query      string
	URL        string
	Title      string
	DocumentID string
	Status     string // pending | confirmed | rejected
	CreatedAt  string
}

// DigestFactGap 生成时想补、但封闭集合内无依据的事实主张（ADR-0023 §2）。
// DocumentID 非空表示已消解；空表示未消解（搜索不可用降级或未命中）。
type DigestFactGap struct {
	ID         string
	DigestID   string
	Text       string
	DocumentID string
	CreatedAt  string
}

// DigestRewrite 渠道语气版本（F3）：输入只有已过门禁的长文 DigestBlock 集合。
type DigestRewrite struct {
	ID        string
	DigestID  string
	Channel   string
	Text      string
	Provider  string
	Model     string
	CreatedAt string
}

// 渠道常量：V1 支持公众号长文版（digest 本体）与小红书笔记版（rewrite）。
const (
	// DigestChannelXiaohongshu 小红书笔记版渠道。
	DigestChannelXiaohongshu = "xiaohongshu"
)
