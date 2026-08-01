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
