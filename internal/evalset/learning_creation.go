// learning_creation.go 学习与成文评测夹具（学习-DJ-创作原子计划 A04）。
//
// 覆盖场景：中文长访谈、英文信息密集节目、多主题/重复/广告、个人笔记身份、跨集矛盾观点。
// 库内只保存自建（self-made）可提交片段；真实节目不入库，仅在 docs/acceptance/
// learning-creation-baseline.md 记录来源与本地快照标识后本地运行。
//
// 每个样本的人工参考（核心观点、支持区间、限定条件、不可推出结论）是后续
// K01/K05/G 组自动与人工评测的判据：自动测试校验夹具自身完整性；真实模型
// 评分待实际生成后填写，不预先给通过结论。
package evalset

import (
	"fmt"

	"github.com/woyin/orangecast/internal/provider"
)

// CorePoint 人工标注的核心观点：判据本体，不是模型输出。
type CorePoint struct {
	Claim           string   // 观点主张（人工措辞）
	SupportSegments []string // 支持区间（Segment ID，必须存在于本样本）
	Qualifiers      []string // 重要限定条件；丢失即算过度推论
	NotImplied      []string // 不可推出的结论：模型常犯的过度引申，出现即记失败
}

// EpisodeFixture 一集学习样本：分段素材 + 噪声标注 + 人工参考。
type EpisodeFixture struct {
	ID                  string
	Language            string // zh | en
	Scenario            string // long_interview | info_dense | multi_topic_ads | conflict_pro | conflict_con
	Description         string
	Source              string // 自建样本固定 "self-made"；真实节目写 "external:<来源>#<本地快照标识>"，片段不入库
	Segments            []provider.Segment
	AdSegmentIDs        []string   // 广告/招贴段：提取结果不得把它们当内容观点
	RepeatedPointGroups [][]string // 语义重复的观点 Segment 组：用于重复率评测（同组不叠加计为多个观点）
	CorePoints          []CorePoint
}

// CrossEpisodeCase 跨集矛盾观点用例：两条成文路径都不得把冲突合并成共识。
type CrossEpisodeCase struct {
	ID          string
	EpisodeIDs  []string // ≥2 个 EpisodeFixture.ID，立场相互对立
	Conflict    string   // 矛盾点
	Expectation string   // 评测期望
}

// OwnerNoteCase 个人笔记身份用例：OwnerReflection 不得伪装为来源主张或引用事实。
type OwnerNoteCase struct {
	ID             string
	EpisodeID      string
	Kind           string // source_note（忠实记录，可挂 Citation）| owner_reflection（个人理解，只能 Reference）
	Content        string
	AnchorSegments []string
	// IsEvidence 期望值：source_note=true（可用作来源主张素材）；owner_reflection=false。
	IsEvidence bool
}

