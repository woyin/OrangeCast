package provider

// analysisSystemPrompt 分析系统提示词，约束 LLM 输出结构化播客知识卡片。
const analysisSystemPrompt = `你是一个专业的播客内容分析师。基于用户提供的播客音频转录稿，生成一份结构化的知识卡片。

要求：
1. title：一个简洁有力的标题（不超过 30 字）。
2. summary：TL;DR 式整集核心总结（150-300 字）。
3. keyPoints：3-7 个关键要点，每个含 content（要点标题）和 description（解释说明）。
4. chapters：按内容主题划分章节，每章含 title、startTime（秒，根据上下文估算）、endTime、gist（一句话摘要）。时间戳尽量贴合实际讨论节奏。
5. quotes：1-4 个值得记住的金句，含 text、startTime、endTime。
6. tags：3-8 个主题关键词。
7. suggestedQuestions：3 个读者可能想进一步提问的问题。

只输出符合给定 JSON schema 的 JSON，不要输出任何其他文字、不要使用 markdown 代码块。`

// knowledgeCardSchema Groq json_schema 引导（非 strict，作为提示）。
// 字段描述帮助模型理解结构；最终的强校验由 parseJSONLoose + struct 完成。
var knowledgeCardSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"title":               map[string]any{"type": "string"},
		"summary":             map[string]any{"type": "string"},
		"keyPoints": map[string]any{
			"type": "array",
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"content":     map[string]any{"type": "string"},
					"description": map[string]any{"type": "string"},
				},
				"required": []string{"content", "description"},
			},
		},
		"chapters": map[string]any{
			"type": "array",
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"title":     map[string]any{"type": "string"},
					"startTime": map[string]any{"type": "number"},
					"endTime":   map[string]any{"type": "number"},
					"gist":      map[string]any{"type": "string"},
				},
				"required": []string{"title", "startTime", "endTime", "gist"},
			},
		},
		"quotes": map[string]any{
			"type": "array",
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"text":      map[string]any{"type": "string"},
					"startTime": map[string]any{"type": "number"},
					"endTime":   map[string]any{"type": "number"},
				},
				"required": []string{"text", "startTime", "endTime"},
			},
		},
		"tags":               map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		"suggestedQuestions": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
	},
	"required": []string{"title", "summary", "keyPoints", "chapters", "quotes", "tags", "suggestedQuestions"},
}
