package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	_ "modernc.org/sqlite"
)

// TestMigration0055 R22：article_revision_id 列与唯一索引；旧 revision: source_url
// 安全回填；外部 URL 不受影响；不存在修订的 revision: 行保持空专用字段。
func TestMigration0055(t *testing.T) {
	ctx := t.Context()
	db := openRaw(t, filepath.Join(t.TempDir(), "mig55.db"))
	ms, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	applyThrough := func(version int) error {
		for _, m := range ms {
			if m.version <= version {
				if err := applyOne(ctx, db, m); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if _, err := db.ExecContext(ctx, schemaMigrationsTable); err != nil {
		t.Fatal(err)
	}
	if err := applyThrough(54); err != nil {
		t.Fatalf("应用 1..54: %v", err)
	}
	// 0054 下构造：真实修订 + 旧 revision: 历史行 + 外部行 + 指向不存在修订的行。
	if _, err = db.ExecContext(ctx,
		`INSERT INTO editorial_profiles (id,name) VALUES ('p55','P')`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx,
		`INSERT INTO article_proposals (id,editorial_profile_id,kind,status,title) VALUES ('ap55','p55','deep_read','accepted','T')`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx,
		`INSERT INTO article_briefs (id,proposal_id,status,thesis) VALUES ('ab55','ap55','confirmed','X')`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx,
		`INSERT INTO article_drafts (id,editorial_profile_id,brief_id,title,status) VALUES ('d55','p55','ab55','T','drafting')`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx,
		`INSERT INTO article_revisions (id,draft_id,version,title,markdown,origin) VALUES ('r55','d55',1,'R55 标题','# x','owner')`); err != nil {
		t.Fatal(err)
	}
	// 同 revision 两条旧行：published 较旧、unpublished 较新 → canonical 必须是
	// published 行；unpublished 行保留但专用字段为空。
	// canonical 行故意挂错画像/旧标题/旧正文：迁移必须校正为确切 revision 元数据。
	if _, err = db.ExecContext(ctx,
		`INSERT INTO editorial_profiles (id,name) VALUES ('p55wrong','WRONG')`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx,
		`INSERT INTO creation_history (id,editorial_profile_id,status,creation_form,title,content,source_url,created_at,updated_at)
		 VALUES ('h1','p55wrong','published','article','旧标题','旧正文','revision:r55','2020-01-01 00:00:00','2020-01-01 00:00:00')`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx,
		`INSERT INTO creation_history (id,editorial_profile_id,status,creation_form,title,source_url,created_at,updated_at)
		 VALUES ('h1b','p55','unpublished','article','T','revision:r55','2021-01-01 00:00:00','2021-01-01 00:00:00')`); err != nil {
		t.Fatal(err)
	}
	for _, row := range [][]string{
		{"h2", "https://a.example/1"}, // 外部：不动
		{"h3", "revision:missing"},    // 修订不存在：不回填
	} {
		if _, err = db.ExecContext(ctx,
			`INSERT INTO creation_history (id,editorial_profile_id,status,creation_form,title,source_url) VALUES (?, 'p55','published','article','T',?)`, row[0], row[1]); err != nil {
			t.Fatal(err)
		}
	}
	for _, m := range ms {
		if m.version == 55 {
			if err := applyOne(ctx, db, m); err != nil {
				t.Fatalf("0055 升级应成功: %v", err)
			}
		}
	}
	var boundID string
	if err := db.QueryRowContext(ctx, `SELECT id FROM creation_history WHERE article_revision_id='r55'`).Scan(&boundID); err != nil {
		t.Fatalf("应恰有一行回填: %v", err)
	}
	if boundID != "h1" {
		t.Fatalf("canonical 必须是 published 行（即使较旧）: %s", boundID)
	}
	var h1bBound int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM creation_history WHERE id='h1b' AND article_revision_id='' AND status='unpublished' AND title='T'`).Scan(&h1bBound); err != nil || h1bBound != 1 {
		t.Fatalf("非 canonical 重复行保留原数据且专用字段为空: %d %v", h1bBound, err)
	}
	// canonical 元数据被校正：画像=draft 画像、title/content=确切修订。
	var fixedProfile, fixedTitle, fixedContent string
	if err := db.QueryRowContext(ctx,
		`SELECT editorial_profile_id, title, content FROM creation_history WHERE article_revision_id='r55'`).Scan(&fixedProfile, &fixedTitle, &fixedContent); err != nil {
		t.Fatal(err)
	}
	if fixedProfile != "p55" {
		t.Fatalf("画像应对齐 draft 画像: %s", fixedProfile)
	}
	if fixedTitle != "R55 标题" || fixedContent != "# x" {
		t.Fatalf("title/content 应对齐确切修订: %q %q", fixedTitle, fixedContent)
	}
	// 非 canonical 行内容不被校正。
	var h1bTitle string
	if err := db.QueryRowContext(ctx, `SELECT title FROM creation_history WHERE id='h1b'`).Scan(&h1bTitle); err != nil || h1bTitle != "T" {
		t.Fatalf("非 canonical 行 title 不动: %q %v", h1bTitle, err)
	}
	var external, missing int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM creation_history WHERE id='h2' AND article_revision_id='' AND source_url='https://a.example/1'`).Scan(&external); err != nil || external != 1 {
		t.Fatalf("外部 URL 不得改动: %d %v", external, err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM creation_history WHERE id='h3' AND article_revision_id=''`).Scan(&missing); err != nil || missing != 1 {
		t.Fatalf("不存在修订的行不得回填: %d %v", missing, err)
	}
	// 非空唯一索引生效。
	if _, err = db.ExecContext(ctx,
		`INSERT INTO creation_history (id,editorial_profile_id,status,creation_form,title,source_url,article_revision_id) VALUES ('h4','p55','unpublished','article','T','revision:r55','r55')`); err == nil {
		t.Fatal("同修订重复历史必须被唯一索引拒绝")
	}
}

// TestRecordArticleHistory_R22：画像正确、CAS、幂等单行、unpublished→published
// 升级、published 不降级、并发重复单行、Get/List 带出 draft/version。
func TestRecordArticleHistory_R22(t *testing.T) {
	s, draftID, base := ownerRevisionFixture(t)
	ctx := t.Context()
	draft, err := s.GetArticleDraft(ctx, draftID)
	check(t, err)

	// 首次登记：unpublished。
	first, err := s.RecordArticleHistory(ctx, base.ID, "unpublished")
	check(t, err)
	if first.EditorialProfileID != draft.EditorialProfileID {
		t.Fatalf("画像必须取文章所属 draft: %s vs %s", first.EditorialProfileID, draft.EditorialProfileID)
	}
	if first.ArticleRevisionID != base.ID || first.ArticleDraftID != draftID || first.ArticleVersion != base.Version {
		t.Fatalf("精确身份缺失: %+v", first)
	}
	// 升级为 published。
	upgraded, err := s.RecordArticleHistory(ctx, base.ID, "published")
	check(t, err)
	if upgraded.Status != "published" || upgraded.ID != first.ID {
		t.Fatalf("unpublished 应升为 published: %+v", upgraded)
	}
	// published 不被降级。
	down, err := s.RecordArticleHistory(ctx, base.ID, "unpublished")
	check(t, err)
	if down.Status != "published" || down.ID != first.ID {
		t.Fatalf("published 不得降级: %+v", down)
	}
	var n int
	check(t, s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM creation_history WHERE article_revision_id=?`, base.ID).Scan(&n))
	if n != 1 {
		t.Fatalf("同修订必须只一行: %d", n)
	}

	// CAS：过期修订（current 已移动）拒绝登记。
	rev2, err := s.SaveOwnerRevisionWithClaimLineage(ctx, draftID, base.ID, "v2", "# v2\n\n全新内容。")
	check(t, err)
	if _, err := s.RecordArticleHistory(ctx, base.ID, "published"); !errors.Is(err, ErrConflict) {
		t.Fatalf("过期修订登记必须 ErrConflict: %v", err)
	}
	// 新 current 可登记。
	second, err := s.RecordArticleHistory(ctx, rev2.ID, "published")
	check(t, err)
	if second.ArticleRevisionID != rev2.ID {
		t.Fatalf("second=%+v", second)
	}

	// 并发重复：同修订并发登记只一行（rev2 已是 published，重复登记幂等）。
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.RecordArticleHistory(ctx, rev2.ID, "published"); err != nil {
				t.Errorf("并发登记: %v", err)
			}
		}()
	}
	wg.Wait()
	var rows int
	check(t, s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM creation_history WHERE article_revision_id=?`, rev2.ID).Scan(&rows))
	if rows != 1 {
		t.Fatalf("并发登记必须单行: %d", rows)
	}

	// 发现召回：登记的历史进入词法重复提醒。
	candidates, err := s.FindCreationHistoryCandidates(ctx, draft.EditorialProfileID, "v2 更新")
	check(t, err)
	found := false
	for _, c := range candidates {
		if c.ArticleRevisionID == rev2.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("登记历史应进入发现召回: %+v", candidates)
	}
}

// TestFindUsageBySourceIsolation R22：同一 Store 内自建两来源（A/B），B 的 keypoint
// 与 A 共享相同 Segment ID（跨来源重复场景）。断言：A 的文章链接精确到 draft+revision
// anchor；B 的 keypoint/来源不得匹配 A 的修订（前缀/子串/重复 Segment 都不串）；
// 查询错误上抛；json_each 精确成员语义在位。
func TestFindUsageBySourceIsolation(t *testing.T) {
	// 整个测试使用 confirmBriefFixture 的同一 Store（来源 A fixture 自带）。
	s, bA, epA, kpA := confirmBriefFixture(t)
	ctx := t.Context()

	// 来源 A：生产 Writer 保存一个引用 kpA 的文章修订。
	jobA, err := s.EnqueueClaimWritingForCreationBrief(ctx, bA.ID)
	check(t, err)
	linkA, err := s.GetCreationArticleLinkByCreationBrief(ctx, bA.ID)
	check(t, err)
	draftAObj, err := s.GetArticleDraftByBrief(ctx, linkA.ArticleBriefID)
	check(t, err)
	revA, err := s.SaveClaimWritingOutput(ctx, ClaimWritingOutput{
		DraftID: draftAObj.ID, JobID: jobA.ID,
		CreationBriefID: bA.ID, BriefVersion: bA.ConfirmedVersion,
		CreationArticleLinkID: linkA.ID, ArticleProposalID: linkA.ArticleProposalID, ArticleBriefID: linkA.ArticleBriefID,
		OwnerClaim: "Owner", Title: "来源A文章", Markdown: "# A\n\n来源说过这句话。",
		ProviderName: "t", ModelName: "m", PromptVersion: provider.ClaimWriterPromptVersion,
		AuthorizedIDs: []string{kpA},
		Entries:       []models.ClaimMapEntry{{Excerpt: "来源说过这句话。", ClaimKind: provider.ClaimSource, MaterialIDs: []string{kpA}}},
	})
	check(t, err)
	wantLink := "/workbench/drafts/" + draftAObj.ID + "#revision-" + revA.ID

	// 读取 A keypoint 的真实 citation 作为共享 Segment ID（真实跨来源重复场景）：
	// B 的 transcript、keypoint、digest 全部复用同一个值。
	kpARow, err := s.GetKeyPoint(ctx, kpA)
	check(t, err)
	var sharedSegs []string
	check(t, json.Unmarshal([]byte(kpARow.CitationsJSON), &sharedSegs))
	if len(sharedSegs) == 0 {
		t.Fatal("A keypoint 缺少 citation")
	}
	sharedSeg := sharedSegs[0]

	// 来源 B：第二个 Episode，刻意复用与 A 相同的 Segment ID。
	pod, err := s.CreatePodcast(ctx, "https://b.example/feed", "B", "", "")
	check(t, err)
	if _, err := s.MergeEpisodes(ctx, pod.ID, []models.Episode{{GUID: "epB", Title: "B", AudioURL: "https://b.mp3"}}); err != nil {
		t.Fatal(err)
	}
	eps, err := s.ListEpisodes(ctx, pod.ID)
	check(t, err)
	if len(eps) == 0 {
		t.Fatal("B 缺少 episode")
	}
	epB := eps[0].ID
	tJob, err := s.EnqueueJob(ctx, models.SourceEpisode, epB, models.JobTranscribe)
	check(t, err)
	tv, err := s.CreateArtifactVersion(ctx, models.SourceEpisode, epB, KindTranscript, "t", "t", "1", tJob.ID,
		`{"language":"zh","text":"B","segments":[{"id":"`+sharedSeg+`","start":0,"end":1,"text":"B"}]}`)
	check(t, err)
	if _, err := s.MarkJobRunning(ctx, tJob.ID); err != nil {
		t.Fatal(err)
	}
	check(t, s.MarkJobSucceeded(ctx, tJob.ID))
	check(t, s.SetCurrentVersion(ctx, models.SourceEpisode, epB, KindTranscript, tv))
	card := &provider.KnowledgeCard{Title: "B", Summary: provider.CitedText{Text: "B 内容", Citations: []string{sharedSeg}}, KeyPoints: []provider.KeyPoint{{Content: "B 内容", Citations: []string{sharedSeg}}}}
	if _, err := s.IndexKeyPoints(ctx, models.SourceEpisode, epB, "B 来源", 1, card, []provider.Segment{{ID: sharedSeg, Start: 0, End: 1, Text: "B"}}); err != nil {
		t.Fatal(err)
	}
	kps, _, err := s.ListKeyPointsFiltered(ctx, KeyPointFilter{SourceID: epB}, 1, 10)
	check(t, err)
	if len(kps) == 0 {
		t.Fatal("B 缺少 keypoint")
	}
	kpB := kps[0].ID
	// B keypoint 的 citation 必须就是共享值。
	kpBRow, err := s.GetKeyPoint(ctx, kpB)
	check(t, err)
	var kpBCitations []string
	check(t, json.Unmarshal([]byte(kpBRow.CitationsJSON), &kpBCitations))
	if len(kpBCitations) == 0 || kpBCitations[0] != sharedSeg {
		t.Fatalf("B keypoint citation 应为共享值 %q: %v", sharedSeg, kpBCitations)
	}

	// A 来源反查：文章链接精确；B 不得出现。
	usagesA, err := s.FindUsageBySource(ctx, models.SourceEpisode, epA)
	check(t, err)
	foundArticle := false
	for _, u := range usagesA {
		if u.Kind == "article" {
			foundArticle = true
			if u.Link != wantLink {
				t.Fatalf("A 的文章链接必须精确: %s want %s", u.Link, wantLink)
			}
		}
	}
	if !foundArticle {
		t.Fatalf("A 反查应包含自己的文章修订: %+v", usagesA)
	}
	// B 来源反查：不得出现 A 的修订。
	usagesB, err := s.FindUsageBySource(ctx, models.SourceEpisode, epB)
	check(t, err)
	for _, u := range usagesB {
		if strings.Contains(u.Link, revA.ID) || strings.Contains(u.Link, draftAObj.ID) {
			t.Fatalf("B 不得串到 A 的修订: %+v", u)
		}
	}
	// FindUsageByKeyPoint 精确成员：A 的 keypoint 匹配 A 修订；B 的不匹配。
	articlesA, err := s.articleUsagesByMaterials(ctx, []string{kpA})
	check(t, err)
	if len(articlesA) != 1 || articlesA[0].Link != wantLink {
		t.Fatalf("A keypoint 应精确匹配 A 修订: %+v", articlesA)
	}
	articlesB, err := s.articleUsagesByMaterials(ctx, []string{kpB})
	check(t, err)
	if len(articlesB) != 0 {
		t.Fatalf("B keypoint 不得匹配 A 修订（重复 Segment/子串都不行）: %+v", articlesB)
	}
	// 查询错误上抛：不存在的 keypoint。
	if _, err := s.FindUsageByKeyPoint(ctx, "missing-kp"); err == nil {
		t.Fatal("缺失 keypoint 应报错")
	}
	// 精读关联：为 A/B 各建一份 digest（块引用各自 Segment），断言 A/B 反查各见
	// 自己的 digest、不见对方的（episode_digests 归属精确关联，不按 Segment 猜）。
	// A/B digest 都引用同一个共享 Segment ID：证明按 digest 归属精确关联时
	// 跨来源重复 ID 不会串。
	dA, err := s.CreateEpisodeDigest(ctx, &models.EpisodeDigest{
		SourceType: models.SourceEpisode, SourceID: epA, Title: "A 的精读",
	}, []models.DigestBlock{{Position: 0, Type: "summary", Text: "A 摘要", Citations: []string{sharedSeg}}})
	check(t, err)
	dB, err := s.CreateEpisodeDigest(ctx, &models.EpisodeDigest{
		SourceType: models.SourceEpisode, SourceID: epB, Title: "B 的精读",
	}, []models.DigestBlock{{Position: 0, Type: "summary", Text: "B 摘要", Citations: []string{sharedSeg}}})
	check(t, err)
	usagesA2, err := s.FindUsageBySource(ctx, models.SourceEpisode, epA)
	check(t, err)
	digestTitlesA := map[string]bool{}
	for _, u := range usagesA2 {
		if u.Kind == "digest" {
			digestTitlesA[u.Title] = true
		}
	}
	if !digestTitlesA["A 的精读"] || digestTitlesA["B 的精读"] {
		t.Fatalf("A 来源反查 digest 不串: %v", digestTitlesA)
	}
	usagesB2, err := s.FindUsageBySource(ctx, models.SourceEpisode, epB)
	check(t, err)
	digestTitlesB := map[string]bool{}
	for _, u := range usagesB2 {
		if u.Kind == "digest" {
			digestTitlesB[u.Title] = true
		}
	}
	if !digestTitlesB["B 的精读"] || digestTitlesB["A 的精读"] {
		t.Fatalf("B 来源反查 digest 不串: %v", digestTitlesB)
	}
	// FindUsageByKeyPoint：A keypoint → A digest；B keypoint → B digest。
	kpDigestsA, err := s.FindUsageByKeyPoint(ctx, kpA)
	check(t, err)
	kpDigestsB, err := s.FindUsageByKeyPoint(ctx, kpB)
	check(t, err)
	if !kpDigestContains(kpDigestsA, "A 的精读") || kpDigestContains(kpDigestsA, "B 的精读") {
		t.Fatalf("A keypoint digest 反查不串: %+v", kpDigestsA)
	}
	if !kpDigestContains(kpDigestsB, "B 的精读") || kpDigestContains(kpDigestsB, "A 的精读") {
		t.Fatalf("B keypoint digest 反查不串: %+v", kpDigestsB)
	}
	_ = dA
	_ = dB

	// json_each 精确成员语义在位：不存在的 ID 零匹配。
	if !jsonEachExact(t, s) {
		t.Fatal("json_each 精确匹配语义失效")
	}
}

// kpDigestContains 判断 keypoint 反查结果中是否包含指定标题的精读。
func kpDigestContains(usages []MaterialUsage, title string) bool {
	for _, u := range usages {
		if u.Kind == "digest" && u.Title == title {
			return true
		}
	}
	return false
}

// jsonEachExact 精确成员语义验证：不存在的 keypoint 零匹配。
func jsonEachExact(t *testing.T, s *Store) bool {
	idsJSON, err := json.Marshal([]string{"no-such-kp"})
	check(t, err)
	rows, err := s.DB.QueryContext(t.Context(),
		`SELECT COUNT(*) FROM claim_map_entries cme WHERE EXISTS (SELECT 1 FROM json_each(cme.material_ids_json) je WHERE je.value IN (SELECT je2.value FROM json_each(?) je2))`, string(idsJSON))
	check(t, err)
	defer rows.Close()
	var n int
	if !rows.Next() {
		t.Fatal("聚合查询缺行")
	}
	check(t, rows.Scan(&n))
	return n == 0
}

// TestHistoryRevisionSurvivesBackupRestore R22：备份/恢复保留 article_revision_id
// 与唯一身份（VACUUM INTO 全量副本语义）。
func TestHistoryRevisionSurvivesBackupRestore(t *testing.T) {
	s, _, base := ownerRevisionFixture(t)
	ctx := t.Context()
	_, err := s.RecordArticleHistory(ctx, base.ID, "published")
	check(t, err)
	dst := filepath.Join(t.TempDir(), "backup.db")
	check(t, ConsistencyBackup(ctx, s.DB, dst))

	restore, err := sql.Open("sqlite", dst)
	check(t, err)
	defer restore.Close()
	var revisionID, status string
	check(t, restore.QueryRowContext(ctx,
		`SELECT article_revision_id, status FROM creation_history WHERE article_revision_id!='' LIMIT 1`).Scan(&revisionID, &status))
	if revisionID != base.ID || status != "published" {
		t.Fatalf("备份恢复丢失登记身份: %s %s", revisionID, status)
	}
	// 恢复库唯一索引仍在：重复登记仍被拒。
	if _, err := restore.ExecContext(ctx,
		`INSERT INTO creation_history (id,editorial_profile_id,status,creation_form,title,source_url,article_revision_id)
		 SELECT lower(hex(randomblob(16))),editorial_profile_id,status,creation_form,title,source_url,article_revision_id FROM creation_history LIMIT 1`); err == nil {
		t.Fatal("恢复库唯一索引必须保留")
	}
}

// TestAttentionQueue_FailedJobsRouting R22（审计修复）：失败任务全类型真实入队后
// 调用 AttentionQueue（单连接下 rows 未关再查询会死锁——本测试即防挂起回归）：
//   - episode 转录失败 → learning 泳道 /sources/episode/<id>；
//   - claim_review 失败 → creation 泳道精确 draft，且只出现在所属画像；
//   - curator_brief 失败 → creation 泳道带画像 workbench；
//   - last_error 可见。
func TestAttentionQueue_FailedJobsRouting(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	// episode 转录失败（learning 泳道，链接来源页）。
	pod, err := s.CreatePodcast(ctx, "https://f55.example/feed", "F55", "", "")
	check(t, err)
	if _, err := s.MergeEpisodes(ctx, pod.ID, []models.Episode{{GUID: "f55", Title: "F55", AudioURL: "https://f55.mp3"}}); err != nil {
		t.Fatal(err)
	}
	eps, err := s.ListEpisodes(ctx, pod.ID)
	check(t, err)
	failedJob, err := s.EnqueueJob(ctx, models.SourceEpisode, eps[0].ID, models.JobTranscribe)
	check(t, err)
	check(t, s.MarkJobFailed(ctx, failedJob.ID, "转录机器故障"))

	// 同一 Store 建第二个画像与其草稿，用于画像过滤断言。
	otherProfile, err := s.CreateEditorialProfile(ctx, models.EditorialProfile{Name: "其他画像"})
	check(t, err)
	proposal, err := s.CreateArticleProposal(ctx, models.ArticleProposal{EditorialProfileID: otherProfile.ID, Title: "P55"})
	check(t, err)
	check(t, s.SetArticleProposalStatus(ctx, proposal.ID, "accepted"))
	brief, err := s.CreateArticleBrief(ctx, models.ArticleBrief{ProposalID: proposal.ID, Thesis: "论点"})
	check(t, err)
	check(t, s.ConfirmArticleBrief(ctx, brief.ID))
	draft, err := s.CreateArticleDraft(ctx, brief.ID, "P55 文章")
	check(t, err)

	// claim_review 失败（creation 泳道，精确 draft；仅 otherProfile 可见）。
	revJob, _, err := s.EnqueueJobIdempotent(ctx, JobIntentSpec{
		SourceType: models.SourceEpisode, SourceID: draft.ID, JobType: models.JobClaimReview,
		IntentID: "claim_review:probe", ConfiguredProvider: "groq", ConfiguredModel: "m",
	})
	check(t, err)
	check(t, s.MarkJobFailed(ctx, revJob.ID, "主张审校故障"))

	// curator_brief 失败（source_id=proposalID；按提案画像过滤）。
	otherProposal2, err := s.CreateCreationProposal(ctx, models.CreationProposal{EditorialProfileID: otherProfile.ID, Status: "proposed", WorkingTitle: "C", ProposedClaim: "C", MaterialIDsJSON: "[]"})
	check(t, err)
	curJob, _, err := s.EnqueueJobIdempotent(ctx, JobIntentSpec{
		SourceType: models.SourceEpisode, SourceID: otherProposal2.ID, JobType: models.JobCuratorBrief,
		IntentID: "curator_failed_probe", ConfiguredProvider: "groq", ConfiguredModel: "m",
	})
	check(t, err)
	check(t, s.MarkJobFailed(ctx, curJob.ID, "Curator 故障"))

	// upload 来源处理失败（learning 泳道，精确 upload 来源页）。
	upload, err := s.CreateUpload(ctx, "a55.mp3", "audio/mpeg", 123)
	check(t, err)
	upJob, _, err := s.EnqueueJobIdempotent(ctx, JobIntentSpec{
		SourceType: models.SourceUpload, SourceID: upload.ID, JobType: models.JobTranscribe,
		IntentID: "transcribe_upload_probe", ConfiguredProvider: "groq", ConfiguredModel: "m",
	})
	check(t, err)
	check(t, s.MarkJobFailed(ctx, upJob.ID, "上传转录故障"))

	// document 来源处理失败（learning 泳道，精确文档页）。
	doc, err := s.CreatePDFDocument(ctx, "R55 文档", "r55.pdf", "文档内容")
	check(t, err)
	docJob, _, err := s.EnqueueJobIdempotent(ctx, JobIntentSpec{
		SourceType: models.SourceDocument, SourceID: doc.ID, JobType: models.JobKeypointQuality,
		IntentID: "quality_doc_probe", ConfiguredProvider: "groq", ConfiguredModel: "m",
	})
	check(t, err)
	check(t, s.MarkJobFailed(ctx, docJob.ID, "文档质量判定故障"))

	// otherProfile 视角：能看到全部三类；episode/draft/curator href 精确。
	items, err := s.AttentionQueue(ctx, otherProfile.ID)
	check(t, err) // 挂起/单连接死锁会让本测试超时失败
	var learnFailed, uploadFailed, docFailed, draftFailed, briefFailed bool
	var draftContinueWriting bool
	for _, it := range items {
		if it.Kind == "continue_writing" && it.ID == draft.ID {
			draftContinueWriting = it.Lane == "creation" && it.Href == "/workbench/drafts/"+draft.ID
		}
		if it.Kind != "failed_job" {
			continue
		}
		if it.Title == "失败任务：transcribe" && it.Href == "/sources/episode/"+eps[0].ID {
			learnFailed = it.Lane == "learning" && it.Detail == "转录机器故障"
		}
		if it.Title == "失败任务：transcribe" && it.Href == "/sources/upload/"+upload.ID {
			uploadFailed = it.Lane == "learning" && it.Detail == "上传转录故障"
		}
		if it.Title == "失败任务：keypoint_quality" {
			docFailed = it.Lane == "learning" && it.Href == "/documents/"+doc.ID && it.Detail == "文档质量判定故障"
		}
		if it.Title == "失败任务：claim_review" {
			draftFailed = it.Lane == "creation" && it.Href == "/workbench/drafts/"+draft.ID && it.Detail == "主张审校故障"
		}
		if it.Title == "失败任务：curator_brief" {
			briefFailed = it.Lane == "creation" && it.Href == "/workbench?profile="+otherProfile.ID
		}
	}
	if !learnFailed {
		t.Fatal("episode 失败任务应属 learning 泳道并链接来源页")
	}
	if !uploadFailed {
		t.Fatal("upload 失败任务应属 learning 泳道并链接 upload 来源页")
	}
	if !docFailed {
		t.Fatal("document 失败任务应属 learning 泳道并链接文档页")
	}
	if !draftFailed {
		t.Fatal("claim_review 失败任务应精确到 draft")
	}
	if !briefFailed {
		t.Fatal("curator_brief 失败任务应链接带画像的 workbench")
	}
	if !draftContinueWriting {
		t.Fatal("所属画像应看到 continue_writing 精确 draft href")
	}

	// 默认画像视角：他画像的 continue_writing 与 draft/curator 失败任务均被过滤；
	// learning 泳道的 episode/upload/document 失败仍可见。
	defProfile, err := s.EnsureDefaultEditorialProfile(ctx)
	check(t, err)
	defItems, err := s.AttentionQueue(ctx, defProfile.ID)
	check(t, err)
	var defEpisodeFailed, defUploadFailed, defDocFailed bool
	for _, it := range defItems {
		if it.Kind == "continue_writing" && it.ID == draft.ID {
			t.Fatalf("他画像的 continue_writing 不得出现: %+v", it)
		}
		if it.Kind == "failed_job" && (it.Title == "失败任务：claim_review" || it.Title == "失败任务：curator_brief") {
			t.Fatalf("他画像的 draft/curator 失败任务不得出现: %+v", it)
		}
		if it.Kind == "failed_job" && it.Title == "失败任务：transcribe" && it.Href == "/sources/episode/"+eps[0].ID {
			defEpisodeFailed = true
		}
		if it.Kind == "failed_job" && it.Href == "/sources/upload/"+upload.ID {
			defUploadFailed = true
		}
		if it.Kind == "failed_job" && it.Href == "/documents/"+doc.ID {
			defDocFailed = true
		}
	}
	if !defEpisodeFailed || !defUploadFailed || !defDocFailed {
		t.Fatalf("默认画像应仍可见 learning 泳道失败任务: ep=%v up=%v doc=%v", defEpisodeFailed, defUploadFailed, defDocFailed)
	}
}

// TestRecordArticleHistory_TwoStoresConcurrentFirstRegister R22（审计修复 2）：
// 同一数据库文件的两个 Store handle 首次并发登记——guarded UPSERT 保证全部成功
// 且恰一行（不需要事务重试，单条 SQL 完成 CAS + 幂等）。
func TestRecordArticleHistory_TwoStoresConcurrentFirstRegister(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "shared.db")
	s1, err := Open(path)
	check(t, err)
	defer s1.Close()
	// 用 s1 建立可登记的修订（current）。
	profile, err := s1.EnsureDefaultEditorialProfile(t.Context())
	check(t, err)
	proposal, err := s1.CreateArticleProposal(t.Context(), models.ArticleProposal{EditorialProfileID: profile.ID, Title: "双Store"})
	check(t, err)
	check(t, s1.SetArticleProposalStatus(t.Context(), proposal.ID, "accepted"))
	brief, err := s1.CreateArticleBrief(t.Context(), models.ArticleBrief{ProposalID: proposal.ID, Thesis: "论点"})
	check(t, err)
	check(t, s1.ConfirmArticleBrief(t.Context(), brief.ID))
	draft, err := s1.CreateArticleDraft(t.Context(), brief.ID, "双Store 文章")
	check(t, err)
	rev, err := s1.CreateArticleRevision(t.Context(), models.ArticleRevision{DraftID: draft.ID, Title: "双Store", Markdown: "# 内容", Origin: "owner"})
	check(t, err)

	s2, err := Open(path)
	check(t, err)
	defer s2.Close()

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, handle := range []*Store{s1, s2} {
		wg.Add(1)
		go func(h *Store) {
			defer wg.Done()
			if _, err := h.RecordArticleHistory(t.Context(), rev.ID, "published"); err != nil {
				errs <- err
			}
		}(handle)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("首次并发登记不得失败: %v", err)
	}
	var n int
	check(t, s1.DB.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM creation_history WHERE article_revision_id=?`, rev.ID).Scan(&n))
	if n != 1 {
		t.Fatalf("并发首次登记必须单行: %d", n)
	}
}