// learningEpisodes 学习样本集（自建）。Segment ID 带 Episode 前缀避免跨样本混淆。
func learningEpisodes() []EpisodeFixture {
	return []EpisodeFixture{
		{
			ID: "zh-long-01", Language: "zh", Scenario: "long_interview",
			Description: "中文长访谈：主持人/嘉宾轮替，含中段广告与限定条件",
			Source:      "self-made",
			Segments: []provider.Segment{
				{ID: "li-seg-01", Start: 0, End: 8, Text: "主持人：欢迎收听本期节目，今天我们请到老年研究者林教授聊聊老年人的数字生活。"},
				{ID: "li-seg-02", Start: 8, End: 17, Text: "林教授：我们团队在两个社区访谈了三十位六十五岁以上的老人，跟踪了半年。"},
				{ID: "li-seg-03", Start: 17, End: 26, Text: "绝大多数受访者最常用的是微信，但功能基本局限在子女手把手教过的视频通话和发红包。"},
				{ID: "li-seg-04", Start: 26, End: 34, Text: "这里要强调，这是两个社区的样本，不能直接推广到所有老年人。"},
				{ID: "li-seg-05", Start: 34, End: 42, Text: "广告：本期节目由安心体检卡赞助，全身体检限时五折，点击链接了解。"},
				{ID: "li-seg-06", Start: 42, End: 51, Text: "主持人：刚才说到学习成本，您观察到的主要障碍是什么？"},
				{ID: "li-seg-07", Start: 51, End: 61, Text: "林教授：最大的障碍不是不愿意学，而是字体太小、流程步骤多，一次教完转头就忘。"},
				{ID: "li-seg-08", Start: 61, End: 70, Text: "有受访者说，不是不想用手机挂号，是挂号应用每半年改版一次，重新学一遍太累。"},
				{ID: "li-seg-09", Start: 70, End: 79, Text: "所以我们建议界面改造优先级应该高于开设老年手机课堂。"},
				{ID: "li-seg-10", Start: 79, End: 88, Text: "主持人：这个建议有没有前提？"},
				{ID: "li-seg-11", Start: 88, End: 97, Text: "林教授：前提是针对已有使用意愿的老人；完全无意愿的群体，任何改造都效果有限。"},
				{ID: "li-seg-12", Start: 97, End: 105, Text: "主持人：感谢林教授，也提醒大家，访谈细节和完整报告会在Shownotes里给出链接。"},
			},
			AdSegmentIDs: []string{"li-seg-05"},
			RepeatedPointGroups: [][]string{
				{"li-seg-03", "li-seg-08"}, // "只常用子女教过的功能" 与 "改版后要重学" 同属学习成本高的表现
			},
			CorePoints: []CorePoint{
				{
					Claim:           "受访老人最常用微信，但功能局限于子女教授过的少数操作",
					SupportSegments: []string{"li-seg-02", "li-seg-03"},
					Qualifiers:      []string{"样本为两个社区 30 位 65 岁以上老人", "跟踪半年", "不能推广到所有老年人"},
					NotImplied:      []string{"老年人普遍拒绝使用智能手机", "微信是老年人唯一的社交工具"},
				},
				{
					Claim:           "主要障碍是界面可用性（字体、步骤、改版频繁）而非学习意愿",
					SupportSegments: []string{"li-seg-07", "li-seg-08"},
					Qualifiers:      []string{"基于访谈观察，非实验证据"},
					NotImplied:      []string{"老年手机课堂完全没有价值"},
				},
				{
					Claim:           "建议界面改造优先于开设课堂，但仅适用于已有使用意愿的群体",
					SupportSegments: []string{"li-seg-09", "li-seg-11"},
					Qualifiers:      []string{"前提：针对已有使用意愿的老人", "对无意愿群体效果有限"},
					NotImplied:      []string{"界面改造能解决全部数字鸿沟问题"},
				},
			},
		},
		{
			ID: "en-dense-01", Language: "en", Scenario: "info_dense",
			Description: "英文信息密集：数字、机构名、限定密集，考验引用支持",
			Source:      "self-made",
			Segments: []provider.Segment{
				{ID: "ed-seg-01", Start: 0, End: 7, Text: "Battery recycling is scaling fast: plants processed roughly 95,000 tonnes of packs in Europe last year."},
				{ID: "ed-seg-02", Start: 7, End: 15, Text: "Hydrometallurgical recovery can reclaim up to 95 percent of cobalt and nickel, though that figure comes from pilot lines, not full-scale plants."},
				{ID: "ed-seg-03", Start: 15, End: 23, Text: "The economics still depend heavily on cobalt prices; at today's lows, several operators break even only with subsidy support."},
				{ID: "ed-seg-04", Start: 23, End: 31, Text: "Direct recycling researcher Dr. Amara Osei argues lab-to-plant transfer remains the field's hardest step."},
				{ID: "ed-seg-05", Start: 31, End: 39, Text: "Regulation is pulling the other direction: the EU battery passport becomes mandatory in 2027, forcing traceability."},
				{ID: "ed-seg-06", Start: 39, End: 47, Text: "Still, second-life storage projects absorb only a fraction of retired packs, because testing costs stay high."},
			},
			AdSegmentIDs:        nil,
			RepeatedPointGroups: nil,
			CorePoints: []CorePoint{
				{
					Claim:           "Hydrometallurgical recovery reaches up to 95% cobalt/nickel recovery, demonstrated at pilot scale",
					SupportSegments: []string{"ed-seg-02"},
					Qualifiers:      []string{"pilot lines, not full-scale plants", "up to"},
					NotImplied:      []string{"Full-scale plants already achieve 95% recovery", "All battery minerals are economically recoverable"},
				},
				{
					Claim:           "Recycling economics hinge on cobalt prices; several operators need subsidies at current lows",
					SupportSegments: []string{"ed-seg-03"},
					Qualifiers:      []string{"at today's lows", "several operators, not all"},
					NotImplied:      []string{"Recycling is unprofitable everywhere", "Subsidies make recycling profitable"},
				},
				{
					Claim:           "EU battery passport mandates traceability from 2027",
					SupportSegments: []string{"ed-seg-05"},
					Qualifiers:      []string{"EU jurisdiction"},
					NotImplied:      []string{"The passport guarantees recycling rates will rise"},
				},
				{
					Claim:           "Second-life storage absorbs only a fraction of retired packs due to testing costs",
					SupportSegments: []string{"ed-seg-06"},
					Qualifiers:      []string{"testing costs stay high"},
					NotImplied:      []string{"Second-life storage is technically infeasible"},
				},
			},
		},
		{
			ID: "zh-multi-01", Language: "zh", Scenario: "multi_topic_ads",
			Description: "多主题/重复/广告：三个主题混排，同一观点跨窗重复，含两段广告",
			Source:      "self-made",
			Segments: []provider.Segment{
				{ID: "mt-seg-01", Start: 0, End: 8, Text: "先说咖啡：多项队列研究显示，每天三到四杯咖啡与更低的全因死亡率相关。"},
				{ID: "mt-seg-02", Start: 8, End: 16, Text: "注意这是相关性，不是因果——可能健康人群本来就更爱喝咖啡。"},
				{ID: "mt-seg-03", Start: 16, End: 24, Text: "广告：好眠床垫，睡出好脊椎，今晚下单立减两千，链接在评论区。"},
				{ID: "mt-seg-04", Start: 24, End: 32, Text: "睡眠部分提醒一句：咖啡因半衰期约五小时，下午三点后的拿铁可能影响入睡。"},
				{ID: "mt-seg-05", Start: 32, End: 40, Text: "换个话题说远程办公：混合办公团队的离职率显著低于全坐班团队。"},
				{ID: "mt-seg-06", Start: 40, End: 48, Text: "回到咖啡：换句话说，适量饮用咖啡的人群死亡率更低，但机制还不清楚。"},
				{ID: "mt-seg-07", Start: 48, End: 56, Text: "远程办公的研究同样有挑选偏差：愿意混合办公的公司本身管理更灵活。"},
				{ID: "mt-seg-08", Start: 56, End: 64, Text: "广告二：本期书籍《睡眠的科学》在出版社旗舰店有小度活动，感兴趣自己搜。"},
				{ID: "mt-seg-09", Start: 64, End: 72, Text: "最后给行动建议：想调整咖啡习惯，先固定最后一杯的时间，再观察两周睡眠变化。"},
			},
			AdSegmentIDs: []string{"mt-seg-03", "mt-seg-08"},
			RepeatedPointGroups: [][]string{
				{"mt-seg-01", "mt-seg-06"}, // 同一观点（适量咖啡与更低死亡率相关）在两个分析窗重复表述
			},
			CorePoints: []CorePoint{
				{
					Claim:           "每天三到四杯咖啡与更低全因死亡率相关，但只是相关性",
					SupportSegments: []string{"mt-seg-01", "mt-seg-02", "mt-seg-06"},
					Qualifiers:      []string{"相关性非因果", "机制不清楚", "可能存在健康人群自选择"},
					NotImplied:      []string{"喝咖啡能延长寿命", "所有人都应该每天喝四杯咖啡"},
				},
				{
					Claim:           "咖啡因半衰期约五小时，下午晚些的摄入可能影响入睡",
					SupportSegments: []string{"mt-seg-04"},
					Qualifiers:      []string{"个体差异未讨论", "约"},
					NotImplied:      []string{"下午喝咖啡一定失眠"},
				},
				{
					Claim:           "混合办公团队离职率更低，但研究存在挑选偏差",
					SupportSegments: []string{"mt-seg-05", "mt-seg-07"},
					Qualifiers:      []string{"愿意混合办公的公司本身管理更灵活"},
					NotImplied:      []string{"全员远程办公必然降低离职率"},
				},
				{
					Claim:           "行动建议：固定最后一杯咖啡的时间并观察两周睡眠变化",
					SupportSegments: []string{"mt-seg-09"},
					Qualifiers:      []string{"是建议而非研究结论"},
					NotImplied:      []string{"该方法经过临床试验验证"},
				},
			},
		},
		{
			ID: "zh-conflict-01", Language: "zh", Scenario: "conflict_pro",
			Description: "跨集矛盾·甲方：强调低剂量咖啡对心血管的正面关联",
			Source:      "self-made",
			Segments: []provider.Segment{
				{ID: "cp-seg-01", Start: 0, End: 9, Text: "本期我们看一项四十万人的队列研究：每天一到两杯咖啡的人群，心血管疾病风险略低。"},
				{ID: "cp-seg-02", Start: 9, End: 18, Text: "研究者猜测与咖啡多酚和抗炎作用有关，但作者自己承认这是关联不是因果。"},
			},
			CorePoints: []CorePoint{
				{
					Claim:           "每天一到两杯咖啡的人群心血管疾病风险略低（四十万人队列）",
					SupportSegments: []string{"cp-seg-01"},
					Qualifiers:      []string{"关联不是因果", "机制只是猜测"},
					NotImplied:      []string{"咖啡保护心血管已被证明"},
				},
			},
		},
		{
			ID: "zh-conflict-02", Language: "zh", Scenario: "conflict_con",
			Description: "跨集矛盾·乙方：强调咖啡因短期升压与敏感人群限制",
			Source:      "self-made",
			Segments: []provider.Segment{
				{ID: "cc-seg-01", Start: 0, End: 9, Text: "咖啡因会在摄入后三十到六十分钟内短暂升高血压，对高血压前期人群尤其明显。"},
				{ID: "cc-seg-02", Start: 9, End: 18, Text: "心内科医生的建议是：已确诊高血压的人先和医生商量饮用量，不要按研究里的一到两杯照搬。"},
			},
			CorePoints: []CorePoint{
				{
					Claim:           "咖啡因短期内升高血压，对高血压前期人群更明显",
					SupportSegments: []string{"cc-seg-01"},
					Qualifiers:      []string{"短期效应", "高血压前期人群尤其明显"},
					NotImplied:      []string{"咖啡导致高血压"},
				},
				{
					Claim:           "确诊高血压者应先咨询医生，不宜照搬研究中的一到两杯",
					SupportSegments: []string{"cc-seg-02"},
					Qualifiers:      []string{"针对已确诊人群", "是医生建议而非研究结论"},
					NotImplied:      []string{"健康人也不能喝咖啡"},
				},
			},
		},
	}
}

