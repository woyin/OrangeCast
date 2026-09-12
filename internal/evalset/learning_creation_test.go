package evalset

import (
	"reflect"
	"strings"
	"testing"

	"github.com/woyin/orangecast/internal/provider"
)

// TestLearningFixtures_RepeatableLoad 夹具两次加载必须逐值相等（可重复加载）。
func TestLearningFixtures_RepeatableLoad(t *testing.T) {
	a := LearningEpisodes()
	b := LearningEpisodes()
	if len(a) == 0 {
		t.Fatal("学习样本集不能为空")
	}
	if !reflect.DeepEqual(a, b) {
		t.Fatal("两次加载结果不一致：夹具不是确定性的")
	}
	if !reflect.DeepEqual(CrossEpisodeCases(), CrossEpisodeCases()) {
		t.Fatal("跨集用例两次加载结果不一致")
	}
	if !reflect.DeepEqual(OwnerNoteCases(), OwnerNoteCases()) {
		t.Fatal("笔记用例两次加载结果不一致")
	}
}

// TestLearningFixtures_ScenarioCoverage 必须覆盖计划要求的场景。
func TestLearningFixtures_ScenarioCoverage(t *testing.T) {
	scenarios := map[string]bool{}
	languages := map[string]bool{}
	for _, ep := range LearningEpisodes() {
		scenarios[ep.Scenario] = true
		languages[ep.Language] = true
	}
	for _, want := range []string{"long_interview", "info_dense", "multi_topic_ads", "conflict_pro", "conflict_con"} {
		if !scenarios[want] {
			t.Errorf("缺少场景 %s 的样本", want)
		}
	}
	if !languages["zh"] || !languages["en"] {
		t.Error("样本必须同时覆盖中文与英文")
	}
}

// TestLearningFixtures_Integrity 完整性校验必须全绿（引用可追溯、结构完备）。
func TestLearningFixtures_Integrity(t *testing.T) {
	issues := CheckLearningFixtures()
	for _, is := range issues {
		t.Errorf("%s: [%s] %s", is.SampleID, is.Kind, is.Detail)
	}
}

// TestLearningFixtures_IntegrityDetectsBreakage 故意破坏夹具时校验必须能发现。
func TestLearningFixtures_IntegrityDetectsBreakage(t *testing.T) {
	broken := EpisodeFixture{
		ID: "broken-01", Language: "zh", Scenario: "multi_topic_ads", Source: "self-made",
		Segments: []provider.Segment{{ID: "b-seg-1", Start: 0, End: 5, Text: "内容"}},
		CorePoints: []CorePoint{{
			Claim:           "无依据观点",
			SupportSegments: []string{"missing-seg"}, // 不存在的支持区间
		}}, // 且缺 Qualifiers/NotImplied、无广告、无重复组
	}
	issues := checkEpisodeFixture(broken)
	if len(issues) == 0 {
		t.Fatal("破坏的夹具必须产生校验问题")
	}
	kinds := map[string]bool{}
	for _, is := range issues {
		kinds[is.Kind] = true
	}
	for _, want := range []string{"citation", "schema"} {
		if !kinds[want] {
			t.Errorf("破坏夹具应产生 %s 类问题，实际 %v", want, issues)
		}
	}
}

// TestOwnerNoteCases_Identity 个人理解不得标为证据；来源笔记必须可作证据。
func TestOwnerNoteCases_Identity(t *testing.T) {
	cases := OwnerNoteCases()
	if len(cases) == 0 {
		t.Fatal("至少需要一个笔记身份用例")
	}
	kinds := map[string]bool{}
	for _, nc := range cases {
		kinds[nc.Kind] = true
		if nc.Kind == "owner_reflection" && nc.IsEvidence {
			t.Errorf("%s: OwnerReflection 不得伪装为证据", nc.ID)
		}
		if nc.Kind == "source_note" && !nc.IsEvidence {
			t.Errorf("%s: SourceNote 应可作来源素材", nc.ID)
		}
		if !strings.Contains(nc.Content, "") {
			t.Errorf("%s: 内容为空", nc.ID)
		}
	}
	if !kinds["source_note"] || !kinds["owner_reflection"] {
		t.Errorf("笔记用例必须同时覆盖两类身份：%v", kinds)
	}
}

// TestCrossEpisodeCases_Contract 跨集用例必须绑定对立两集并写明保留冲突的期望。
func TestCrossEpisodeCases_Contract(t *testing.T) {
	for _, cs := range CrossEpisodeCases() {
		if len(cs.EpisodeIDs) < 2 {
			t.Errorf("%s: 至少两集", cs.ID)
		}
		if !strings.Contains(cs.Expectation, "不得合并") {
			t.Errorf("%s: 期望必须要求保留冲突不合并：%s", cs.ID, cs.Expectation)
		}
	}
}
