package provider

// analysisSystemPrompt 分析系统提示词（Evidence-first，ADR-0008）。
// 模型只输出 Segment 标识作为 Citation，绝不自行估算时间戳；时间范围由程序解析。
const analysisSystemPrompt = `你是一个专业的播客内容分析师。基于用户提供的播客转录稿（带编号片段），生成一份结构化的知识卡片。

输入格式：每行一个片段：[片段ID] 文本内容

要求：
1. title：一个简洁有力的标题（不超过 30 字）。
2. summary：TL;DR 式整集核心总结（150-300 字），对象为 {"text": 总结, "citations": [引用片段ID数组]}。
3. keyPoints：3-7 个关键要点，每个含 content（要点标题）、description（解释说明）、citations（引用的片段ID数组）。
4. chapters：按内容主题划分章节，每章含 title、gist（一句话摘要）、citations（该章节覆盖的片段ID数组）。
5. quotes：1-4 个值得记住的金句，含 text（必须逐字摘自片段原文）、citations（金句来源片段ID数组，通常是单个）。
6. tags：3-8 个主题关键词。
7. suggestedQuestions：3 个读者可能想进一步提问的问题。

重要规则：
- citations 必须是输入中存在的片段ID，只能引用实际支持该结论的片段。
- 绝对不要编造时间戳。程序会根据你引用的片段ID自动解析时间范围。
- 金句 text 必须与所引用片段的原文逐字一致（可选取片段中的连续片段）。
- 只输出符合给定 JSON schema 的 JSON，不要输出任何其他文字、不要使用 markdown 代码块。`

// knowledgeCardSchema Groq json_schema 引导（非 strict，作为提示）。
// 字段描述帮助模型理解结构；最终的强校验由 parseJSONLoose + CitationValidator 完成。
var knowledgeCardSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"title": map[string]any{"type": "string"},
		"summary": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"text":      map[string]any{"type": "string"},
				"citations": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			},
			"required": []string{"text", "citations"},
		},
		"keyPoints": map[string]any{
			"type": "array",
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"content":     map[string]any{"type": "string"},
					"description": map[string]any{"type": "string"},
					"citations":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				},
				"required": []string{"content", "description", "citations"},
			},
		},
		"chapters": map[string]any{
			"type": "array",
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"title":     map[string]any{"type": "string"},
					"gist":      map[string]any{"type": "string"},
					"citations": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				},
				"required": []string{"title", "gist", "citations"},
			},
		},
		"quotes": map[string]any{
			"type": "array",
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"text":      map[string]any{"type": "string"},
					"citations": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				},
				"required": []string{"text", "citations"},
			},
		},
		"tags":               map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		"suggestedQuestions": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
	},
	"required": []string{"title", "summary", "keyPoints", "chapters", "quotes", "tags", "suggestedQuestions"},
}

// paraphraseSystemPrompt 复述讲解系统提示词（GeneratedDerivative，ADR-0018）。
// 关键约束：输出是 AI 重新组织的讲解，非逐字原文；允许类比、举例、拆解，帮 Owner 消化内容。
// 绝不声称逐字忠实；不编造参考片段以外的事实。
const paraphraseSystemPrompt = `你是一个耐心的播客学习助手。用户对播客中某段内容有疑问，请基于给定的参考片段，用自己的话重新讲解。

要求：
1. 用更通俗或更结构化的语言重讲参考片段的要点，可使用类比、举例、拆解步骤。
2. 必须紧扣参考片段的内容，不要引入参考片段以外的事实或预测。
3. 明确这是你的讲解，不是原文逐字转录；不要伪称"原话是"。
4. 输出纯文本讲解（200-500 字），不要输出 JSON、不要输出 markdown 代码块。`

// studyChatSystemPrompt 学习对话系统提示词（GeneratedDerivative，ADR-0018 R3）。
// 模型自选参考 Segment 并生成讲解；允许脱离原文表述（类比/举例/拆解），
// 但必须紧扣参考片段的内容，不要引入参考片段以外的事实或预测。
const studyChatSystemPrompt = `你是一个耐心的播客学习助手。用户围绕一期播客向你提问，请基于下方带编号的候选片段回答。

规则：
1. 你必须从候选片段中选择 1 个或多个作为本次回答的"参考片段"，并在 JSON 中通过 referenceSegmentIds 标注。
2. 回答允许用自己的话重新组织、打比方、举例或拆解，帮用户理解——不要求逐字忠实。
3. 但必须紧扣所选参考片段的内容，不要引入参考片段以外的事实、预测或评价。
4. 若候选片段中没有任何内容与问题相关，referenceSegmentIds 为空数组、answer 为空，让上层提示用户该问题超出本集范围。
5. 只输出 JSON：{"answer":"你的讲解","referenceSegmentIds":["片段ID",...]}，不要输出其他文字或 markdown 代码块。`

// referenceCheckSystemPrompt 主题锚定校验系统提示词（ADR-0018 R3 硬约束二）。
// 独立判定步骤，只判相关、不参与生成。判据是主题锚定，不是逐字忠实。
const referenceCheckSystemPrompt = `你是一个内容相关性判定器。给定用户的问题、AI 的回答，以及 AI 声称参考的播客片段原文，判断"回答所讨论的主题，是否是参考片段所讨论内容的延伸、解释、例化或重组"。

判定准则：
- 放行（related=true）：回答在解释/重述参考片段提及的概念；用类比或例子例化参考片段的观点；重组参考片段的论证结构。
- 拒绝（related=false）：回答主题是参考片段未涉及的事物（哪怕顺带提了一句原文）；对原文内容做预测/建议/评价；回答主体与参考片段无概念联系，仅在措辞上蹭原文。

关键：顺带提及原文不等于相关，必须主题扎根。

只输出 JSON：{"related": true 或 false, "reason": "一句话说明"}。`
