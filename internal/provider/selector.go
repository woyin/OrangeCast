package provider

import (
	"fmt"
)

// Selector 按 Provider 名称构造 provider bundle。
// ADR-0009：不再存在全局 active_provider 切换；Groq 是默认零成本 Provider，
// 付费 Provider 仅在对单次 ProcessingJob 尝试显式授权时经此处构造（授权随任务记录）。
type Selector struct {
	groqAPIKey   string
	openaiAPIKey string
}

func NewSelector(groqAPIKey, openaiAPIKey string) *Selector {
	return &Selector{groqAPIKey: groqAPIKey, openaiAPIKey: openaiAPIKey}
}

// Bundle 按 provider 名返回对应的全套实现。
func (sel *Selector) Bundle(activeProvider string) (*ProviderBundle, error) {
	switch activeProvider {
	case "openai":
		if sel.openaiAPIKey == "" {
			return nil, fmt.Errorf("active_provider=openai 但 OPENAI_API_KEY 未配置")
		}
		oa := NewOpenAIProvider(sel.openaiAPIKey)
		return &ProviderBundle{Transcription: oa, Analysis: oa, QA: oa, Highlight: oa}, nil
	case "groq", "":
		if sel.groqAPIKey == "" {
			return nil, fmt.Errorf("active_provider=groq 但 GROQ_API_KEY 未配置")
		}
		g := NewGroqProvider(sel.groqAPIKey)
		return &ProviderBundle{Transcription: g, Analysis: g, QA: g, Highlight: g}, nil
	default:
		return nil, fmt.Errorf("未知 provider: %s", activeProvider)
	}
}

// 默认导出便捷函数，供测试或启动期检查 key 是否就绪。
func (sel *Selector) HasGroq() bool   { return sel.groqAPIKey != "" }
func (sel *Selector) HasOpenAI() bool { return sel.openaiAPIKey != "" }

// TaskConfig 每个任务的 Provider + Model 配置（来自 settings）。
type TaskConfig struct {
	Provider string
	Model    string // 空则用该 provider 的默认模型
}

// BundleForTask 按 TaskConfig 返回配置好的 bundle（模型名注入）。
func (sel *Selector) BundleForTask(tc TaskConfig) (*ProviderBundle, error) {
	bundle, err := sel.Bundle(tc.Provider)
	if err != nil {
		return nil, err
	}
	if tc.Model != "" {
		// 用指定模型覆盖（不修改原 bundle）
		switch tc.Provider {
		case "groq":
			if g, ok := bundle.Analysis.(*GroqProvider); ok {
				custom := g.WithModel(tc.Model)
				bundle.Analysis = custom
				bundle.Highlight = custom
				bundle.QA = custom
			}
		case "openai":
			if o, ok := bundle.Analysis.(*OpenAIProvider); ok {
				custom := o.WithModel(tc.Model)
				bundle.Analysis = custom
				bundle.Highlight = custom
				bundle.QA = custom
			}
		}
	}
	return bundle, nil
}
