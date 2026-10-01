// Selector 按 Provider 名称构造 provider bundle（ADR-0009）。
// key 和 baseURL 可在运行时从 SQLite settings 覆盖，支持兼容 API（DeepSeek/Together/Ollama）。
// BundleForTask 按任务级 TaskConfig 返回注入模型名的 bundle。
package provider

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/woyin/orangecast/internal/models"
)

// Selector 按 Provider 名称构造 provider bundle。
// key 和 baseURL 可在运行时从 SQLite settings 覆盖（ADR-0009 扩展）。
type Selector struct {
	embeddingKey, embeddingURL, embeddingModel string
	embeddingDimensions                        int
	groqAPIKey                                 string
	groqBaseURL                                string
	openaiAPIKey                               string
	openaiBaseURL                              string
	podAPIKey, podBaseURL, podModel            string
	narration                                  NarrationProvider // 自托管 Kokoro（独立于 groq/openai 开关，ADR-0019）
}

// NewSelector 构造一个 Selector，初始 key 来自环境变量；URL/baseURL 留空走各 Provider 默认。
// 调用 ApplySettings / ApplySettingsFrom 在运行时用 SQLite settings 覆盖 key/URL。
func NewSelector(groqAPIKey, openaiAPIKey string) *Selector {
	return &Selector{groqAPIKey: groqAPIKey, openaiAPIKey: openaiAPIKey}
}

// WithPod configures an independent text-only automatic-article connection.
func (sel *Selector) WithPod(key, baseURL, model string) *Selector {
	sel.podAPIKey, sel.podBaseURL, sel.podModel = key, baseURL, model
	return sel
}

// WithNarration 注入 Narration Provider（自托管 Kokoro，独立于 groq/openai 切换）。
func (sel *Selector) WithNarration(np NarrationProvider) *Selector {
	sel.narration = np
	return sel
}

// HasGroq 返回 Groq key 是否就绪。
func (sel *Selector) HasGroq() bool { return sel.groqAPIKey != "" }

// HasOpenAI 返回 OpenAI key 是否就绪。
func (sel *Selector) HasOpenAI() bool { return sel.openaiAPIKey != "" }

// ApplySettings 用 SQLite settings 覆盖默认 key/URL（启动时 + 保存时调）。
func (sel *Selector) ApplySettings(groqKey, groqURL, openaiKey, openaiURL string) {
	if groqKey != "" {
		sel.groqAPIKey = groqKey
	}
	if groqURL != "" {
		sel.groqBaseURL = groqURL
	}
	if openaiKey != "" {
		sel.openaiAPIKey = openaiKey
	}
	if openaiURL != "" {
		sel.openaiBaseURL = openaiURL
	}
}

// ApplySettingsFrom 从实例级 Settings 覆盖 Provider 的 key/URL（ADR-0009 扩展）。
// 收敛 main.go 启动装配与 handleSettings 保存后立即刷新的重复解引用逻辑。
func (sel *Selector) ApplySettingsFrom(st *models.Settings) {
	if st == nil {
		return
	}
	gKey, gURL, oKey, oURL := "", "", "", ""
	if st.GroqAPIKey != nil {
		gKey = *st.GroqAPIKey
	}
	if st.GroqBaseURL != nil {
		gURL = *st.GroqBaseURL
	}
	if st.OpenAIAPIKey != nil {
		oKey = *st.OpenAIAPIKey
	}
	if st.OpenAIBaseURL != nil {
		oURL = *st.OpenAIBaseURL
	}
	sel.ApplySettings(gKey, gURL, oKey, oURL)
}

// Bundle 按 provider 名返回对应的全套实现。
func (sel *Selector) Bundle(activeProvider string) (*ProviderBundle, error) {
	switch activeProvider {
	case "pod":
		if sel.podAPIKey == "" || sel.podBaseURL == "" || sel.podModel == "" {
			return nil, fmt.Errorf("自动文章 POD_* 配置不完整")
		}
		p := NewOpenAIProvider(sel.podAPIKey).WithBaseURL(sel.podBaseURL).WithModel(sel.podModel)
		p.providerName = "pod"
		return &ProviderBundle{KnowledgeArticle: p}, nil
	case "openai":
		if sel.openaiAPIKey == "" {
			return nil, fmt.Errorf("active_provider=openai 但 OPENAI_API_KEY 未配置")
		}
		oa := NewOpenAIProvider(sel.openaiAPIKey)
		if sel.openaiBaseURL != "" {
			oa.baseURL = sel.openaiBaseURL
		}
		return &ProviderBundle{Transcription: oa, Analysis: oa, QA: oa, Writer: oa, Scout: oa, Curator: oa, EvidenceReviewer: oa, ClaimReviewer: oa, StyleEditor: oa, Highlight: oa, Paraphrase: oa, StudyChat: oa, RefChecker: oa, Narration: sel.narration, DigestWriter: oa, DigestSearch: oa, DigestRewriter: oa}, nil
	case "groq", "":
		if sel.groqAPIKey == "" {
			return nil, fmt.Errorf("active_provider=groq 但 GROQ_API_KEY 未配置")
		}
		g := NewGroqProvider(sel.groqAPIKey)
		if sel.groqBaseURL != "" {
			g.baseURL = sel.groqBaseURL
		}
		return &ProviderBundle{Transcription: g, Analysis: g, QA: g, Writer: g, Scout: g, Curator: g, EvidenceReviewer: g, ClaimReviewer: g, StyleEditor: g, Highlight: g, Paraphrase: g, StudyChat: g, RefChecker: g, Narration: sel.narration, DigestWriter: g, DigestSearch: g, DigestRewriter: g}, nil
	default:
		return nil, fmt.Errorf("未知 provider: %s", activeProvider)
	}
}

