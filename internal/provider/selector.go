package provider

import (
	"fmt"
)

// Selector 运行时实时选择 provider bundle（第 9 题：处理时读 active_provider，非入队写死）。
type Selector struct {
	groqAPIKey   string
	openaiAPIKey string
}

func NewSelector(groqAPIKey, openaiAPIKey string) *Selector {
	return &Selector{groqAPIKey: groqAPIKey, openaiAPIKey: openaiAPIKey}
}

// Bundle 根据 activeProvider 返回对应的全套 provider 实现。
// groq 为主力，openai 为兜底。
func (sel *Selector) Bundle(activeProvider string) (*ProviderBundle, error) {
	switch activeProvider {
	case "openai":
		if sel.openaiAPIKey == "" {
			return nil, fmt.Errorf("active_provider=openai 但 OPENAI_API_KEY 未配置")
		}
		oa := NewOpenAIProvider(sel.openaiAPIKey)
		return &ProviderBundle{Transcription: oa, Analysis: oa, QA: oa}, nil
	case "groq", "":
		if sel.groqAPIKey == "" {
			return nil, fmt.Errorf("active_provider=groq 但 GROQ_API_KEY 未配置")
		}
		g := NewGroqProvider(sel.groqAPIKey)
		return &ProviderBundle{Transcription: g, Analysis: g, QA: g}, nil
	default:
		return nil, fmt.Errorf("未知 provider: %s", activeProvider)
	}
}

// 默认导出便捷函数，供测试或启动期检查 key 是否就绪。
func (sel *Selector) HasGroq() bool   { return sel.groqAPIKey != "" }
func (sel *Selector) HasOpenAI() bool { return sel.openaiAPIKey != "" }
