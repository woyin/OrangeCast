package mcpserver

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
	"github.com/woyin/orangecast/internal/testdb"
)

func seedLibrary(t *testing.T) (*store.Store, string) {
	t.Helper()
	s, err := testdb.Open(filepath.Join(t.TempDir(), "t.db"), "../store/migrations", store.Open)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	p, _ := s.CreatePodcast(ctx, "https://f.xml", "Pod", "", "")
	s.MergeEpisodes(ctx, p.ID, []models.Episode{{GUID: "g1", Title: "好莱坞沟通课", AudioURL: "https://a.mp3"}})
	eps, _ := s.ListEpisodes(ctx, p.ID)
	id := eps[0].ID
	job, _ := s.EnqueueJob(ctx, models.SourceEpisode, id, models.JobTranscribe)
	vt, _ := s.CreateArtifactVersion(ctx, models.SourceEpisode, id, store.KindTranscript, "groq", "m", "1", job.ID,
		`{"segments":[{"id":"seg-0001","start":75,"end":80,"text":"通胀是物价上升"}]}`)
	s.SetCurrentVersion(ctx, models.SourceEpisode, id, store.KindTranscript, vt)
	card := `{"title":"通胀解析","summary":{"text":"概览","citations":["seg-0001"]},"keyPoints":[{"content":"要点","description":"d","citations":["seg-0001"]}],"chapters":[],"quotes":[{"text":"通胀是物价上升","citations":["seg-0001"]}],"tags":["经济","宏观"]}`
	vc, _ := s.CreateArtifactVersion(ctx, models.SourceEpisode, id, store.KindKnowledgeCard, "groq", "m", "1", job.ID, card)
	s.SetCurrentVersion(ctx, models.SourceEpisode, id, store.KindKnowledgeCard, vc)
	if err := s.IndexSearch(ctx, models.SourceEpisode, id, "好莱坞沟通课", "概览", []provider.Segment{{ID: "seg-0001", Start: 75, End: 80, Text: "通胀是物价上升"}}); err != nil {
		t.Fatal(err)
	}
	return s, id
}

func call(t *testing.T, tool Tool, args string) (string, error) {
	t.Helper()
	return tool.Run(context.Background(), json.RawMessage(args))
}

func toolByName(t *testing.T, tools []Tool, name string) Tool {
	t.Helper()
	for _, tl := range tools {
		if tl.Name == name {
			return tl
		}
	}
	t.Fatalf("no tool %s", name)
	return Tool{}
}

func TestLibraryTools(t *testing.T) {
	s, id := seedLibrary(t)
	tools := LibraryTools(s, "https://oc.example/")

	search := toolByName(t, tools, "search_library")
	out, err := call(t, search, `{"query":"通胀","limit":5}`)
	if err != nil || !strings.Contains(out, "好莱坞沟通课") || !strings.Contains(out, "https://oc.example/sources/episode/"+id) {
		t.Fatalf("search: %q %v", out, err)
	}
	if !strings.Contains(out, "@ 1:15") || !strings.Contains(out, "?t=75.0") {
		t.Errorf("命中片段应带时间与跳转链接: %q", out)
	}
	if out, _ := call(t, search, `{"query":"完全无关的量子计算"}`); !strings.Contains(out, "没有找到") {
		t.Errorf("无命中: %q", out)
	}
	for _, bad := range []string{`{}`, `{"query":"  "}`, `nope`} {
		if _, err := call(t, search, bad); err == nil {
			t.Errorf("search %s 应报参数错误", bad)
		}
	}

	get := toolByName(t, tools, "get_source")
	out, err = call(t, get, `{"source_type":"episode","source_id":"`+id+`"}`)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# 通胀解析", "## 摘要", "概览", "关键要点（AI 归纳）", "金句（逐字原文）", "> 通胀是物价上升", "标签：经济、宏观"} {
		if !strings.Contains(out, want) {
			t.Errorf("get_source 应含 %q: %s", want, out)
		}
	}
	for _, bad := range []string{`{"source_type":"episode"}`, `nope`, `{"source_type":"episode","source_id":"missing"}`} {
		if _, err := call(t, get, bad); err == nil {
			t.Errorf("get_source %s 应报错", bad)
		}
	}
	// 卡片数据损坏。
	if _, err := s.DB.Exec(`UPDATE artifact_versions SET payload='{bad' WHERE kind=?`, store.KindKnowledgeCard); err != nil {
		t.Fatal(err)
	}
	if _, err := call(t, get, `{"source_type":"episode","source_id":"`+id+`"}`); err == nil || !strings.Contains(err.Error(), "损坏") {
		t.Errorf("损坏: %v", err)
	}
}

func TestListSnipsTool(t *testing.T) {
	s, id := seedLibrary(t)
	ctx := context.Background()
	tools := LibraryTools(s, "https://oc.example")
	list := toolByName(t, tools, "list_snips")
	if out, err := call(t, list, ``); err != nil || !strings.Contains(out, "还没有") {
		t.Fatalf("空: %q %v", out, err)
	}
	if err := s.UpsertEvidenceAudio(ctx, models.SourceEpisode, id, "a.mp3", "mp3", 100, "sha-mcp"); err != nil {
		t.Fatal(err)
	}
	snap, err := s.FreezeSourceSnapshot(ctx, models.SourceEpisode, id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateLearningExcerpt(ctx, snap.ID, []string{"seg-0001"}); err != nil {
		t.Fatal(err)
	}
	out, err := call(t, list, `{"limit":3}`)
	if err != nil || !strings.Contains(out, "通胀是物价上升") || !strings.Contains(out, "好莱坞沟通课") || !strings.Contains(out, "?t=75.0") {
		t.Errorf("list_snips: %q %v", out, err)
	}
	s.DB.Close()
	if _, err := call(t, list, `{}`); err == nil {
		t.Error("库关闭后应报错")
	}
	if _, err := call(t, toolByName(t, tools, "search_library"), `{"query":"通胀"}`); err == nil {
		t.Error("库关闭后检索应报错")
	}
}

func TestClampAndLabels(t *testing.T) {
	if clamp(0, 8, 20) != 8 || clamp(-1, 8, 20) != 8 || clamp(5, 8, 20) != 5 || clamp(99, 8, 20) != 20 {
		t.Error("clamp")
	}
	if timeLabel(65, false) != "" || timeLabel(65, true) != "@ 1:05" {
		t.Error("timeLabel")
	}
	if startOf(store.KnowledgeSearchHit{Position: 9}) != 0 || startOf(store.KnowledgeSearchHit{Position: 9, SegmentID: "s"}) != 9 {
		t.Error("startOf")
	}
	if got := renderCard(&provider.KnowledgeCard{Title: "T"}, "u"); got != "# T\nu\n\n" {
		t.Errorf("minimal card: %q", got)
	}
}