// LearningEpisodes 返回学习样本集。内容编译期固定，重复调用返回等值结果
// （可重复加载由测试用两次调用深比较保证）。
func LearningEpisodes() []EpisodeFixture {
	return learningEpisodes()
}

// CrossEpisodeCases 返回跨集矛盾用例。
func CrossEpisodeCases() []CrossEpisodeCase {
	return []CrossEpisodeCase{
		{
			ID:          "cross-coffee-01",
			EpisodeIDs:  []string{"zh-conflict-01", "zh-conflict-02"},
			Conflict:    "低剂量咖啡与更低心血管风险相关 vs 咖啡因短期升压、确诊人群应限制",
			Expectation: "归并、精读与文章都必须保留双方限定条件，不得合并为'咖啡有益心血管'的共识，也不得用一方否定另一方",
		},
	}
}

// OwnerNoteCases 返回个人笔记身份用例。
func OwnerNoteCases() []OwnerNoteCase {
	return ownerNoteCasesForCheck
}

// ownerNoteCasesForCheck 笔记用例存储（测试可临时替换以覆盖校验分支）。
var ownerNoteCasesForCheck = []OwnerNoteCase{
	{
		ID: "note-li-01", EpisodeID: "zh-long-01", Kind: "source_note",
		Content:        "受访者提到挂号应用每半年改版一次，老人需要重新学习。",
		AnchorSegments: []string{"li-seg-08"},
		IsEvidence:     true,
	},
	{
		ID: "note-li-02", EpisodeID: "zh-long-01", Kind: "owner_reflection",
		Content:        "这让我想起我外婆：只肯用视频通话，每次系统更新都要找人帮忙——我猜改版疲劳是普遍现象。",
		AnchorSegments: []string{"li-seg-03"},
		IsEvidence:     false,
	},
}