// TestFindUsageByNote_R22：同来源与跨来源精读都按唯一 note_id 精确返回；
// 不存在的 note 返回空。
func TestFindUsageByNote_R22(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	pod, err := s.CreatePodcast(ctx, "https://note.example/feed", "N", "", "")
	check(t, err)
	if _, err := s.MergeEpisodes(ctx, pod.ID, []models.Episode{{GUID: "n1", Title: "N1", AudioURL: "https://n1.mp3"}, {GUID: "n2", Title: "N2", AudioURL: "https://n2.mp3"}}); err != nil {
		t.Fatal(err)
	}
	eps, err := s.ListEpisodes(ctx, pod.ID)
	check(t, err)
	epA, epB := eps[0].ID, eps[1].ID
	note, err := s.CreateOwnerNote(ctx, models.OwnerNote{SourceType: string(models.SourceEpisode), SourceID: epA, Kind: "owner_reflection", Content: "A 的个人理解", CitationsJSON: "[]", ReferencesJSON: "[]"})
	check(t, err)
	// 同来源精读引用笔记。
	_, err = s.CreateEpisodeDigest(ctx, &models.EpisodeDigest{SourceType: models.SourceEpisode, SourceID: epA, Title: "同来源精读"},
		[]models.DigestBlock{{Position: 0, Type: "note", Text: "笔记引用", NoteID: note.ID}})
	check(t, err)
	// 跨来源精读引用笔记（合法：跨集综合）。
	_, err = s.CreateEpisodeDigest(ctx, &models.EpisodeDigest{SourceType: models.SourceEpisode, SourceID: epB, Title: "跨来源精读"},
		[]models.DigestBlock{{Position: 0, Type: "note", Text: "跨集笔记引用", NoteID: note.ID}})
	check(t, err)
	usages, err := s.FindUsageByNote(ctx, note.ID)
	check(t, err)
	titles := map[string]bool{}
	for _, u := range usages {
		titles[u.Title] = true
	}
	if !titles["同来源精读"] || !titles["跨来源精读"] {
		t.Fatalf("同/跨来源精读都必须返回: %v", titles)
	}
	missing, err := s.FindUsageByNote(ctx, "no-such-note")
	check(t, err)
	if len(missing) != 0 {
		t.Fatalf("不存在的 note 应空: %+v", missing)
	}
}