// TaskConfig 每个任务的 Provider + Model 配置（来自 settings）。
type TaskConfig struct {
	Provider      string
	Model         string // 空则用该 provider 的默认模型
	Transcription bool   // Audio model overrides never alter the chat route.
}

// EffectiveTaskModel returns the exact model selected by a task configuration,
// including provider defaults. Callers persist this value in audit records so
// a blank setting never becomes an untraceable model choice.
func EffectiveTaskModel(tc TaskConfig) string {
	if tc.Model != "" {
		return tc.Model
	}
	switch tc.Provider {
	case "openai":
		return openaiAnalysisModel
	case "groq", "":
		return groqAnalysisModel
	default:
		return ""
	}
}

// BundleForTask 按 TaskConfig 返回配置好的 bundle（模型名注入）。
func (sel *Selector) BundleForTask(tc TaskConfig) (*ProviderBundle, error) {
	bundle, err := sel.Bundle(tc.Provider)
	if err != nil {
		return nil, err
	}
	if tc.Transcription {
		switch p := bundle.Transcription.(type) {
		case *GroqProvider:
			bundle.Transcription = p.WithTranscriptionModel(tc.Model)
		case *OpenAIProvider:
			bundle.Transcription = p.WithTranscriptionModel(tc.Model)
		default:
			return nil, fmt.Errorf("provider %s 不支持音频转录", tc.Provider)
		}
		return bundle, nil
	}
	if tc.Provider == "pod" && tc.Model != "" {
		if p, ok := bundle.KnowledgeArticle.(*OpenAIProvider); ok {
			bundle.KnowledgeArticle = p.WithModel(tc.Model)
		}
	}
	if tc.Model != "" {
		switch tc.Provider {
		case "groq":
			if g, ok := bundle.Analysis.(*GroqProvider); ok {
				custom := g.WithModel(tc.Model)
				bundle.Analysis = custom
				bundle.Highlight = custom
				bundle.QA = custom
				bundle.Writer = custom
				bundle.Scout = custom
				bundle.EvidenceReviewer = custom
				bundle.ClaimReviewer = custom
				bundle.StyleEditor = custom
				bundle.Curator = custom
				bundle.Paraphrase = custom
				bundle.StudyChat = custom
				bundle.RefChecker = custom
			}
		case "openai":
			if o, ok := bundle.Analysis.(*OpenAIProvider); ok {
				custom := o.WithModel(tc.Model)
				bundle.Analysis = custom
				bundle.Highlight = custom
				bundle.QA = custom
				bundle.Writer = custom
				bundle.Scout = custom
				bundle.EvidenceReviewer = custom
				bundle.ClaimReviewer = custom
				bundle.StyleEditor = custom
				bundle.Curator = custom
				bundle.Paraphrase = custom
				bundle.StudyChat = custom
				bundle.RefChecker = custom
			}
		}
	}
	return bundle, nil
}

// TranscriptionConnectionFingerprint identifies an endpoint without storing its
// credentials or URL in an admitted voice task.
func TranscriptionConnectionFingerprint(providerName, baseURL string) string {
	if baseURL == "" {
		if providerName == "openai" {
			baseURL = openaiBaseURL
		} else {
			baseURL = groqBaseURL
		}
	}
	sum := sha256.Sum256([]byte(providerName + ":" + strings.TrimRight(baseURL, "/")))
	return hex.EncodeToString(sum[:])
}

// TranscriptionConnectionID returns this selector's currently resolved route.
func (sel *Selector) TranscriptionConnectionID(providerName string) string {
	base := sel.groqBaseURL
	if providerName == "openai" {
		base = sel.openaiBaseURL
	}
	return TranscriptionConnectionFingerprint(providerName, base)
}

// WithEmbedding sets only the independent learning-index route. It does no I/O.
func (sel *Selector) WithEmbedding(key, baseURL, model string, dimensions int) *Selector {
	sel.embeddingKey, sel.embeddingURL, sel.embeddingModel, sel.embeddingDimensions = key, baseURL, model, dimensions
	return sel
}

// Embedding returns an explicitly configured client; no other provider is a fallback.
func (sel *Selector) Embedding() (*EmbeddingClient, error) {
	return NewEmbeddingClient(sel.embeddingKey, sel.embeddingURL, sel.embeddingModel, sel.embeddingDimensions)
}