// setOwnerNoteCasesForCheck 替换笔记用例集（仅测试使用）。
func setOwnerNoteCasesForCheck(cases []OwnerNoteCase) {
	ownerNoteCasesForCheck = cases
}

// CheckLearningFixtures 校验学习/成文夹具自身的完整性（不评模型输出）。
// 返回空切片表示夹具可重复加载且人工参考可追溯。
func CheckLearningFixtures() []Issue {
	var issues []Issue
	byID := map[string]bool{}
	segIDs := map[string]map[string]bool{} // episode -> segment ids
	episodes := learningEpisodes()
	for _, ep := range episodes {
		if byID[ep.ID] {
			issues = append(issues, Issue{SampleID: ep.ID, Kind: "schema", Detail: fmt.Sprintf("EpisodeFixture ID 重复: %s", ep.ID)})
		}
		byID[ep.ID] = true
		issues = append(issues, checkEpisodeFixture(ep)...)
		ids := map[string]bool{}
		for _, seg := range ep.Segments {
			ids[seg.ID] = true
		}
		segIDs[ep.ID] = ids
	}

	add := func(id, kind, format string, args ...any) {
		issues = append(issues, Issue{SampleID: id, Kind: kind, Detail: fmt.Sprintf(format, args...)})
	}
	for _, cs := range CrossEpisodeCases() {
		if len(cs.EpisodeIDs) < 2 {
			add(cs.ID, "schema", "跨集用例至少需要两集")
		}
		for _, eid := range cs.EpisodeIDs {
			if !byID[eid] {
				add(cs.ID, "citation", "跨集用例引用不存在的 Episode %s", eid)
			}
		}
		if cs.Conflict == "" || cs.Expectation == "" {
			add(cs.ID, "schema", "跨集用例必须写明矛盾点与评测期望")
		}
	}

	for _, nc := range OwnerNoteCases() {
		ids, ok := segIDs[nc.EpisodeID]
		if !ok {
			add(nc.ID, "citation", "笔记用例引用不存在的 Episode %s", nc.EpisodeID)
			continue
		}
		if len(nc.AnchorSegments) == 0 {
			add(nc.ID, "schema", "笔记用例缺少锚定区间")
		}
		for _, sid := range nc.AnchorSegments {
			if !ids[sid] {
				add(nc.ID, "citation", "笔记用例锚定不存在的 Segment %s", sid)
			}
		}
		if nc.Kind != "source_note" && nc.Kind != "owner_reflection" {
			add(nc.ID, "schema", "笔记类型非法: %s", nc.Kind)
		}
		if nc.Kind == "owner_reflection" && nc.IsEvidence {
			add(nc.ID, "schema", "OwnerReflection 不得标记为证据身份")
		}
		if nc.Kind == "source_note" && !nc.IsEvidence {
			add(nc.ID, "schema", "SourceNote 应可作为来源素材（IsEvidence=true）")
		}
	}
	return issues
}

