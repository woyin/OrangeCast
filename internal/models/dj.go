package models

// DJ 播放清单（D02 / ADR-0024 §1/§3）：来源快照 + 可用高光 + 目标时长 → 有序清单。
// 清单按版本持久化：新高光版本生成新清单，正在收听的旧清单不被替换。
// 项类型：evidence（原音区间，程序解析起止）与 narration（解说引用）；
// 所有语音标注为 AI 解说或原音，身份不混淆。

const (
	// DJItemEvidence 原音区间项。
	DJItemEvidence = "evidence"
	// DJItemNarration 解说项（串场/开场/收尾，GeneratedDerivative 音频）。
	DJItemNarration = "narration"
)

// DJPlan 一份 DJ 播放清单。
type DJPlan struct {
	ID                string
	SourceType        SourceType
	SourceID          string
	Version           int
	TargetSeconds     float64
	TotalSeconds      float64 // 估算总时长（含串场估计）；内容不足允许短于目标
	HighlightVersion  int     // 输入：冻结的高光版本
	InputSnapshotJSON string
	CreatedAt         string

	Items []DJPlanItem `json:"-"` // 有序条目（存储层加载）
}

// DJPlanItem 清单中的一项。
type DJPlanItem struct {
	Position    int
	Kind        string // evidence | narration
	HighlightID string
	NarrationID string // 仅 narration 项：指向 narrations.id
	SegmentIDs  []string
	Start, End  float64
	EstSeconds  float64
	Reason      string // 选择/排序理由（首版：原顺序 + 时长预算）

	// D04：串场解说词（受约束模板生成，随清单持久化，可审计、可重合成）。
	ScriptKind string // intro | transition | outro | gist（空 = 无脚本）
	ScriptText string
}

// ListeningProgress 听播进度（D07）：Source + 清单版本 + 片段身份 + 片段内位置
// + 速度。seq 单调递增防旧请求覆盖新状态；与完整播放（原音页）进度语义分开。
type ListeningProgress struct {
	ID                string
	SourceType        SourceType
	SourceID          string
	PlanID            string
	PlanVersion       int
	ItemPosition      int
	HighlightID       string
	ItemOffsetSeconds float64
	Speed             float64
	Seq               int64
	UpdatedAt         string
}
