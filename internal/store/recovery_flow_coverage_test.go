package store

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
)

func TestIdeationRoundDiagnosisPromotionLifecycle(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	profile := seedWorkspaceProfile(t, s, "诊断流程画像")
	session, err := s.CreateIdeationSession(ctx, models.IdeationSession{
		EditorialProfileID: profile.ID, Intent: "比较两种学习方式", ConstraintsJSON: `{}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.AddIdeationRound(ctx, session.ID, "", "问题", `{}`, `[]`); !errors.Is(err, ErrInvalidEditorialState) {
		t.Fatalf("轮次必须带客户端幂等键: %v", err)
	}
	materialSnapshot := `[{"id":"kp-1","content":"材料"}]`
	round, created, err := s.AddIdeationRound(ctx, session.ID, "round-1", "哪种方式更适合？", `{}`, materialSnapshot)
	if err != nil || !created {
		t.Fatalf("首轮应创建: round=%+v created=%v err=%v", round, created, err)
	}
	again, created, err := s.AddIdeationRound(ctx, session.ID, "round-1", "重复提交", `{}`, materialSnapshot)
	if err != nil || created || again.ID != round.ID {
		t.Fatalf("同 nonce 应复用首轮: again=%+v created=%v err=%v", again, created, err)
	}
	second, created, err := s.AddIdeationRound(ctx, session.ID, "round-2", "还缺什么证据？", `{}`, materialSnapshot)
	if err != nil || !created || second.PrevRoundID != round.ID {
		t.Fatalf("第二轮必须链接首轮: second=%+v created=%v err=%v", second, created, err)
	}
	rounds, err := s.ListIdeationRounds(ctx, session.ID)
	if err != nil || len(rounds) != 2 || rounds[1].ID != second.ID {
		t.Fatalf("轮次历史必须按顺序恢复: rounds=%+v err=%v", rounds, err)
	}
	if got, err := s.GetIdeationRoundByID(ctx, round.ID); err != nil || got.ClientNonce != "round-1" {
		t.Fatalf("按 ID 读取轮次失败: got=%+v err=%v", got, err)
	}

	diagnosis := `{"gaps":["缺结果数据"],"proposedClaims":[{"claim":"间隔复习更适合长期记忆","materialIds":["kp-1"]}]}`
	md, err := s.CreateMaterialDiagnosisForRound(ctx, session.ID, round.ID, diagnosis, materialSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MarkIdeationRoundDiagnosed(ctx, round.ID, md.ID, false); err != nil {
		t.Fatal(err)
	}
	if got, err := s.GetMaterialDiagnosis(ctx, md.ID); err != nil || got.DiagnosisJSON != diagnosis {
		t.Fatalf("诊断必须可恢复: got=%+v err=%v", got, err)
	}
	proposal, err := s.PromoteDiagnosisClaim(ctx, session.ID, round.ID, 0)
	if err != nil || proposal.IdeationRoundID != round.ID || proposal.MaterialIDsJSON != `["kp-1"]` {
		t.Fatalf("诊断主张提升失败: proposal=%+v err=%v", proposal, err)
	}
	duplicate, err := s.PromoteDiagnosisClaim(ctx, session.ID, round.ID, 0)
	if err != nil || duplicate.ID != proposal.ID {
		t.Fatalf("重复提升必须幂等: duplicate=%+v err=%v", duplicate, err)
	}
	if _, err := s.PromoteDiagnosisClaim(ctx, session.ID, round.ID, 9); !errors.Is(err, ErrInvalidEditorialState) {
		t.Fatalf("越界候选必须拒绝: %v", err)
	}
	if err := s.MarkIdeationRoundDiagnosed(ctx, second.ID, "", true); err != nil {
		t.Fatal(err)
	}
}

func TestCreationSelectionListsAndFreezesEligibleMaterials(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	profile := seedWorkspaceProfile(t, s, "选材画像")
	episodeID := seedTranscriptEpisode(t, s, "selection-flow")
	keyPoint, err := s.CreateManualKeyPoint(ctx, KeyPointRow{
		SourceType: models.SourceEpisode, SourceID: episodeID, SourceTitle: "选材单集",
		Content: "可复用的重点", CitationsJSON: `["seg-1"]`, TimeStart: 0, TimeEnd: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	note, err := s.CreateOwnerNote(ctx, models.OwnerNote{
		SourceType: string(models.SourceEpisode), SourceID: episodeID, Kind: "owner_reflection",
		Content: "我的理解", CitationsJSON: `[]`, ReferencesJSON: `[]`,
	})
	if err != nil {
		t.Fatal(err)
	}
	selection, err := s.SaveCreationSelection(ctx, &models.CreationSelection{
		EditorialProfileID: profile.ID, Title: "本轮素材",
		MaterialIDs: []string{keyPoint.ID, "missing-keypoint"}, NoteIDs: []string{note.ID, "missing-note"},
		Scope: []models.CreationSelectionScope{{SourceType: string(models.SourceEpisode), SourceID: episodeID}},
	})
	if err != nil || len(selection.MaterialIDs) != 1 || len(selection.NoteIDs) != 1 || len(selection.Excluded) != 2 {
		t.Fatalf("选择应保留合格材料并解释排除项: selection=%+v err=%v", selection, err)
	}
	listed, err := s.ListCreationSelections(ctx, profile.ID)
	if err != nil || len(listed) != 1 || listed[0].ID != selection.ID {
		t.Fatalf("画像选材历史读取失败: listed=%+v err=%v", listed, err)
	}
	snapshot, err := s.MaterialSnapshotFromSelections(ctx, []string{selection.ID, "missing-selection"})
	if err != nil || len(snapshot) != 5 {
		t.Fatalf("快照应包含重点、笔记、选择时排除项和缺失选择: snapshot=%+v err=%v", snapshot, err)
	}
	valid := map[string]bool{}
	for _, item := range snapshot {
		if item.Error == "" {
			valid[item.Kind] = true
		}
	}
	if !valid["keypoint"] || !valid["note"] {
		t.Fatalf("合格重点与个人笔记必须冻结正文身份: %+v", snapshot)
	}
}

func TestProposalBatchDecisionsReleaseBackpressure(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	profile := seedWorkspaceProfile(t, s, "提案决策画像")
	batch, created, err := s.ReserveAutomaticProposalBatch(ctx, models.ProposalBatch{
		EditorialProfileID: profile.ID, IdempotencyKey: "decision-flow", MaterialSnapshotJSON: `["kp"]`,
	})
	if err != nil || !created {
		t.Fatalf("批次占位失败: batch=%+v created=%v err=%v", batch, created, err)
	}
	proposals := []models.CreationProposal{
		{WorkingTitle: "接受", ProposedClaim: "主张 A", MaterialIDsJSON: `["kp"]`},
		{WorkingTitle: "稍后", ProposedClaim: "主张 B", MaterialIDsJSON: `["kp"]`},
		{WorkingTitle: "拒绝", ProposedClaim: "主张 C", MaterialIDsJSON: `["kp"]`},
	}
	if err := s.FinalizeAutomaticProposalBatch(ctx, batch.ID, "fake", "scout", "", nil, proposals); err != nil {
		t.Fatal(err)
	}
	batches, err := s.ListProposalBatches(ctx, profile.ID)
	if err != nil || len(batches) != 1 || batches[0].Status != "ready" {
		t.Fatalf("批次历史必须暴露待决策结果: batches=%+v err=%v", batches, err)
	}
	rows, err := s.ListCreationProposals(ctx, profile.ID)
	if err != nil || len(rows) != 3 {
		t.Fatalf("批次提案数量错误: rows=%+v err=%v", rows, err)
	}
	byTitle := map[string]*models.CreationProposal{}
	for _, proposal := range rows {
		byTitle[proposal.WorkingTitle] = proposal
	}
	if count, err := s.CountOpenProposalsForBatch(ctx, batch.ID); err != nil || count != 3 {
		t.Fatalf("初始未决数应为 3: count=%d err=%v", count, err)
	}
	if err := s.DecideProposal(ctx, byTitle["接受"].ID, "accept", "Owner 修订后的主张", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveProposalForLater(ctx, byTitle["稍后"].ID); err != nil {
		t.Fatal(err)
	}
	if err := s.RejectProposal(ctx, byTitle["拒绝"].ID, "TooBroad", "范围过大"); err != nil {
		t.Fatal(err)
	}
	if err := s.RejectProposal(ctx, byTitle["拒绝"].ID, "TooBroad", "范围过大"); err != nil {
		t.Fatalf("同一决策重放必须幂等: %v", err)
	}
	if count, err := s.CountOpenProposalsForBatch(ctx, batch.ID); err != nil || count != 0 {
		t.Fatalf("全部决策后不应有未决提案: count=%d err=%v", count, err)
	}
	got, err := s.GetProposalBatchByIdempotencyKey(ctx, "decision-flow")
	if err != nil || got.Status != "completed" {
		t.Fatalf("最后一条决策必须释放批次背压: batch=%+v err=%v", got, err)
	}
}

func TestDurableJobDerivativeAndSnapshotReadPaths(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	episodeID := seedIntentEpisode(t, s, "durable-read-paths")
	upstream, err := s.EnqueueIngestionJobWithSnapshot(ctx, models.SourceEpisode, episodeID, models.JobTranscribe, `{"processing_depth":"knowledge_dj"}`)
	if err != nil || upstream == nil {
		t.Fatalf("自动摄取快照入队失败: job=%+v err=%v", upstream, err)
	}
	if err := s.MarkJobRemoteCallStarted(ctx, upstream.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MarkJobRunning(ctx, upstream.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkJobSucceeded(ctx, upstream.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateEpisodeStatus(ctx, episodeID, models.StatusUnprocessed); err != nil {
		t.Fatal(err)
	}
	downstream, err := s.EnqueueJob(ctx, models.SourceEpisode, episodeID, models.JobAnalyze)
	if err != nil || downstream == nil {
		t.Fatalf("下游任务入队失败: job=%+v err=%v", downstream, err)
	}
	if err := s.InheritJobInputSnapshot(ctx, upstream.ID, downstream.ID); err != nil {
		t.Fatal(err)
	}
	exec, err := s.GetJobExecution(ctx, downstream.ID)
	if err != nil || exec.InputSnapshotJSON != `{"processing_depth":"knowledge_dj"}` {
		t.Fatalf("下游任务必须继承冻结深度: exec=%+v err=%v", exec, err)
	}
	payload, _ := json.Marshal(provider.TranscriptPayload{Text: "正文", Segments: []provider.Segment{{ID: "seg-1", Start: 0, End: 2, Text: "正文"}}})
	version, err := s.CreateArtifactVersion(ctx, models.SourceEpisode, episodeID, KindTranscript, "fake", "m", "1", downstream.ID, string(payload))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetCurrentVersion(ctx, models.SourceEpisode, episodeID, KindTranscript, version); err != nil {
		t.Fatal(err)
	}
	highlightJob, err := s.EnqueueHighlightJobWithChain(ctx, models.SourceEpisode, episodeID, true)
	if err != nil || highlightJob == nil {
		t.Fatalf("高光链任务入队失败: job=%+v err=%v", highlightJob, err)
	}
	highlightExec, err := s.GetJobExecution(ctx, highlightJob.ID)
	if err != nil || !json.Valid([]byte(highlightExec.InputSnapshotJSON)) {
		t.Fatalf("高光任务必须冻结转录身份: exec=%+v err=%v", highlightExec, err)
	}

	firstVersion, reused, err := s.CreateNarrationCached(ctx, models.SourceEpisode, episodeID, "h1", "cache-1", "voice", "model", "a.wav", 1.5, 8, "local")
	if err != nil || reused || firstVersion != 1 {
		t.Fatalf("首次解说缓存写入失败: version=%d reused=%v err=%v", firstVersion, reused, err)
	}
	againVersion, reused, err := s.CreateNarrationCached(ctx, models.SourceEpisode, episodeID, "h1", "cache-1", "voice", "model", "ignored.wav", 2, 9, "local")
	if err != nil || !reused || againVersion != firstVersion {
		t.Fatalf("相同缓存身份必须复用: version=%d reused=%v err=%v", againVersion, reused, err)
	}
	if row, err := s.GetNarrationByCacheKey(ctx, models.SourceEpisode, episodeID, "h1", "cache-1"); err != nil || row.RelPath != "a.wav" {
		t.Fatalf("缓存解说读取失败: row=%+v err=%v", row, err)
	}

	plan, err := s.CreateDJPlan(ctx, &models.DJPlan{
		SourceType: models.SourceEpisode, SourceID: episodeID, HighlightVersion: 1, TargetSeconds: 60, TotalSeconds: 5,
		Items: []models.DJPlanItem{{Kind: models.DJItemNarration, HighlightID: "h1", EstSeconds: 5, Reason: "test"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if latest, err := s.GetLatestDJPlanForSource(ctx, models.SourceEpisode, episodeID); err != nil || latest.ID != plan.ID {
		t.Fatalf("最新 DJ 清单读取失败: latest=%+v err=%v", latest, err)
	}
	djJob, err := s.EnqueueDJPlanJob(ctx, models.SourceEpisode, episodeID, 1, 60)
	if err != nil || djJob == nil {
		t.Fatalf("DJ 清单任务入队失败: job=%+v err=%v", djJob, err)
	}
	if duplicate, err := s.EnqueueDJPlanJob(ctx, models.SourceEpisode, episodeID, 1, 60); err != nil || duplicate != nil {
		t.Fatalf("相同 DJ 意图应被活跃任务去重: duplicate=%+v err=%v", duplicate, err)
	}

	snapshot, err := s.FreezeSourceSnapshot(ctx, models.SourceEpisode, episodeID)
	if err != nil {
		t.Fatal(err)
	}
	snapshots, err := s.ListSourceSnapshots(ctx, models.SourceEpisode, episodeID)
	if err != nil || len(snapshots) != 1 || snapshots[0].ID != snapshot.ID {
		t.Fatalf("来源快照列表读取失败: snapshots=%+v err=%v", snapshots, err)
	}
}

func TestCuratorRevisionBridgesIntoDurableWritingAndRevision(t *testing.T) {
	s, brief, _, keyPointID := makeConfirmFixture(t)
	ctx := context.Background()
	proposal, err := s.GetCreationProposal(ctx, brief.CreationProposalID)
	if err != nil {
		t.Fatal(err)
	}
	baseRevision, err := s.GetCreationBriefRevision(ctx, brief.ID, brief.CurrentVersion)
	if err != nil {
		t.Fatal(err)
	}
	curatorInput := baseRevision.CuratorInputSnapshotJSON
	curatorJob, err := s.EnqueueCuratorBriefJob(ctx, proposal.ID, brief.ID, curatorInput, "test", "curator-model")
	if err != nil || curatorJob == nil {
		t.Fatalf("Curator 任务入队失败: job=%+v err=%v", curatorJob, err)
	}
	curated, err := s.ApplyCuratorResultRevision(ctx, brief.ID, curatorJob.ID, brief.CurrentVersion, models.CreationBriefRevision{
		OwnerClaim: proposal.OwnerClaim, ClaimPlanJSON: `{"thesis":"Owner"}`,
		MaterialPlanJSON: `{"selected":["` + keyPointID + `"],"rejected":[]}`,
		Outline:          "# 文章大纲", Style: "清晰", ClaimType: "synthesis",
		UnresolvedQuestionsJSON: `[]`, CuratorPromptVersion: provider.CuratorPromptVersion,
		CuratorInputSnapshotJSON: curatorInput,
	})
	if err != nil || curated.Version != brief.CurrentVersion+1 || curated.OriginJobID != curatorJob.ID {
		t.Fatalf("Curator 修订落库失败: revision=%+v err=%v", curated, err)
	}
	replayed, err := s.ApplyCuratorResultRevision(ctx, brief.ID, curatorJob.ID, brief.CurrentVersion, models.CreationBriefRevision{})
	if err != nil || replayed.ID != curated.ID {
		t.Fatalf("Curator 结果重放必须复用原修订: replayed=%+v err=%v", replayed, err)
	}
	if _, err := s.MarkJobRunning(ctx, curatorJob.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkJobSucceeded(ctx, curatorJob.ID); err != nil {
		t.Fatal(err)
	}
	if duplicate, err := s.EnqueueCuratorBriefJob(ctx, proposal.ID, brief.ID, curatorInput, "test", "curator-model"); err != nil || duplicate != nil {
		t.Fatalf("成功 Curator 意图不得重复生成修订: duplicate=%+v err=%v", duplicate, err)
	}
	briefs, err := s.ListCreationBriefs(ctx, proposal.EditorialProfileID)
	if err != nil || len(briefs) != 1 || briefs[0].CurrentVersion != curated.Version {
		t.Fatalf("创作合同列表必须投影当前 Curator 修订: briefs=%+v err=%v", briefs, err)
	}

	link, err := s.ConfirmCreationBriefVersionAndEnsureLink(ctx, brief.ID, curated.Version, "v2")
	if err != nil {
		t.Fatal(err)
	}
	lookup := []func(context.Context, string) (*CreationArticleLink, error){
		s.GetCreationArticleLinkByCreationProposal,
		s.GetCreationArticleLinkByArticleProposal,
		s.GetCreationArticleLinkByArticleBrief,
	}
	ids := []string{proposal.ID, link.ArticleProposalID, link.ArticleBriefID}
	for i, get := range lookup {
		got, err := get(ctx, ids[i])
		if err != nil || got.ID != link.ID {
			t.Fatalf("创作桥接反查 %d 失败: got=%+v err=%v", i, got, err)
		}
	}
	writerJob, err := s.EnqueueClaimWritingForCreationBrief(ctx, brief.ID)
	if err != nil || writerJob == nil {
		t.Fatalf("确认合同必须能入队写作: job=%+v err=%v", writerJob, err)
	}
	persistedJob, err := s.getClaimWritingIntentJob(ctx, claimWritingIntentID(brief.ID, curated.Version))
	if err != nil || persistedJob.ID != writerJob.ID {
		t.Fatalf("写作意图必须可恢复: job=%+v err=%v", persistedJob, err)
	}
	mapping, err := s.MapCreationBriefsToDrafts(ctx, proposal.EditorialProfileID)
	if err != nil || mapping[brief.ID] != writerJob.SourceID {
		t.Fatalf("工作台必须能从合同导航到草稿: mapping=%+v err=%v", mapping, err)
	}

	first, err := s.CreateArticleRevision(ctx, models.ArticleRevision{
		DraftID: writerJob.SourceID, Title: "第一版", Markdown: "保留片段。即将修改的片段。", Origin: "owner",
	})
	if err != nil {
		t.Fatal(err)
	}
	entries := []models.ClaimMapEntry{
		{Excerpt: "保留片段", ClaimKind: provider.ClaimSource, MaterialIDs: []string{keyPointID}, SourceTitle: "R17", CitationRefs: []string{"seg-1"}},
		{Excerpt: "即将修改的片段", ClaimKind: provider.ClaimOwner},
	}
	if err := s.SaveClaimMap(ctx, first.DraftID, first.ID, entries); err != nil {
		t.Fatal(err)
	}
	second, err := s.CreateArticleRevision(ctx, models.ArticleRevision{
		DraftID: writerJob.SourceID, Title: "第二版", Markdown: "保留片段。已经重写。", Origin: "owner",
	})
	if err != nil {
		t.Fatal(err)
	}
	inherited, err := s.InheritClaimMap(ctx, first.ID, second.ID, second.DraftID, second.Markdown)
	if err != nil || inherited != 1 {
		t.Fatalf("只应继承逐字保留的主张映射: inherited=%d err=%v", inherited, err)
	}
	claimMap, err := s.ListClaimMap(ctx, second.DraftID, second.ID)
	if err != nil || len(claimMap) != 1 || claimMap[0].Excerpt != "保留片段" {
		t.Fatalf("新修订 ClaimMap 读取失败: map=%+v err=%v", claimMap, err)
	}
	if markdown, err := s.GetArticleMarkdown(ctx, second.ID); err != nil || markdown != second.Markdown {
		t.Fatalf("审校正文读取失败: markdown=%q err=%v", markdown, err)
	}
	if _, _, err := s.GetArticleSource(ctx, second.DraftID); err != nil {
		t.Fatalf("文章来源身份读取失败: %v", err)
	}
	history, err := s.RecordArticleHistory(ctx, second.ID, "unpublished")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.GetArticleHistoryForRevision(ctx, second.ID); err != nil || got.ID != history.ID {
		t.Fatalf("文章历史反查失败: got=%+v err=%v", got, err)
	}
	style, err := s.CreateArticleReview(ctx, models.ArticleReview{
		RevisionID: second.ID, Kind: ReviewKindStyle, Status: "failed", IssuesJSON: `["减少重复"]`, OriginJobID: writerJob.ID,
		Provider: strPtr("fake-styler"), Model: strPtr("style-model"), PromptVersion: strPtr(provider.StyleEditorPromptVersion),
	})
	if err != nil {
		t.Fatal(err)
	}
	latestStyle, err := s.LatestStyleReview(ctx, second.ID)
	if err != nil || latestStyle.ID != style.ID {
		t.Fatalf("最新风格审校读取失败: review=%+v err=%v", latestStyle, err)
	}
	revisionJob, err := s.EnqueueClaimRevisionForRevision(ctx, second.ID)
	if err != nil || revisionJob == nil {
		t.Fatalf("当前修订必须能入队 AI 修订: job=%+v err=%v", revisionJob, err)
	}
	if got, err := s.ClaimRevisionJobForRevision(ctx, second.ID); err != nil || got.ID != revisionJob.ID {
		t.Fatalf("AI 修订意图必须可恢复: job=%+v err=%v", got, err)
	}
}

func TestApplyKeypointQualityAndOperationalStoreContracts(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	episodeID := seedTranscriptEpisode(t, s, "quality-apply")
	card := &provider.KnowledgeCard{KeyPoints: []provider.KeyPoint{{Content: "自动重点", Description: "说明", Citations: []string{"seg-1"}}}}
	if _, err := s.IndexKeyPoints(ctx, models.SourceEpisode, episodeID, "质量单集", 1, card, []provider.Segment{{ID: "seg-1", Start: 0, End: 1, Text: "素材"}}); err != nil {
		t.Fatal(err)
	}
	rows, err := s.ListKeyPointRowsByCardVersion(ctx, models.SourceEpisode, episodeID, 1)
	if err != nil || len(rows) != 1 {
		t.Fatalf("卡片版本重点读取失败: rows=%+v err=%v", rows, err)
	}
	keyPoint := rows[0]
	fingerprint := provider.FingerprintKeypoint(keyPoint.Content, keyPoint.Description, []string{"seg-1"})
	if err := s.SaveKeypointQualityResult(ctx, &models.KeypointQualityResult{
		KeyPointID: keyPoint.ID, SourceType: models.SourceEpisode, SourceID: episodeID,
		CardVersion: 1, ContentFingerprint: fingerprint, Decision: models.KPQualityReady,
		Reasons: []string{"证据充分"}, InputSnapshotJSON: `{}`, Provider: "fake", Model: "quality-model",
	}); err != nil {
		t.Fatal(err)
	}
	stats, err := s.ApplyKeypointQualityResults(ctx, models.SourceEpisode, episodeID, 1)
	if err != nil || stats.Applied != 1 || stats.MaterialChanges != 1 {
		t.Fatalf("质量结果必须推进正式状态并记录变化: stats=%+v err=%v", stats, err)
	}
	replayed, err := s.ApplyKeypointQualityResults(ctx, models.SourceEpisode, episodeID, 1)
	if err != nil || replayed.Unchanged != 1 || replayed.MaterialChanges != 0 {
		t.Fatalf("质量结果重放必须幂等: stats=%+v err=%v", replayed, err)
	}

	doc, err := s.CreateWebDocument(ctx, "落源文档", "https://example.com/quality-source", "正文")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.GetDocumentByOriginURL(ctx, doc.OriginURL); err != nil || got.ID != doc.ID {
		t.Fatalf("网页落源身份查询失败: got=%+v err=%v", got, err)
	}
	job, _, err := s.EnqueueJobIdempotent(ctx, JobIntentSpec{
		SourceType: models.SourceEpisode, SourceID: episodeID, JobType: models.JobDigest, IntentID: "defer-quality-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeferJobForDailyLimit(ctx, job.ID, "+1 hour", "达到日限额"); err != nil {
		t.Fatal(err)
	}
	deferred, err := s.GetJob(ctx, job.ID)
	if err != nil || deferred.Status != models.StatusRunning || deferred.LastError == nil || *deferred.LastError != "达到日限额" {
		t.Fatalf("额度延期状态不可恢复: job=%+v err=%v", deferred, err)
	}
	if err := s.RecordUsageReceipt(ctx, models.UsageReceipt{ReceiptID: "receipt-quality", Operation: "quality", Provider: "fake", Model: "m"}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateUsageCost(ctx, "receipt-quality", 7); err != nil {
		t.Fatal(err)
	}
	progress := &models.ListeningProgress{SourceType: models.SourceEpisode, SourceID: episodeID, PlanID: "p", PlanVersion: 1, ItemPosition: 2, Seq: 1}
	if err := s.SaveListeningProgress(ctx, progress); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteListeningProgress(ctx, models.SourceEpisode, episodeID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetListeningProgress(ctx, models.SourceEpisode, episodeID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("删除后的收听进度必须不可见: %v", err)
	}
}