// checkEpisodeFixture 校验单个 Episode 夹具的结构与标注完备性。
func checkEpisodeFixture(ep EpisodeFixture) []Issue {
	var issues []Issue
	add := func(kind, format string, args ...any) {
		issues = append(issues, Issue{SampleID: ep.ID, Kind: kind, Detail: fmt.Sprintf(format, args...)})
	}
	if ep.Source == "" {
		add("schema", "缺少 Source 标识（自建或 external:<来源>#<快照>）")
	}
	ids := map[string]bool{}
	for _, seg := range ep.Segments {
		if ids[seg.ID] {
			add("schema", "Segment ID 重复: %s", seg.ID)
		}
		ids[seg.ID] = true
		if seg.Start < 0 || seg.End <= seg.Start {
			add("time", "segment %s 时间非法: %.2f-%.2f", seg.ID, seg.Start, seg.End)
		}
		if seg.Text == "" {
			add("schema", "segment %s 文本为空", seg.ID)
		}
	}
	for _, ad := range ep.AdSegmentIDs {
		if !ids[ad] {
			add("citation", "广告段 %s 不存在", ad)
		}
	}
	if ep.Scenario == "multi_topic_ads" && len(ep.AdSegmentIDs) == 0 {
		add("schema", "multi_topic_ads 场景必须标注至少一段广告")
	}
	for gi, group := range ep.RepeatedPointGroups {
		if len(group) < 2 {
			add("schema", "重复组 %d 少于两个 Segment，不构成重复", gi)
		}
		for _, sid := range group {
			if !ids[sid] {
				add("citation", "重复组 %d 引用不存在的 Segment %s", gi, sid)
			}
		}
	}
	if ep.Scenario == "multi_topic_ads" && len(ep.RepeatedPointGroups) == 0 {
		add("schema", "multi_topic_ads 场景必须提供跨窗重复组")
	}
	if len(ep.CorePoints) == 0 {
		add("schema", "没有人工标注的核心观点")
	}
	for ci, cp := range ep.CorePoints {
		if len(cp.SupportSegments) == 0 {
			add("citation", "核心观点 %d 缺少支持区间", ci)
		}
		for _, sid := range cp.SupportSegments {
			if !ids[sid] {
				add("citation", "核心观点 %d 引用不存在的 Segment %s", ci, sid)
			}
		}
		if len(cp.Qualifiers) == 0 {
			add("schema", "核心观点 %d 未标注限定条件（判据要求显式列出）", ci)
		}
		if len(cp.NotImplied) == 0 {
			add("schema", "核心观点 %d 未标注不可推出的结论", ci)
		}
	}
	return issues
}
