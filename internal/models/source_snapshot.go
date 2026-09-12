package models

// 来源快照（B01 / ADR-0024 §4）：按 Source 及明确版本冻结的不可变读取身份。
// 快照不复制正文，只记录血缘（转录/文档版本 + 原音哈希）；产物保存时应引用
// 快照 ID，使引用绑定冻结内容而非"当前版本"指针。

// SourceSnapshotKind 快照类别：音频（Episode/Upload）或文档（Document）。
type SourceSnapshotKind string

const (
	// SnapshotKindAudio 音频快照：正文来自指定版本的 Transcript Segment。
	SnapshotKindAudio SourceSnapshotKind = "audio"
	// SnapshotKindDocument 文档快照：正文来自指定版本的 EvidenceDocument 段落位置。
	SnapshotKindDocument SourceSnapshotKind = "document"
)

// SourceSnapshotStatus 快照生命周期状态。
const (
	// SnapshotActive 快照可用。
	SnapshotActive = "active"
	// SnapshotMissingAudio 原音文件缺失：正文可读，但明确不可回听。
	SnapshotMissingAudio = "missing_audio"
	// SnapshotPurged 来源已 Purge：快照明确失效，读取报错而不是静默解析。
	SnapshotPurged = "purged"
)

// SourceSnapshotStatus 原音回听可用性（读取时动态判定，不写回快照行）。
const (
	// AudioPlayable 当前原音哈希与冻结一致，可安全回听。
	AudioPlayable = "playable"
	// AudioReplaced 原音已被重新处理覆盖：不回放新文件冒充历史引用，明确不可回听。
	AudioReplaced = "audio_replaced"
	// AudioIdentityUnknown 旧数据缺少原音身份（legacy），可回听性不可推定。
	AudioIdentityUnknown = "audio_unknown"
)

// SourceSnapshot 一个来源在冻结时刻的血缘身份。
type SourceSnapshot struct {
	ID               string
	SourceType       SourceType
	SourceID         string
	Kind             SourceSnapshotKind
	Title            string
	ContentVersion   int    // audio: transcript 版本号；document: documents.version
	ContentVersionID string // audio: artifact_versions.id；document: documents.id
	AudioSHA256      string // 冻结时 EvidenceAudio 哈希；document 为空
	Legacy           bool
	Status           string
	CreatedAt        string
}

// SnapshotAudioIdentity 快照的原音回听身份：可播放性与原因分离。
type SnapshotAudioIdentity struct {
	Status     string // AudioPlayable | AudioReplaced | AudioIdentityUnknown
	CurrentSHA string // 当前 EvidenceAudio 哈希（替换时用于展示差异，不用于回放冒充）
	FrozenSHA  string
	RelPath    string // 仅 Status=playable 时可作为回放地址
	Format     string
	SizeBytes  int64
}
