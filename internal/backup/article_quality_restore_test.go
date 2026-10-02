package backup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	jobqueue "github.com/woyin/orangecast/internal/queue"
	"github.com/woyin/orangecast/internal/store"
)

// Restore must preserve separately accepted Owner observations, not recreate cases
// from current article text or lose the purge triggers in the restored database.
func TestArticleQualityV2RestorePreservesOwnerVersionsAndPurgeBoundary(t *testing.T) {
	ctx := t.Context()
	root := t.TempDir()
	evidence := filepath.Join(root, "evidence")
	if err := os.MkdirAll(evidence, 0700); err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(filepath.Join(root, dbFileName))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	pod, err := s.CreatePodcast(ctx, "https://quality-restore.test/feed", "冻结来源", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.MergeEpisodes(ctx, pod.ID, []models.Episode{{GUID: "quality", Title: "原始来源", AudioURL: "https://quality-restore.test/audio"}}); err != nil {
		t.Fatal(err)
	}
	episodes, err := s.ListEpisodes(ctx, pod.ID)
	if err != nil || len(episodes) != 1 {
		t.Fatal(episodes, err)
	}
	source := episodes[0].ID
	if _, err = s.DB.ExecContext(ctx, `UPDATE episodes SET model_data_policy='external_allowed' WHERE id=?`, source); err != nil {
		t.Fatal(err)
	}
	profile, err := s.CreateEditorialProfile(ctx, models.EditorialProfile{Name: "学习", TargetAudience: "自己", Voice: "具体"})
	if err != nil {
		t.Fatal(err)
	}
	req := provider.KnowledgeArticleRequest{Materials: []provider.KnowledgeMaterial{{ID: "material-frozen", Kind: "keypoint", SourceType: "episode", SourceID: source, Version: 1}}}
	blocks := []provider.KnowledgeBlock{{Kind: "synthesis", Text: "原始来源的冻结表达", MaterialIDs: []string{"material-frozen"}}}
	input, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(blocks)
	if err != nil {
		t.Fatal(err)
	}
	article := uuid.NewString()
	// Seed an already completed article: this test exercises backup, not provider calls.
	if _, err = s.DB.ExecContext(ctx, `INSERT INTO knowledge_articles(id,profile_id,input_hash,input_json,status,stage,title,blocks_json,provider,model,working_revision) VALUES(?,?,?,?,'needs_review','review','冻结文章',?,'fixture','writer',1)`, article, profile.ID, "frozen-input", string(input), string(body)); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.ExecContext(ctx, `INSERT INTO knowledge_article_revisions(article_id,revision,title,input_json,blocks_json,content_hash,origin,provider,model,prompt_version) VALUES(?,1,'冻结文章',?,?,'exact-frozen-content','owner','fixture','writer','v1')`, article, string(input), string(body)); err != nil {
		t.Fatal(err)
	}
	revision, err := s.GetKnowledgeRevision(ctx, article, 1)
	if err != nil {
		t.Fatal(err)
	}
	feedback, err := s.RecordArticleQualityFeedback(ctx, article, 1, -1, revision.ContentHash, "", "useful", "Owner 的独立反馈")
	if err != nil {
		t.Fatal(err)
	}
	one, err := s.AcceptArticleQualityCase(ctx, feedback, uuid.NewString(), 0, "Owner 第一版期望")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ClassifyArticleQualityCase(ctx, one.ID, 1, "capacity_omission", `{"owner_confirmed":"冻结窗口未读"}`); err != nil {
		t.Fatal(err)
	}
	one, err = s.GetArticleQualityCase(ctx, one.ID)
	if err != nil {
		t.Fatal(err)
	}
	two, err := s.AcceptArticleQualityCase(ctx, feedback, uuid.NewString(), 1, "Owner 第二版期望")
	if err != nil {
		t.Fatal(err)
	}
	observations, err := s.ListArticleQualityFeedback(ctx)
	if err != nil {
		t.Fatal(err)
	}
	before, err := s.BuildArticleQualityManifest(ctx, []string{one.ID, two.ID}, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "quality-v2.tar.gz")
	manifest, err := Create(ctx, s, evidence, archive)
	if err != nil || manifest.Version != 2 {
		t.Fatal(manifest, err)
	}
	dst := filepath.Join(t.TempDir(), "restored")
	manifest, err = Restore(ctx, archive, dst, false)
	if err != nil || manifest.Version != 2 {
		t.Fatal(manifest, err)
	}
	restored, err := store.Open(filepath.Join(dst, dbFileName))
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	actualRevision, err := restored.GetKnowledgeRevision(ctx, article, 1)
	if err != nil || !reflect.DeepEqual(revision, actualRevision) {
		t.Fatal("revision changed", actualRevision, err)
	}
	actualFeedback, err := restored.ListArticleQualityFeedback(ctx)
	if err != nil || !reflect.DeepEqual(observations, actualFeedback) {
		t.Fatal("Owner feedback changed", actualFeedback, err)
	}
	for _, want := range []*store.ArticleQualityCase{one, two} {
		actual, err := restored.GetArticleQualityCase(ctx, want.ID)
		if err != nil || !reflect.DeepEqual(want, actual) {
			t.Fatal("frozen case changed", actual, err)
		}
	}
	after, err := restored.BuildArticleQualityManifest(ctx, []string{one.ID, two.ID}, "fixture")
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("manifest fingerprint changed", after, err)
	}
	purger := jobqueue.NewWorker(restored, nil, filepath.Join(dst, "tmp"), filepath.Join(dst, "evidence"), filepath.Join(dst, "narrations"))
	if err = purger.PurgeSource(ctx, models.SourceEpisode, source); err != nil {
		t.Fatal(err)
	}
	for _, want := range []*store.ArticleQualityCase{one, two} {
		actual, err := restored.GetArticleQualityCase(ctx, want.ID)
		if err != nil || actual.InputJSON != "" || actual.BlocksJSON != "" || actual.State != "unavailable" || actual.Expected != want.Expected || actual.Version != want.Version || actual.Fingerprint != want.Fingerprint || actual.Classification != want.Classification || actual.ClassificationEvidence != want.ClassificationEvidence {
			t.Fatal("purge violated Owner/body boundary", actual, err)
		}
		if _, err = restored.BuildArticleQualityManifest(ctx, []string{want.ID}, "fixture"); err == nil {
			t.Fatal("purged case externally exportable")
		}
	}
	actualFeedback, err = restored.ListArticleQualityFeedback(ctx)
	// Titles are a live presentation projection; purging the article changes that
	// label, while the immutable Owner feedback itself must remain identical.
	if len(actualFeedback) != len(observations) {
		t.Fatal("purge changed feedback count", err)
	}
	for i := range actualFeedback {
		actualFeedback[i].ArticleTitle = observations[i].ArticleTitle
	}
	if err != nil || !reflect.DeepEqual(observations, actualFeedback) {
		t.Fatal("purge removed Owner feedback", actualFeedback, err)
	}
}
