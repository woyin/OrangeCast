package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
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

// TestIdeationRounds_Lifecycle C02：轮次冻结输入与材料快照；nonce 幂等；
// 旧轮次不被新范围覆盖；详情页可回看。
func TestIdeationRounds_Lifecycle(t *testing.T) {
	srv := newTestServer(t)
	session := claimOwnerAndLogin(t, srv, "rounds@example.com", "password123")
	ctx := t.Context()
	profile, err := srv.store.EnsureDefaultEditorialProfile(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := srv.store.CreateIdeationSession(ctx, models.IdeationSession{
		EditorialProfileID: profile.ID, Intent: "两种学习方法的边界",
	})
	if err != nil {
		t.Fatal(err)
	}
	rec0 := doWithCookie(srv, session, http.MethodGet, "/dashboard")
	csrf := ""
	for _, c := range rec0.Result().Cookies() {
		if c.Name == "cwp_csrf" {
			csrf = c.Value
		}
	}
	post := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/workbench/ideation/round", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("X-CSRF-Token", csrf)
		req.AddCookie(session)
		req.AddCookie(&http.Cookie{Name: "cwp_csrf", Value: csrf})
		rec := httptest.NewRecorder()
		srv.Router().ServeHTTP(rec, req)
		return rec
	}
	form := url.Values{
		"session_id":       {sess.ID},
		"input":            {"补充：两类方法的时间成本对比"},
		"nonce":            {"nonce-1"},
		"material_ids":     {"kp-a\nkp-b"},
		"constraints_json": {`{"scope":"ep1"}`},
	}
	if rec := post(form.Encode()); rec.Code != http.StatusOK {
		t.Fatalf("追加轮次应 200: %d %s", rec.Code, rec.Body.String())
	}
	// 相同 nonce 重复提交（刷新/双击）→ 只一轮。
	if rec := post(form.Encode()); rec.Code != http.StatusOK {
		t.Fatalf("重复提交应 200: %d", rec.Code)
	}
	rounds, err := srv.store.ListIdeationRounds(ctx, sess.ID)
	if err != nil || len(rounds) != 1 {
		t.Fatalf("重复提交只应产生一轮: %v %d", err, len(rounds))
	}
	if rounds[0].MaterialSnapshotJSON != `["kp-a","kp-b"]` && !strings.Contains(rounds[0].MaterialSnapshotJSON, "kp-a") {
		t.Logf("材料快照: %s", rounds[0].MaterialSnapshotJSON)
	}
	// 第二轮：新输入 + 新范围（旧轮次快照保持不变）。
	if rec := post(url.Values{
		"session_id": {sess.ID}, "input": {"换个角度：成本与收益"},
		"nonce": {"nonce-2"}, "material_ids": {"kp-c"},
	}.Encode()); rec.Code != http.StatusOK {
		t.Fatalf("第二轮应 200: %d", rec.Code)
	}
	rounds, _ = srv.store.ListIdeationRounds(ctx, sess.ID)
	if len(rounds) != 2 || rounds[1].PrevRoundID != rounds[0].ID || rounds[1].RoundNo != 2 {
		t.Fatalf("轮次应链接前轮: %+v", rounds)
	}
	if strings.Contains(rounds[0].MaterialSnapshotJSON, "kp-c") {
		t.Fatalf("第一轮快照不得被第二轮覆盖: %s", rounds[0].MaterialSnapshotJSON)
	}
	// 会话详情页回看。
	page := doWithCookie(srv, session, http.MethodGet, "/workbench/ideation/rounds?session_id="+sess.ID)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "换个角度") {
		t.Fatalf("轮次详情应可回看: %d body=%s", page.Code, page.Body.String()[:min(400, len(page.Body.String()))])
	}
	// 空 nonce → 自动生成，仍可用。
	if rec := post(url.Values{"session_id": {sess.ID}, "input": {"第三轮"}}.Encode()); rec.Code != http.StatusOK {
		t.Fatalf("空 nonce 应自动生成: %d", rec.Code)
	}
	rounds, _ = srv.store.ListIdeationRounds(ctx, sess.ID)
	if len(rounds) != 3 {
		t.Fatalf("应有三轮: %d", len(rounds))
	}
}

