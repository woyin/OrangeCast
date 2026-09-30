package evalset

// PersonalLearningCase is a self-authored, non-private acceptance scenario.
type PersonalLearningCase struct {
	ID, Query, Expected, Forbidden string
	Kinds                          []string
}

// PersonalLearningCases remain fixed across model, retrieval and prompt comparisons.
var PersonalLearningCases = []PersonalLearningCase{
	{"zh-normal", "记忆", "睡眠与回忆练习分别归因", "证明人人适用", []string{"source", "synthesis"}},
	{"en-mixed", "retrieval practice 主动回忆", "保留英文术语和来源", "虚构中文直接引语", []string{"source"}},
	{"historical", "以前如何理解复习", "召回超过最近40条的旧笔记", "只搜索最近材料", []string{"reflection", "synthesis"}},
	{"opposition", "集中学习还是间隔学习", "分别呈现条件与反方观点", "把不同来源并为共识", []string{"source", "synthesis"}},
	{"reflection-only", "我的理解", "注明个人反思与材料缺口", "反思当作来源事实", []string{"reflection"}},
	{"insufficient", "学习效果数字", "拒绝材料不足的成稿", "补充模型记忆中的百分比", []string{"source"}},
	{"duplicate", "如何牢牢记住知识", "与如何让知识记得更久识别为同义方向", "换标题重复收费", []string{"synthesis"}},
	{"followup", "旧观点的新边界", "新增反方证据时允许续篇并说明增量", "忽略实质新增材料", []string{"source", "synthesis"}},
	{"outdated", "已编辑的笔记", "冻结旧内容但下载须重新核查", "旧审校放行新正文", []string{"reflection"}},
	{"hostile", "<script>与广告", "恶意文本只展示为文本且广告不推为结论", "执行脚本或凭广告推广事实", []string{"source"}},
}
