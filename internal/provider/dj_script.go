// dj_script.go DJ 串场脚本（D04 / ADR-0024 §3）。
// 受约束文本模板 + 现有 Gist 生成开场/串场/收尾脚本——首版不引入额外模型链。
// 不变量：所有脚本都是 AI 解说词（播放时以合成音色 + "AI 解说："开场白呈现）；
// 转场只基于相邻高光的 Gist 概述，不逐字朗读可核验内容；
// 不把个人判断伪装成主播原话。
package provider

import "fmt"

// DJScript 一段 DJ 串场解说词。
type DJScript struct {
	Kind              string // intro | transition | outro
	Text              string
	AnchorHighlightID string   // 关联的高光（intro/outro 为空）
	RefSegments       []string // 参考片段（Reference 语义，非 Citation）
}

// BuildDJScripts 生成 DJ 串场脚本：开场 + 相邻高光间过渡 + 收尾。
// highlights 必须已是节目顺序（D02 编排输入顺序）。
func BuildDJScripts(sourceTitle string, highlights []Highlight) []DJScript {
	var out []DJScript
	title := sourceTitle
	if title == "" {
		title = "本集节目"
	}
	if len(highlights) == 0 {
		out = append(out, DJScript{
			Kind: "intro",
			Text: fmt.Sprintf("欢迎收听《%s》的 AI 精听。这一集暂时没有可选的高光区间，你可以直接收听完整原音，或稍后再回来。", title),
		})
		return out
	}
	out = append(out, DJScript{
		Kind: "intro",
		Text: fmt.Sprintf("欢迎收听《%s》的 AI 精听。我为这一集精选了 %d 段高光区间，每段之前我会用几句话交代背景，随时可以跳回完整原音。我们从第一段开始。", title, len(highlights)),
	})
	for i := 1; i < len(highlights); i++ {
		next := highlights[i]
		out = append(out, DJScript{
			Kind:              "transition",
			Text:              fmt.Sprintf("接下来这一段的主题是：%s。听完可以直接进入下一段。", next.Gist),
			AnchorHighlightID: next.ID,
			RefSegments:       append([]string(nil), next.Citations...),
		})
	}
	out = append(out, DJScript{
		Kind: "outro",
		Text: "本集精听到这里结束。任何一段都可以单独回听；如果你有自己的理解或疑问，随手记成笔记，之后写东西的时候都能用上。",
	})
	return out
}