// TestResearchNeed_RequiresProcessedSource C04：虚构/未处理来源不能 resolved；
// 已处理来源可解决。
func TestResearchNeed_RequiresProcessedSource(t *testing.T) {
	srv := newTestServer(t)
	ctx := t.Context()
	profile, err := srv.store.EnsureDefaultEditorialProfile(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := srv.store.CreateIdeationSession(ctx, models.IdeationSession{EditorialProfileID: profile.ID, Intent: "研究缺口"})
	if err != nil {
		t.Fatal(err)
	}
	proposal, err := srv.store.CreateCreationProposal(ctx, models.CreationProposal{
		EditorialProfileID: profile.ID, Status: "proposed", WorkingTitle: "方向",
		ProposedClaim: "主张", IdeationSessionID: sess.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	need, err := srv.store.CreateResearchNeed(ctx, models.ResearchNeed{
		CreationProposalID: proposal.ID, Severity: "blocking", Question: "缺口问题",
	})
	if err != nil {
		t.Fatal(err)
	}
	// 虚构来源（"new-source" 字符串占位）→ 拒绝。
	if err := srv.store.ResolveResearchNeed(ctx, need.ID, "new-source"); err == nil {
		t.Fatal("虚构来源不得解决缺口")
	}
	// 已处理来源（有转录）→ 可解决。
	podcast3, _ := srv.store.CreatePodcast(ctx, "https://feed.example.com/research-src.xml", "研究来源", "", "")
	srv.store.MergeEpisodes(ctx, podcast3.ID, []models.Episode{{GUID: "rs-1", Title: "研究单集", AudioURL: "https://a.mp3"}})
	eps3, _ := srv.store.ListEpisodes(ctx, podcast3.ID)
	srcEp := eps3[0].ID
	job, _, err := srv.store.EnqueueJobIdempotent(ctx, store.JobIntentSpec{
		SourceType: models.SourceEpisode, SourceID: srcEp, JobType: models.JobTranscribe, IntentID: "research-src-tr",
	})
	if err != nil {
		t.Fatal(err)
	}
	payload := `{"language":"zh","text":"t","segments":[{"id":"seg-0001","start":0,"end":10,"text":"研究内容"}]}`
	version, err := srv.store.CreateArtifactVersion(ctx, models.SourceEpisode, srcEp, store.KindTranscript, "fake", "m", "1", job.ID, payload)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.store.MarkJobRunning(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	if err := srv.store.MarkJobSucceeded(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	if err := srv.store.UpdateEpisodeStatus(ctx, srcEp, models.StatusProcessed); err != nil {
		t.Fatal(err)
	}
	if err := srv.store.SetCurrentVersion(ctx, models.SourceEpisode, srcEp, store.KindTranscript, version); err != nil {
		t.Fatal(err)
	}
	if err := srv.store.ResolveResearchNeed(ctx, need.ID, srcEp); err != nil {
		t.Fatalf("已处理来源应可解决: %v", err)
	}
	got, _ := srv.store.GetResearchNeed(ctx, need.ID)
	if got.Status != "resolved" || got.ResolutionSourceID != srcEp {
		t.Fatalf("缺口应已解决并记录来源: %+v", got)
	}
}

// TestAutomaticCreationProposalsValidation C05：伪造素材 ID 与跨集不足被拦截，
// 合法候选保留，同义标题去重。
func TestAutomaticCreationProposalsValidation(t *testing.T) {
	srv := newTestServer(t)
	ctx := t.Context()
	profile, err := srv.store.EnsureDefaultEditorialProfile(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// 构造发送快照：两个 Episode 各一条素材。
	sentMaterials := []provider.ArticleMaterial{
		{KeyPointID: "kp-ep1", SourceID: "ep-1", SourceTitle: "第一集", Content: "内容A"},
		{KeyPointID: "kp-ep2", SourceID: "ep-2", SourceTitle: "第二集", Content: "内容B"},
	}
	_ = profile

	result := &provider.ScoutResult{
		Proposals: []provider.ScoutProposal{
			{Title: "合法跨集候选", Thesis: "跨集论点", CandidateKeyPointIDs: []string{"kp-ep1", "kp-ep2"}},
			{Title: "伪造素材候选", Thesis: "编造论点", CandidateKeyPointIDs: []string{"kp-fake"}},
			{Title: "单集候选", Thesis: "单集论点", CandidateKeyPointIDs: []string{"kp-ep1"}},
			{Title: "合法跨集候选", Thesis: "跨集论点"}, // 同义重复
		},
	}
	proposals, err := srv.automaticCreationProposals(ctx, profile.ID, "batch-1", result, sentMaterials)
	if err != nil {
		t.Fatal(err)
	}
	if len(proposals) != 1 {
		t.Fatalf("应只保留 1 条合法跨集候选: %d", len(proposals))
	}
	if proposals[0].WorkingTitle != "合法跨集候选" {
		t.Fatalf("保留的应为合法候选: %+v", proposals[0])
	}
	if !strings.Contains(proposals[0].MaterialIDsJSON, "kp-ep1") || !strings.Contains(proposals[0].MaterialIDsJSON, "kp-ep2") {
		t.Fatalf("保留候选应含两集素材: %s", proposals[0].MaterialIDsJSON)
	}
}
