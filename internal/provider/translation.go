// translation.go 对齐译文 Provider（Translation，CitedDerivative）。
// 译文按 Segment 对齐：模型只能返回 {"segmentId":"译文"} 的映射，程序丢弃
// 未知 Segment 与空译文，绝不改写原语言 Transcript，也不改写时间范围。
package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// translationSystemPrompt 对齐翻译提示词：只译给定片段，保持编号，不增删内容。
const translationSystemPrompt = `你是一个逐句对齐的播客翻译助手。用户给出带编号的片段和一个目标语言。

要求：
1. 逐个片段翻译到目标语言，保留原编号，不得合并、拆分或新增片段。
2. 不添加解释、不改变语气、不省略信息；专有名词可保留原文。
3. 数字、时间与专有名词必须与原文一致，不得推算或补全。
4. 输出纯 JSON 对象：{"segments":[{"id":"片段ID","text":"译文"}]}。
5. 不要输出 markdown 代码块或任何额外文字。`

// TranslationSet 一次翻译的结果（只含成功译出的片段）。
type TranslationSet struct {
	Segments []TranslatedSegment `json:"segments"`
	// Model/Usage 运行期字段（不进入持久化 payload）。
	Model string    `json:"-"`
	Usage TaskUsage `json:"-"`
}

// TranslatedSegment 一个片段的对齐译文。
type TranslatedSegment struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

// TranslationProvider 把一段转录按 Segment 对齐翻译到目标语言。
type TranslationProvider interface {
	Translate(segments []Segment, targetLanguage string) (*TranslationSet, error)
	Name() string
}

// ValidateTranslationSet 过滤模型输出：只保留真实存在的 Segment 与非空译文，
// 并去除重复 ID。返回结果永远不包含编造的片段身份。
func ValidateTranslationSet(set *TranslationSet, segments []Segment) []TranslatedSegment {
	if set == nil {
		return nil
	}
	valid := make(map[string]bool, len(segments))
	for _, seg := range segments {
		valid[seg.ID] = true
	}
	seen := map[string]bool{}
	out := make([]TranslatedSegment, 0, len(set.Segments))
	for _, item := range set.Segments {
		text := strings.TrimSpace(item.Text)
		if item.ID == "" || text == "" || !valid[item.ID] || seen[item.ID] {
			continue
		}
		seen[item.ID] = true
		out = append(out, TranslatedSegment{ID: item.ID, Text: text})
	}
	return out
}

// translationChunk 一次最多翻译的片段数，兼顾上下文与输出长度。
const translationChunk = 40

func translationPrompt(segments []Segment, targetLanguage string) string {
	var sb strings.Builder
	for _, seg := range segments {
		sb.WriteString(fmt.Sprintf("[%s] %s\n", seg.ID, seg.Text))
	}
	return fmt.Sprintf("目标语言：%s\n\n片段：\n%s", targetLanguage, sb.String())
}

// Translate 由 Groq 实现的对齐翻译。
func (g *GroqProvider) Translate(segments []Segment, targetLanguage string) (*TranslationSet, error) {
	if len(segments) == 0 {
		return nil, fmt.Errorf("翻译至少需要一个片段")
	}
	if strings.TrimSpace(targetLanguage) == "" {
		return nil, fmt.Errorf("翻译必须指定目标语言")
	}
	combined := &TranslationSet{}
	for start := 0; start < len(segments); start += translationChunk {
		end := start + translationChunk
		if end > len(segments) {
			end = len(segments)
		}
		content, _, model, usage, err := g.completeContextWithUsage(context.Background(), []map[string]string{
			{"role": "system", "content": translationSystemPrompt},
			{"role": "user", "content": translationPrompt(segments[start:end], targetLanguage)},
		}, "object")
		if err != nil {
			return nil, err
		}
		chunk := &TranslationSet{}
		if err := parseJSONLoose(content, chunk); err != nil {
			return nil, fmt.Errorf("解析译文失败（原始输出: %s）: %w", truncate(content, 200), err)
		}
		combined.Segments = append(combined.Segments, chunk.Segments...)
		combined.Model = model
		combined.Usage.InputUnits += usage.InputUnits
		combined.Usage.OutputUnits += usage.OutputUnits
	}
	combined.Segments = ValidateTranslationSet(combined, segments)
	if len(combined.Segments) == 0 {
		return nil, fmt.Errorf("模型没有返回任何可核验的对齐译文")
	}
	return combined, nil
}

// Translate 由 OpenAI 兼容端点实现的对齐翻译。
func (o *OpenAIProvider) Translate(segments []Segment, targetLanguage string) (*TranslationSet, error) {
	if len(segments) == 0 {
		return nil, fmt.Errorf("翻译至少需要一个片段")
	}
	if strings.TrimSpace(targetLanguage) == "" {
		return nil, fmt.Errorf("翻译必须指定目标语言")
	}
	combined := &TranslationSet{}
	for start := 0; start < len(segments); start += translationChunk {
		end := start + translationChunk
		if end > len(segments) {
			end = len(segments)
		}
		payload := map[string]any{
			"model":        o.effectiveAnalysisModel(),
			"instructions": translationSystemPrompt,
			"input":        translationPrompt(segments[start:end], targetLanguage),
			"text":         map[string]any{"format": map[string]any{"type": "json_object"}},
		}
		data, err := o.chatComplete(context.Background(), payload, "对齐翻译")
		if err != nil {
			return nil, err
		}
		var r struct {
			OutputText string `json:"output_text"`
		}
		if err := json.Unmarshal(data, &r); err != nil {
			return nil, fmt.Errorf("解析 openai 译文响应: %w", err)
		}
		chunk := &TranslationSet{}
		if err := parseJSONLoose(r.OutputText, chunk); err != nil {
			return nil, fmt.Errorf("解析译文 JSON: %w", err)
		}
		combined.Segments = append(combined.Segments, chunk.Segments...)
	}
	combined.Segments = ValidateTranslationSet(combined, segments)
	if len(combined.Segments) == 0 {
		return nil, fmt.Errorf("模型没有返回任何可核验的对齐译文")
	}
	return combined, nil
}
