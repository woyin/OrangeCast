package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
)

// seedC01Keypoints 一次索引建立单集全部关键观点（K03 协调语义下，
// 多次索引会移除未匹配重点），将指定内容设为 ready 并返回内容 → ID 映射。
func seedC01Keypoints(t *testing.T, srv *Server, epID string, specs []struct {
	Content string
	SegID   string
	Ready   bool
}) map[string]string {
	t.Helper()
	var kps []provider.KeyPoint
	segSet := map[string]bool{}
	for _, sp := range specs {
		kps = append(kps, provider.KeyPoint{Content: sp.Content, Citations: []string{sp.SegID}})
		segSet[sp.SegID] = true
	}
	var segments []provider.Segment
	for id := range segSet {
		segments = append(segments, provider.Segment{ID: id, Start: 0, End: 5, Text: "x"})
	}
	card := &provider.KnowledgeCard{KeyPoints: kps}
	if _, err := srv.store.IndexKeyPoints(t.Context(), models.SourceEpisode, epID, "ep", 1, card, segments); err != nil {
		t.Fatal(err)
	}
	rows, _, err := srv.store.ListKeyPoints(t.Context(), 1, 50)
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]string{}
	for _, kp := range rows {
		ids[kp.Content] = kp.ID
	}
	for _, sp := range specs {
		if sp.Ready {
			if err := srv.store.SetKeyPointQualityStatus(t.Context(), ids[sp.Content], models.KeyPointReady); err != nil {
				t.Fatal(err)
			}
		}
	}
	return ids
}

// TestCreationSelection_EndToEnd C01：默认画像、跨来源素材与个人笔记进同一选择、
// 不合格材料显式排除、确认保留用户输入、全程无付费调用。
func TestCreationSelection_EndToEnd(t *testing.T) {
	srv := newTestServer(t)
	session := claimOwnerAndLogin(t, srv, "csel@example.com", "password123")
	ctx := t.Context()

	// 来源一：ready 观点 + needs_review 观点（后者应被排除）。
	podcast, _ := srv.store.CreatePodcast(ctx, "https://feed.example.com/csel1.xml", "C1", "", "")
	srv.store.MergeEpisodes(ctx, podcast.ID, []models.Episode{{GUID: "c1", Title: "E1", AudioURL: "https://a.mp3"}})
	eps1, _ := srv.store.ListEpisodes(ctx, podcast.ID)
	ids1 := seedC01Keypoints(t, srv, eps1[0].ID, []struct {
		Content string
		SegID   string
		Ready   bool
	}{
		{"有依据的观点一", "seg-0001", true},
		{"待审核的观点", "seg-0002", false},
	})
	kpReady, kpReview := ids1["有依据的观点一"], ids1["待审核的观点"]

	// 来源二：ready 观点（跨来源）+ 被排除观点。
	podcast2, _ := srv.store.CreatePodcast(ctx, "https://feed.example.com/csel2.xml", "C2", "", "")
	srv.store.MergeEpisodes(ctx, podcast2.ID, []models.Episode{{GUID: "c2", Title: "E2", AudioURL: "https://a.mp3"}})
	eps2, _ := srv.store.ListEpisodes(ctx, podcast2.ID)
	ids2 := seedC01Keypoints(t, srv, eps2[0].ID, []struct {
		Content string
		SegID   string
		Ready   bool
	}{
		{"另一集的观点", "seg-0001", true},
		{"被排除的观点", "seg-0002", true},
	})
	kpReady2, kpExcluded := ids2["另一集的观点"], ids2["被排除的观点"]
	profile, err := srv.store.EnsureDefaultEditorialProfile(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.store.SetEditorialRelevance(ctx, models.EditorialRelevance{
		EditorialProfileID: profile.ID, KeyPointID: kpExcluded,
		Assessment: "relevant", OwnerOverride: "excluded", Rationale: "Owner 排除",
	}); err != nil {
		t.Fatal(err)
	}

	// 个人笔记（OwnerReflection）。
	note, err := srv.store.CreateOwnerNote(ctx, models.OwnerNote{
		SourceType: string(models.SourceEpisode), SourceID: eps1[0].ID,
		Kind: "owner_reflection", Content: "我自己的理解：样本偏差要警惕",
	})
	if err != nil {
		t.Fatal(err)
	}

	// 提交选择：2 合法观点 + 1 待审核 + 1 被排除 + 1 笔记 + 1 不存在材料。
	rec0 := doWithCookie(srv, session, http.MethodGet, "/dashboard")
	csrf := ""
	for _, c := range rec0.Result().Cookies() {
		if c.Name == "cwp_csrf" {
			csrf = c.Value
		}
	}
	form := url.Values{
		"_csrf":        {csrf},
		"title":        {"咖啡与健康素材"},
		"material_ids": {strings.Join([]string{kpReady, kpReady2, kpReview, kpExcluded}, "\n")},
		"note_ids":     {note.ID + "\n" + "note-missing"},
	}
	req := httptest.NewRequest(http.MethodPost, "/creation/selections", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-CSRF-Token", csrf)
	req.AddCookie(session)
	req.AddCookie(&http.Cookie{Name: "cwp_csrf", Value: csrf})
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("保存应 303: %d %s", rec.Code, rec.Body.String())
	}

	selections, err := srv.store.ListCreationSelections(ctx, profile.ID)
	if err != nil || len(selections) != 1 {
		t.Fatalf("应有 1 条选择: %v %+v", err, selections)
	}
	sel := selections[0]
	if sel.EditorialProfileID != profile.ID {
		t.Fatalf("应复用默认画像: %+v", sel)
	}
	if len(sel.MaterialIDs) != 2 {
		t.Fatalf("合格观点应为 2 条: materials=%v notes=%v excluded=%+v", sel.MaterialIDs, sel.NoteIDs, sel.Excluded)
	}
	if len(sel.NoteIDs) != 1 || sel.NoteIDs[0] != note.ID {
		t.Fatalf("个人笔记应保留: %+v", sel.NoteIDs)
	}
	if len(sel.Excluded) != 3 {
		t.Fatalf("应显式记录 2 条排除: %+v", sel.Excluded)
	}
	excludedReasons := ""
	for _, ex := range sel.Excluded {
		excludedReasons += ex.Reason
	}
	if !strings.Contains(excludedReasons, "待审核") && !strings.Contains(excludedReasons, "需 ready") {
		t.Fatalf("质量原因应显式: %s", excludedReasons)
	}
	if !strings.Contains(excludedReasons, "排除") {
		t.Fatalf("Owner 排除原因应显式: %s", excludedReasons)
	}

	// 页面渲染选择与排除原因。
	page := doWithCookie(srv, session, http.MethodGet, "/creation/selections")
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "咖啡与健康素材") || !strings.Contains(page.Body.String(), "被排除材料") {
		t.Fatalf("选择页应显示选择与排除原因")
	}
}
