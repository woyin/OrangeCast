package store

import (
	"errors"
	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/provider"
	"strings"
	"testing"
)

func TestLearningExportBoundedIdentityAndWithdrawal(t *testing.T) {
	s, _, ep, note := knowledgeStoreFixture(t)
	q := createQuestion(t, s)
	q = questionChange(t, s, q, LearningQuestionChange{Action: "link", Link: provider.LearningQuestionLink{Kind: "note", ObjectID: note, SourceType: "episode", SourceID: ep}})
	p, e := s.PreviewLearningExport(t.Context(), LearningExportScope{Kind: "question", ID: q.ID})
	if e != nil {
		t.Fatal(e)
	}
	if p.Count < 3 {
		t.Fatalf("incomplete objects %d", p.Count)
	}
	key := uuid.NewString()
	v, e := s.CreateLearningExport(t.Context(), p.ID, p.Hash, key, "owner")
	if e != nil {
		t.Fatal(e)
	}
	same, e := s.CreateLearningExport(t.Context(), p.ID, p.Hash, key, "owner")
	if e != nil || same.ID != v.ID {
		t.Fatal(same, e)
	}
	if _, e = s.CreateLearningExport(t.Context(), p.ID, "wrong", key, "owner"); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	snap, e := s.LearningExportSnapshot(t.Context(), v.ID)
	if e != nil || len(snap.Objects) < 3 {
		t.Fatal(snap, e)
	}
	if _, e = s.DB.Exec(`UPDATE owner_notes SET content=content||'修改',revision=revision+1 WHERE id=?`, note); e != nil {
		t.Fatal(e)
	}
	if _, e = s.CreateLearningExport(t.Context(), p.ID, p.Hash, uuid.NewString(), "owner"); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	if e = s.PublishLearningExport(t.Context(), v.ID, "/tmp/should-not-publish.zip"); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	fresh, e := s.PreviewLearningExport(t.Context(), LearningExportScope{Kind: "question", ID: q.ID, IncludeHistory: true})
	if e != nil {
		t.Fatal(e)
	}
	historyExport, e := s.CreateLearningExport(t.Context(), fresh.ID, fresh.Hash, uuid.NewString(), "owner")
	if e != nil {
		t.Fatal(e)
	}
	historySnap, e := s.LearningExportSnapshot(t.Context(), historyExport.ID)
	if e != nil {
		t.Fatal(e)
	}
	versions := map[int]bool{}
	for _, o := range historySnap.Objects {
		if o.Kind == "notes" {
			versions[o.Revision] = true
		}
	}
	if len(versions) != 2 || !versions[1] || !versions[2] {
		t.Fatal(versions)
	}
	if _, e = s.PreviewLearningExport(t.Context(), LearningExportScope{Kind: "question", ID: "missing"}); !errors.Is(e, ErrNotFound) {
		t.Fatal(e)
	}
}
func TestLearningExportRequestLimitExpiryAndObjectLimit(t *testing.T) {
	s, _, _, _ := knowledgeStoreFixture(t)
	q := createQuestion(t, s)
	scope := LearningExportScope{Kind: "question", ID: q.ID}
	for i := 0; i < 100; i++ {
		if _, e := s.PreviewLearningExport(t.Context(), scope); e != nil {
			t.Fatal(i, e)
		}
	}
	if _, e := s.PreviewLearningExport(t.Context(), scope); !errors.Is(e, ErrInvalidEditorialState) {
		t.Fatal(e)
	}
	if _, e := s.DB.Exec(`UPDATE learning_export_previews SET expires_at='2000-01-01T00:00:00Z'`); e != nil {
		t.Fatal(e)
	}
	if e := s.CleanupLearningExports(t.Context()); e != nil {
		t.Fatal(e)
	}
	var retained int
	s.DB.QueryRow(`SELECT count(*) FROM learning_export_previews WHERE snapshot_json!='{}'`).Scan(&retained)
	if retained != 0 {
		t.Fatal(retained)
	}
	fresh, e := s.PreviewLearningExport(t.Context(), scope)
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 100; i++ {
		if _, e = s.CreateLearningExport(t.Context(), fresh.ID, fresh.Hash, uuid.NewString(), "owner"); e != nil {
			t.Fatal(i, e)
		}
	}
	if _, e = s.CreateLearningExport(t.Context(), fresh.ID, fresh.Hash, uuid.NewString(), "owner"); !errors.Is(e, ErrInvalidEditorialState) {
		t.Fatal(e)
	}
}