// TestRecordArticleHistory_FixesLegacyMetadata R22（第三轮审计 4）：
// 已存在但元数据错误的登记行（错误画像/旧标题正文/旧 source_url），再次登记后
// 被校正为确切 revision 元数据；status 单调、created_at 不变。
func TestRecordArticleHistory_FixesLegacyMetadata(t *testing.T) {
	s, draftID, base := ownerRevisionFixture(t)
	ctx := t.Context()
	draft, err := s.GetArticleDraft(ctx, draftID)
	check(t, err)
	// 模拟历史遗留： article_revision_id 已绑定但画像/标题/正文全部错误。
	// 画像使用真实存在的第二个画像（FK 约束），元数据仍是错误的。
	wrongProfile, err := s.CreateEditorialProfile(ctx, models.EditorialProfile{Name: "错误画像"})
	check(t, err)
	if _, err := s.DB.ExecContext(ctx,
		`INSERT INTO creation_history (id,editorial_profile_id,status,creation_form,title,content,source_url,article_revision_id)
		 VALUES (lower(hex(randomblob(16))),?,'unpublished','article','错误标题','错误正文','revision:wrong',?)`,
		wrongProfile.ID, base.ID); err != nil {
		t.Fatal(err)
	}
	after, err := s.RecordArticleHistory(ctx, base.ID, "published")
	check(t, err)
	if after.EditorialProfileID != draft.EditorialProfileID {
		t.Fatalf("画像应被校正: %s", after.EditorialProfileID)
	}
	if after.Title != base.Title || after.Content != base.Markdown {
		t.Fatalf("title/content 应被校正: %q %q", after.Title, after.Content)
	}
	if after.SourceURL != "revision:"+base.ID {
		t.Fatalf("source_url 应被校正: %s", after.SourceURL)
	}
	if after.Status != "published" {
		t.Fatalf("status 应为 published: %s", after.Status)
	}
	var n int
	check(t, s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM creation_history WHERE article_revision_id=?`, base.ID).Scan(&n))
	if n != 1 {
		t.Fatalf("仍应单行: %d", n)
	}
}

// TestNoteOnlyArticleNavigation R22（第三轮审计 3）：仅使用 OwnerNote 的文章修订
// 可由 FindUsageByNote 与 FindUsageBySource 找到；相邻/前缀 ID 不误匹配；其他来源不串。
// 注意：本测试仅验证导航查询语义——fixture 直接经 SaveClaimWritingOutput 落库
// （AuthorizedIDs 显式含 note ID）；Writer 冻结输入/ValidateClaimMap/发布门禁对
// OwnerNote 的端到端契约不在 R22 范围，留给 R23 旅程验证。
func TestNoteOnlyArticleNavigation(t *testing.T) {
	s, bA, epA, _ := confirmBriefFixture(t)
	ctx := t.Context()
	// 真实 OwnerNote 挂在来源 A。
	note, err := s.CreateOwnerNote(ctx, models.OwnerNote{
		SourceType: string(models.SourceEpisode), SourceID: epA,
		Kind: "owner_reflection", Content: "来源 A 的个人理解",
		CitationsJSON: "[]", ReferencesJSON: "[]",
	})
	check(t, err)
	// note-only 文章：claim-map 材料只有 note ID（无 KeyPoint）。
	job, err := s.EnqueueClaimWritingForCreationBrief(ctx, bA.ID)
	check(t, err)
	link, err := s.GetCreationArticleLinkByCreationBrief(ctx, bA.ID)
	check(t, err)
	draftObj, err := s.GetArticleDraftByBrief(ctx, link.ArticleBriefID)
	check(t, err)
	rev, err := s.SaveClaimWritingOutput(ctx, ClaimWritingOutput{
		DraftID: draftObj.ID, JobID: job.ID,
		CreationBriefID: bA.ID, BriefVersion: bA.ConfirmedVersion,
		CreationArticleLinkID: link.ID, ArticleProposalID: link.ArticleProposalID, ArticleBriefID: link.ArticleBriefID,
		OwnerClaim: "Owner", Title: "笔记文章", Markdown: "# 笔记文章\n\nOwner 的判断在此。",
		ProviderName: "t", ModelName: "m", PromptVersion: provider.ClaimWriterPromptVersion,
		AuthorizedIDs: []string{note.ID},
		Entries:       []models.ClaimMapEntry{{Excerpt: "Owner 的判断在此。", ClaimKind: provider.ClaimOwner, MaterialIDs: []string{note.ID}}},
	})
	check(t, err)
	// FindUsageByNote：返回精读（无）+ 文章（精确 anchor）。
	usages, err := s.FindUsageByNote(ctx, note.ID)
	check(t, err)
	var articleFound bool
	wantLink := "/workbench/drafts/" + draftObj.ID + "#revision-" + rev.ID
	for _, u := range usages {
		if u.Kind == "article" && u.Link == wantLink {
			articleFound = true
		}
	}
	if !articleFound {
		t.Fatalf("note-only 文章应被 FindUsageByNote 找到: %+v", usages)
	}
	// FindUsageBySource（来源 A）：包含该文章。
	srcUsages, err := s.FindUsageBySource(ctx, models.SourceEpisode, epA)
	check(t, err)
	articleFound = false
	for _, u := range srcUsages {
		if u.Kind == "article" && u.Link == wantLink {
			articleFound = true
		}
	}
	if !articleFound {
		t.Fatalf("note-only 文章应被 FindUsageBySource 找到: %+v", srcUsages)
	}
	// 相邻/前缀 ID 不误匹配。
	near := note.ID[:len(note.ID)-1] + "0"
	if near == note.ID {
		near = note.ID + "0"
	}
	nearUsages, err := s.articleUsagesByMaterials(ctx, []string{near})
	check(t, err)
	if len(nearUsages) != 0 {
		t.Fatalf("相邻 ID 不得误匹配: %+v", nearUsages)
	}
}

// TestFindUsageByNote_GhostNoteEmpty R22（第四轮审计 4）：claim_map_entries 中
// 手工放入不存在的 note ID，FindUsageByNote(ghost) 仍为空——不给不存在笔记
// 伪造文章使用记录。
func TestFindUsageByNote_GhostNoteEmpty(t *testing.T) {
	s, _, _, revID, _ := reviewedRevisionFixture(t)
	ctx := t.Context()
	if _, err := s.DB.ExecContext(ctx,
		`UPDATE claim_map_entries SET material_ids_json='["ghost-note-id"]' WHERE revision_id=?`, revID); err != nil {
		t.Fatal(err)
	}
	usages, err := s.FindUsageByNote(ctx, "ghost-note-id")
	check(t, err)
	if len(usages) != 0 {
		t.Fatalf("ghost note 不得产生使用记录: %+v", usages)
	}
}
