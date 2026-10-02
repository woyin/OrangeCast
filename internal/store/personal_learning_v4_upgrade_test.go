package store

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/models"
)

// TestPersonalLearningV4UpgradePreservesExecutionFacts exercises the actual
// 0069 prefix, upgrade, and database backup without rewriting historical inputs
// or inventing receipts for an unknown remote response.
func TestPersonalLearningV4UpgradePreservesExecutionFacts(t *testing.T) {
	old, path := historicalTestStore(t, 69)
	ctx := t.Context()
	question, err := old.CreateLearningQuestion(ctx, LearningQuestion{Body: "升级后如何保留个人理解与付费事实？"})
	if err != nil {
		t.Fatal(err)
	}
	doc, err := old.CreatePastedDocument(ctx, "升级固定材料", "只保留已经发生的事实，不虚构未知响应。")
	if err != nil {
		t.Fatal(err)
	}
	note, err := old.CreateOwnerNote(ctx, models.OwnerNote{SourceType: "document", SourceID: doc.ID, Kind: "owner_reflection", Content: "这是升级前的个人文字。"})
	if err != nil {
		t.Fatal(err)
	}
	// Seed the old schema with its original columns; current session methods
	// now require the C08 revision column introduced after this prefix.
	sessionID, messageID := uuid.NewString(), uuid.NewString()
	if _, err = old.DB.ExecContext(ctx, `INSERT INTO study_sessions(id,source_type,source_id,title) VALUES(?,'document',?,'旧单来源会话')`, sessionID, doc.ID); err != nil {
		t.Fatal(err)
	}
	const oldAnswer = "旧回答不能补造新账本。"
	if _, err = old.DB.ExecContext(ctx, `INSERT INTO study_messages(id,session_id,role,content) VALUES(?,?,'assistant',?)`, messageID, sessionID, oldAnswer); err != nil {
		t.Fatal(err)
	}
	profile, err := old.CreateEditorialProfile(ctx, models.EditorialProfile{Name: "旧文章画像", TargetAudience: "个人", Voice: "清楚", StyleGuide: "保留条件"})
	if err != nil {
		t.Fatal(err)
	}
	articleID := uuid.NewString()
	const articleInput = `{"prompt_version":"knowledge-article-v4","stage":"write","materials":[]}`
	const oldBlocks = `[{"kind":"synthesis","text":"升级前已通过的文章正文。","material_ids":[]}]`
	if _, err = old.DB.ExecContext(ctx, `INSERT INTO knowledge_articles(id,profile_id,input_hash,input_json,status,stage,title,blocks_json,provider,model,working_revision,passed_revision,review_model) VALUES(?,?,?,?,'ready','review','旧通过文章',?,'pod','historical-writer',1,1,'historical-reviewer')`, articleID, profile.ID, "historical-input-hash", articleInput, oldBlocks); err != nil {
		t.Fatal(err)
	}
	if _, err = old.DB.ExecContext(ctx, `INSERT INTO knowledge_article_revisions(article_id,revision,title,input_json,blocks_json,content_hash,origin,provider,model,prompt_version) VALUES(?,1,'旧通过文章',?,?,'historical-content-hash','model','pod','historical-writer','knowledge-article-v4')`, articleID, articleInput, oldBlocks); err != nil {
		t.Fatal(err)
	}
	if _, err = old.DB.ExecContext(ctx, `INSERT INTO knowledge_article_reviews(id,article_id,revision,content_hash,passed,issues_json,provider,model,prompt_version) VALUES(?,?,1,'historical-content-hash',1,'[]','pod','historical-reviewer','knowledge-article-v4')`, uuid.NewString(), articleID); err != nil {
		t.Fatal(err)
	}
	ep := seedEpisodeForArtifact(t, old)
	queueID := uuid.NewString()
	if _, err = old.DB.ExecContext(ctx, `INSERT INTO listening_queue_entries(id,source_type,source_id,mode,audio_sha256,title,position,created_at) VALUES(?,'episode',?,'original','old-audio-sha','旧稍后听',3,'2026-09-01')`, queueID, ep); err != nil {
		t.Fatal(err)
	}
	if _, err = old.DB.ExecContext(ctx, `UPDATE listening_queue_state SET revision=7,current_item_id=?,autoplay=1 WHERE id=1`, queueID); err != nil {
		t.Fatal(err)
	}
	knownID, unknownID := uuid.NewString(), uuid.NewString()
	for _, id := range []string{knownID, unknownID} {
		state, checkpoint := "unknown", ""
		if id == knownID {
			state, checkpoint = "complete", `{"stage":"write","response":{"article":"已取得响应"}}`
		}
		_, err = old.DB.ExecContext(ctx, `INSERT INTO processing_jobs
			(id,source_type,source_id,job_type,status,intent_id,input_snapshot_json,
			config_version,configured_provider,configured_model,checkpoint_json,result_state,
			remote_call_started,attempt_count,stop_requested,control_revision)
			VALUES(?,'knowledge_article',?,'knowledge_article','failed',?,?,'knowledge-article-v4','pod','historical-writer',?,?,1,1,1,2)`,
			id, articleID, id, `{"stage":"write","request":{"prompt_version":"knowledge-article-v4","stage":"write"}}`, checkpoint, state)
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err = old.DB.ExecContext(ctx, `INSERT INTO budget_reservations
		(id,job_id,operation,estimated_cost_cents,status,actual_cost_cents)
		VALUES(?,?,'knowledge_article',31,'settled',17),(?,?,'knowledge_article',31,'released_unknown',NULL)`,
		uuid.NewString(), knownID, uuid.NewString(), unknownID); err != nil {
		t.Fatal(err)
	}
	if _, err = old.DB.ExecContext(ctx, `UPDATE run_controls SET paused=1,revision=2,reason='升级前暂停' WHERE kind='lane' AND target='knowledge'`); err != nil {
		t.Fatal(err)
	}
	want := map[string]*models.ProcessingJobExecution{}
	for _, id := range []string{knownID, unknownID} {
		want[id], err = old.GetJobExecution(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
	}
	if err = old.Close(); err != nil {
		t.Fatal(err)
	}
	upgraded, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	backup := filepath.Join(t.TempDir(), "v4-backup.db")
	if err = ConsistencyBackup(ctx, upgraded.DB, backup); err != nil {
		t.Fatal(err)
	}
	restored, err := Open(backup)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	for name, current := range map[string]*Store{"upgraded": upgraded, "restored": restored} {
		t.Run(name, func(t *testing.T) {
			var input, blocks, hash, reviewModel string
			var working, passed int
			if err := current.DB.QueryRowContext(ctx, `SELECT input_json,blocks_json,input_hash,review_model,working_revision,passed_revision FROM knowledge_articles WHERE id=?`, articleID).Scan(&input, &blocks, &hash, &reviewModel, &working, &passed); err != nil || input != articleInput || blocks != oldBlocks || hash != "historical-input-hash" || reviewModel != "historical-reviewer" || working != 1 || passed != 1 {
				t.Fatal("historical article changed", input, blocks, hash, working, passed, err)
			}
			var body, revisionHash string
			if err := current.DB.QueryRowContext(ctx, `SELECT blocks_json,content_hash FROM knowledge_article_revisions WHERE article_id=? AND revision=1`, articleID).Scan(&body, &revisionHash); err != nil || body != oldBlocks || revisionHash != "historical-content-hash" {
				t.Fatal("historical passed revision changed", body, revisionHash, err)
			}
			var item, mode, audioSHA, title, created string
			var queueRevision, autoplay, position int
			if err := current.DB.QueryRowContext(ctx, `SELECT revision,current_item_id,autoplay FROM listening_queue_state WHERE id=1`).Scan(&queueRevision, &item, &autoplay); err != nil || queueRevision != 7 || item != queueID || autoplay != 1 {
				t.Fatal("historical current queue changed", queueRevision, item, autoplay, err)
			}
			if err := current.DB.QueryRowContext(ctx, `SELECT mode,audio_sha256,title,position,created_at FROM listening_queue_entries WHERE id=?`, queueID).Scan(&mode, &audioSHA, &title, &position, &created); err != nil || mode != "original" || audioSHA != "old-audio-sha" || title != "旧稍后听" || position != 3 || created != "2026-09-01" {
				t.Fatal("historical queue identity changed", mode, audioSHA, title, position, created, err)
			}
			for id, before := range want {
				after, err := current.GetJobExecution(ctx, id)
				if err != nil || !reflect.DeepEqual(before, after) {
					t.Fatalf("historical execution changed: before=%+v after=%+v err=%v", before, after, err)
				}
			}
			gotQuestion, err := current.GetLearningQuestion(ctx, question.ID)
			if err != nil || gotQuestion.Body != question.Body || gotQuestion.Revision != question.Revision {
				t.Fatal("question changed", gotQuestion, err)
			}
			gotNote, err := current.GetOwnerNote(ctx, note.ID)
			if err != nil || gotNote.Content != note.Content || gotNote.Revision != note.Revision {
				t.Fatal("owner text changed", gotNote, err)
			}
			gotMessage, err := current.GetStudyMessage(ctx, messageID)
			if err != nil || gotMessage.Content != oldAnswer {
				t.Fatal("legacy message changed", gotMessage, err)
			}
			control, err := current.GetRunControl(ctx, "lane", "knowledge")
			if err != nil || !control.Paused || control.Revision != 2 {
				t.Fatal("control changed", control, err)
			}
			var unknownCost *int
			var state string
			if err = current.DB.QueryRowContext(ctx, `SELECT status,actual_cost_cents FROM budget_reservations WHERE job_id=?`, unknownID).Scan(&state, &unknownCost); err != nil || state != "released_unknown" || unknownCost != nil {
				t.Fatal("unknown cost was fabricated", state, unknownCost, err)
			}
			var bad int
			if err = current.DB.QueryRowContext(ctx, `SELECT count(*) FROM pragma_foreign_key_check`).Scan(&bad); err != nil || bad != 0 {
				t.Fatal("foreign key integrity", bad, err)
			}
		})
	}
}