func TestLearningExportThemePassedHistoryDraftAndUnderstanding(t *testing.T) {
	s, profile, ep, _ := knowledgeStoreFixture(t)
	ctx := t.Context()
	q := createQuestion(t, s)
	args := []any{profile, q.ID, profile, q.ID, q.ID, q.ID, ep, ep}
	var e error
	for _, stmt := range strings.Split(`INSERT INTO themes(id,editorial_profile_id,name)VALUES('export-theme',?,'导出主题');UPDATE learning_questions SET theme_id='export-theme' WHERE id=?;INSERT INTO knowledge_articles(id,profile_id,input_hash,input_json,provider,model,passed_revision,working_revision)VALUES('export-article',?,'export-hash','{}','pod','model',1,2);INSERT INTO knowledge_article_revisions(article_id,revision,title,input_json,blocks_json,content_hash,origin,provider,model,prompt_version)VALUES('export-article',1,'通过文章','{}','[{"kind":"source","text":"确切的通过正文","material_ids":[]}]','hash1','owner','pod','m','v'),('export-article',2,'草稿文章','{}','[{"kind":"reflection","text":"未通过草稿正文","material_ids":[]}]','hash2','owner','pod','m','v');INSERT INTO learning_question_links(question_id,kind,object_id,state)VALUES(?,'article','export-article','confirmed');INSERT INTO understanding_snapshots(id,question_id,version,answer,request_key,payload_hash)VALUES('export-understanding',?,1,'跨来源自己的理解','u-request','u-hash');INSERT INTO understanding_heads(question_id,revision,current_snapshot_id)VALUES(?,1,'export-understanding');INSERT INTO understanding_references(snapshot_id,ordinal,kind,object_id,version,source_type,source_id,body)VALUES('export-understanding',1,'source',?,1,'episode',?,'引用正文');`, ";") {
		if strings.TrimSpace(stmt) == "" {
			continue
		}
		n := strings.Count(stmt, "?")
		if _, e = s.DB.Exec(stmt, args[:n]...); e != nil {
			t.Fatal(stmt, e)
		}
		args = args[n:]
	}

	if e != nil {
		t.Fatal(e)
	}
	scope := LearningExportScope{Kind: "theme", ID: "export-theme", PublicURL: "https://example.test", IncludeExcerpts: true}
	p, e := s.PreviewLearningExport(ctx, scope)
	if e != nil {
		t.Fatal(e)
	}
	v, e := s.CreateLearningExport(ctx, p.ID, p.Hash, uuid.NewString(), "owner")
	if e != nil {
		t.Fatal(e)
	}
	snap, e := s.LearningExportSnapshot(ctx, v.ID)
	if e != nil {
		t.Fatal(e)
	}
	passed, understanding, sources := 0, 0, 0
	for _, o := range snap.Objects {
		switch o.Kind {
		case "articles":
			if o.Revision != 1 || o.Status != "passed" || !strings.Contains(o.Body, "确切的通过正文") {
				t.Fatal(o)
			}
			passed++
		case "understandings":
			understanding++
			if len(o.Links) < 2 {
				t.Fatal(o)
			}
		case "sources":
			if o.Revision != 1 || !strings.Contains(o.Body, "保留来源上下文") {
				t.Fatal(o)
			}
			sources++
		}
	}
	if passed != 1 || understanding != 1 || sources != 1 {
		t.Fatal(passed, understanding, sources)
	}
	if _, e = s.DB.Exec(`INSERT INTO article_quality_feedback(id,article_id,revision,content_hash,paragraph_index,paragraph_hash,category,comment)VALUES('export-feedback','export-article',1,'hash1',0,'paragraph','useful','要复用');INSERT INTO article_quality_cases(id,feedback_id,version,expected,classification,input_json,blocks_json,fingerprint)VALUES('export-case','export-feedback',1,'明确出处','material_thin','{}','[{"kind":"source","text":"冻结质量案例正文"}]','fingerprint')`); e != nil {
		t.Fatal(e)
	}
	scope.IncludeDrafts = true
	p, e = s.PreviewLearningExport(ctx, scope)
	if e != nil {
		t.Fatal(e)
	}
	v, e = s.CreateLearningExport(ctx, p.ID, p.Hash, uuid.NewString(), "owner")
	if e != nil {
		t.Fatal(e)
	}
	snap, e = s.LearningExportSnapshot(ctx, v.ID)
	if e != nil {
		t.Fatal(e)
	}
	draft := 0
	cases := 0
	for _, o := range snap.Objects {
		if o.Kind == "cases" {
			cases++
			if !strings.Contains(o.Body, "冻结质量案例正文") || !strings.Contains(o.Body, "明确出处") {
				t.Fatal(o)
			}
		}
		if o.Kind == "articles" && o.Status == "draft" {
			draft++
		}
	}
	if cases != 1 {
		t.Fatal(cases)
	}
	if draft != 1 {
		t.Fatal(draft)
	}
	if _, e = s.DB.Exec(`DELETE FROM episodes WHERE id=?`, ep); e != nil {
		t.Fatal(e)
	}
	p, e = s.PreviewLearningExport(ctx, scope)
	if e != nil {
		t.Fatal(e)
	}
	v, e = s.CreateLearningExport(ctx, p.ID, p.Hash, uuid.NewString(), "owner")
	if e != nil {
		t.Fatal(e)
	}
	snap, e = s.LearningExportSnapshot(ctx, v.ID)
	if e != nil {
		t.Fatal(e)
	}
	for _, o := range snap.Objects {
		if o.Kind == "sources" {
			t.Fatal("purged source included")
		}
		if o.Kind == "understandings" && o.Body != "跨来源自己的理解\n尚不确定：\n下一步：" {
			t.Fatal(o)
		}
	}
}

