package provider

import (
	"strings"
	"testing"
)

// TestBuildDJScripts_Contract D04：开场/过渡/收尾结构、过渡基于下一高光 Gist。
func TestBuildDJScripts_Contract(t *testing.T) {
	// 空高光：仅开场，提示直接听原音。
	only := BuildDJScripts("调试节目", nil)
	if len(only) != 1 || only[0].Kind != "intro" || !strings.Contains(only[0].Text, "完整原音") {
		t.Fatalf("空高光应只有开场提示: %+v", only)
	}
	hs := []Highlight{
		{ID: "h1", Gist: "开场观点", Citations: []string{"seg-0001"}},
		{ID: "h2", Gist: "核心论证", Citations: []string{"seg-0003"}},
	}
	scripts := BuildDJScripts("我的节目", hs)
	kinds := []string{}
	for _, sc := range scripts {
		kinds = append(kinds, sc.Kind)
	}
	if len(scripts) != 3 || kinds[0] != "intro" || kinds[1] != "transition" || kinds[2] != "outro" {
		t.Fatalf("两高光应为 intro+过渡+outro 共 3 段: %v", kinds)
	}
	if !strings.Contains(scripts[1].Text, "核心论证") {
		t.Fatalf("过渡应基于下一高光 Gist: %s", scripts[1].Text)
	}
	if scripts[1].AnchorHighlightID != "h2" || len(scripts[1].RefSegments) != 1 || scripts[1].RefSegments[0] != "seg-0003" {
		t.Fatalf("过渡应引用下一高光身份: %+v", scripts[1])
	}
	if !strings.Contains(scripts[0].Text, "我的节目") || !strings.Contains(scripts[0].Text, "2 段") {
		t.Fatalf("开场应说明节目与范围: %s", scripts[0].Text)
	}
	if !strings.Contains(scripts[2].Text, "回听") || !strings.Contains(scripts[2].Text, "笔记") {
		t.Fatalf("收尾应提示回听与记笔记: %s", scripts[3].Text)
	}
}