func TestLearningExportObjectLimitAndOwnerStop(t *testing.T) {
	s, profile, _, _ := knowledgeStoreFixture(t)
	ctx := t.Context()
	if _, e := s.DB.Exec(`INSERT INTO themes(id,editorial_profile_id,name)VALUES('bounded-theme',?,'有界主题')`, profile); e != nil {
		t.Fatal(e)
	}
	if _, e := s.DB.Exec(`WITH RECURSIVE n(i)AS(VALUES(1) UNION ALL SELECT i+1 FROM n WHERE i<501) INSERT INTO learning_questions(id,body,theme_id)SELECT 'bounded-'||i,'问题 '||i,'bounded-theme' FROM n`); e != nil {
		t.Fatal(e)
	}
	if _, e := s.PreviewLearningExport(ctx, LearningExportScope{Kind: "theme", ID: "bounded-theme"}); !errors.Is(e, ErrInvalidEditorialState) {
		t.Fatal(e)
	}
	q := createQuestion(t, s)
	p, e := s.PreviewLearningExport(ctx, LearningExportScope{Kind: "question", ID: q.ID})
	if e != nil {
		t.Fatal(e)
	}
	v, e := s.CreateLearningExport(ctx, p.ID, p.Hash, uuid.NewString(), "owner")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.DB.Exec(`UPDATE processing_jobs SET stop_requested=1 WHERE id=?`, v.JobID); e != nil {
		t.Fatal(e)
	}
	if e = s.PublishLearningExport(ctx, v.ID, "/tmp/stopped-export.zip"); !errors.Is(e, ErrRunControlled) {
		t.Fatal(e)
	}
}
