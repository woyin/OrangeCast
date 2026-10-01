package store

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/models"
)

func seedRun(t *testing.T, s *Store, source, body string) string {
	t.Helper()
	id := uuid.NewString()
	_, err := s.DB.Exec(`INSERT INTO processing_jobs(id,source_type,source_id,job_type,status,input_snapshot_json,configured_provider,configured_model)VALUES(?,'knowledge_article',?,'knowledge_article','queued',?,'pod','frozen-model')`, id, source, body)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
func TestRunControlAdmissionClaimCASAndReplay(t *testing.T) {
	s, _, _, _ := knowledgeStoreFixture(t)
	ctx := t.Context()
	a := seedRun(t, s, "direction-a", `{"stage":"discover"}`)
	b := seedRun(t, s, "direction-b", `{"stage":"write"}`)
	key := uuid.NewString()
	change := func() error { return s.ChangeRunControl(ctx, "job", b, "priority", "先处理", key, 1, 10) }
	if err := change(); err != nil {
		t.Fatal(err)
	}
	if err := change(); err != nil {
		t.Fatal(err)
	}
	if err := s.ChangeRunControl(ctx, "job", b, "priority", "不同理由", key, 1, 10); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	j, err := s.ClaimNextJob(ctx, "+1 minute")
	if err != nil || j.ID != b {
		t.Fatal(j, err)
	}
	if err = s.ChangeRunControl(ctx, "job", b, "stop", "旧窗口", uuid.NewString(), 2, 0); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	if err = s.ChangeRunControl(ctx, "job", b, "priority", "已领取", uuid.NewString(), 3, 1); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	if err = s.ChangeRunControl(ctx, "job", b, "stop", "停止下一阶段", uuid.NewString(), 3, 0); err != nil {
		t.Fatal(err)
	}
	if err = s.MarkJobRemoteCallStarted(ctx, b); !errors.Is(err, ErrRunControlled) {
		t.Fatal(err)
	}
	if err = s.SaveJobCheckpoint(ctx, b, `{"known_response":true}`); err != nil {
		t.Fatal(err)
	}
	if err = s.ChangeRunControl(ctx, "job", a, "stop", "停止排队", uuid.NewString(), 1, 0); err != nil {
		t.Fatal(err)
	}
	if j, err = s.ClaimNextJob(ctx, "+1 minute"); err != nil || j != nil {
		t.Fatal(j, err)
	}
	if ok, err := s.MarkJobRunning(ctx, a); err != nil || ok {
		t.Fatal(ok, err)
	}
	if err = s.ResetRunningOnStartup(ctx); err != nil {
		t.Fatal(err)
	}
	if j, err = s.ClaimNextJob(ctx, "+1 minute"); err != nil || j != nil {
		t.Fatal("restart revived stopped job", j, err)
	}
	if err = s.ChangeRunControl(ctx, "job", a, "resume_queue", "明确恢复未外发任务", uuid.NewString(), 2, 0); err != nil {
		t.Fatal(err)
	}
	if j, err = s.ClaimNextJob(ctx, "+1 minute"); err != nil || j.ID != a {
		t.Fatal(j, err)
	}
	control, err := s.GetRunControl(ctx, "direction", "knowledge_article:direction-c")
	if !errors.Is(err, ErrNotFound) {
		t.Fatal(control, err)
	}
	c := seedRun(t, s, "direction-c", `{"stage":"write"}`)
	if err = s.ChangeRunControl(ctx, "direction", "knowledge_article:direction-c", "pause", "暂不写作", uuid.NewString(), 1, 0); err != nil {
		t.Fatal(err)
	}
	control, err = s.GetRunControl(ctx, "direction", "knowledge_article:direction-c")
	if err != nil || !control.Paused || control.Revision != 2 {
		t.Fatal(control, err)
	}
	if _, err = s.DB.Exec(`INSERT INTO processing_jobs(id,source_type,source_id,job_type,status)VALUES(?,'knowledge_article','direction-c','knowledge_article','queued')`, uuid.NewString()); err == nil || !strings.Contains(err.Error(), "Owner运行控制已暂停") {
		t.Fatal(err)
	}
	if err = s.CheckRunControl(ctx, c); !errors.Is(err, ErrRunControlled) {
		t.Fatal(err)
	}
	if err = s.ResetRunningOnStartup(ctx); err != nil {
		t.Fatal(err)
	}
	if err = s.ChangeRunControl(ctx, "direction", "knowledge_article:direction-c", "resume", "核对后恢复", uuid.NewString(), 2, 0); err != nil {
		t.Fatal(err)
	}
	if err = s.CheckRunControl(ctx, c); err != nil {
		t.Fatal(err)
	}
	if err = s.ChangeRunControl(ctx, "lane", "knowledge", "pause", "暂停类别", uuid.NewString(), 1, 0); err != nil {
		t.Fatal(err)
	}
	paused, _, err := s.ListRuns(ctx, "paused", "knowledge", 0)
	if err != nil || len(paused) != 2 {
		t.Fatal(len(paused), err)
	}
}
func TestRunControlClaimStopCompetition(t *testing.T) {
	for i := 0; i < 10; i++ {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			s, _, _, _ := knowledgeStoreFixture(t)
			ctx := t.Context()
			id := seedRun(t, s, "race", `{}`)
			var wg sync.WaitGroup
			wg.Add(2)
			var claimed *models.ProcessingJob
			var claimErr, stopErr error
			go func() { defer wg.Done(); claimed, claimErr = s.ClaimNextJob(ctx, "+1 minute") }()
			go func() {
				defer wg.Done()
				stopErr = s.ChangeRunControl(ctx, "job", id, "stop", "并发停止", uuid.NewString(), 1, 0)
			}()
			wg.Wait()
			if claimErr != nil {
				t.Fatal(claimErr)
			}
			if claimed == nil {
				if stopErr != nil {
					t.Fatal(stopErr)
				}
			} else {
				if !errors.Is(stopErr, ErrConflict) {
					t.Fatal("claim and stale queued stop both won", stopErr)
				}
			}
		})
	}
}
func TestRunMetadataCostsPaginationAndInvalidControls(t *testing.T) {
	s, _, _, _ := knowledgeStoreFixture(t)
	ctx := t.Context()
	for i := 0; i < 52; i++ {
		seedRun(t, s, fmt.Sprint(i), `{"stage":"discover"}`)
	}
	runs, next, err := s.ListRuns(ctx, "", "", 0)
	if err != nil || !next || len(runs) != 50 {
		t.Fatal(len(runs), next, err)
	}
	id := runs[0].ID
	second, next, err := s.ListRuns(ctx, "queued", "knowledge", 50)
	if err != nil || next || len(second) != 2 {
		t.Fatal(second, next, err)
	}
	voice := uuid.NewString()
	s.DB.Exec(`INSERT INTO processing_jobs(id,source_type,source_id,job_type,status)VALUES(?,'voice_note','voice','transcribe','queued')`, voice)
	if err = s.RecordUsageReceipt(ctx, models.UsageReceipt{ReceiptID: "known", AttemptID: id, Operation: "knowledge_article_discover", CostKnown: true, CostCents: 7}); err != nil {
		t.Fatal(err)
	}
	s.RecordUsageReceipt(ctx, models.UsageReceipt{ReceiptID: "voice", AttemptID: voice + ":response", Operation: "transcription", InputUnits: 9})
	s.DB.Exec(`UPDATE usage_records SET unit_kind='audio_and_text_tokens',audio_seconds=5 WHERE receipt_id='voice'`)
	s.MarkJobRemoteCallStarted(ctx, voice)
	if err = s.ChangeRunControl(ctx, "job", id, "stop", "保留事实", uuid.NewString(), 1, 0); err != nil {
		t.Fatal(err)
	}
	costs, err := s.GetRunCostSummary(ctx)
	if err != nil || costs.KnownCost != 7 || costs.UnknownTasks != 1 {
		t.Fatal(costs, err)
	}
	vruns, _, err := s.ListRuns(ctx, "", "voice", 0)
	if err != nil || len(vruns) != 1 || vruns[0].InputUnits != 9 || vruns[0].UnknownReceipts != 1 {
		t.Fatal(vruns, err)
	}
	usage, err := s.ListRunUsage(ctx, voice)
	if err != nil || len(usage) != 1 || usage[0].AudioSeconds != 5 || usage[0].CostKnown {
		t.Fatal(usage, err)
	}
	acts, next, err := s.ListRunControlActions(ctx, id, "knowledge_article:0", 0)
	if err != nil || next || len(acts) != 1 {
		t.Fatal(acts, next, err)
	}
	if _, e := s.DB.Exec(`DELETE FROM processing_jobs WHERE id=?`, id); e != nil {
		t.Fatal(e)
	}
	costs, err = s.GetRunCostSummary(ctx)
	if err != nil || costs.KnownCost != 7 || costs.UnknownTasks != 1 {
		t.Fatal("purge hid paid facts", costs, err)
	}
	for _, v := range []struct {
		kind, target, action, key string
		rev, priority             int
	}{{"job", id, "invalid", uuid.NewString(), 1, 0}, {"invalid", id, "pause", uuid.NewString(), 1, 0}, {"job", id, "stop", "invalid", 1, 0}, {"job", id, "stop", uuid.NewString(), 0, 0}, {"job", id, "stop", uuid.NewString(), 1, 11}, {"lane", "missing", "pause", uuid.NewString(), 1, 0}, {"direction", "missing", "pause", uuid.NewString(), 1, 0}, {"job", "missing", "stop", uuid.NewString(), 1, 0}} {
		if e := s.ChangeRunControl(ctx, v.kind, v.target, v.action, "理由", v.key, v.rev, v.priority); e == nil {
			t.Fatal(v)
		}
	}
	if e := s.ChangeRunControl(ctx, "job", id, "stop", "", uuid.NewString(), 1, 0); e == nil {
		t.Fatal("empty reason")
	}
	for _, status := range []string{"succeeded", "failed", "running", "stopped", "paused"} {
		if _, _, e := s.ListRuns(ctx, status, "", 0); e != nil {
			t.Fatal(e)
		}
	}
	if _, _, e := s.ListRuns(ctx, "bad", "", 0); e == nil {
		t.Fatal("bad status")
	}
	if _, _, e := s.ListRuns(ctx, "", "bad", 0); e == nil {
		t.Fatal("bad lane")
	}
	if _, _, e := s.ListRuns(ctx, "", "", -1); e == nil {
		t.Fatal("bad offset")
	}
	if _, _, e := s.ListRunControlActions(ctx, id, "", -1); e == nil {
		t.Fatal("bad actions offset")
	}
	if e := s.CheckRunControl(ctx, "missing"); !errors.Is(e, ErrNotFound) {
		t.Fatal(e)
	}
}

func TestRunControlsSurviveReopenAndUpgradeOldJobs(t *testing.T) {
	s, _, _, _ := knowledgeStoreFixture(t)
	ctx := t.Context()
	id := seedRun(t, s, "restart", `{"stage":"update_propose","request":{"update":{"proposal_id":"old"}}}`)
	var seq int
	var name, path string
	s.DB.QueryRow(`PRAGMA database_list`).Scan(&seq, &name, &path)
	// Reconstruct 0068 schema and verify migration backfills recorded lanes only.
	for _, sql := range []string{`DROP TRIGGER run_control_admission`, `DROP TRIGGER run_control_lane`, `DROP TABLE run_control_actions`, `DROP TABLE run_controls`, `DROP INDEX run_control_claim`, `ALTER TABLE processing_jobs DROP COLUMN run_lane`, `ALTER TABLE processing_jobs DROP COLUMN priority`, `ALTER TABLE processing_jobs DROP COLUMN stop_requested`, `ALTER TABLE processing_jobs DROP COLUMN control_revision`, `DELETE FROM schema_migrations WHERE version=69`} {
		if _, e := s.DB.Exec(sql); e != nil {
			t.Fatal(e)
		}
	}
	s.Close()
	up, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	runs, _, err := up.ListRuns(ctx, "", "updates", 0)
	if err != nil || len(runs) != 1 || runs[0].ID != id || runs[0].Model != "frozen-model" {
		t.Fatal(runs, err)
	}
	if err = up.ChangeRunControl(ctx, "direction", "knowledge_article:restart", "pause", "重启仍暂停", uuid.NewString(), 1, 0); err != nil {
		t.Fatal(err)
	}
	up.Close()
	up, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer up.Close()
	if err = up.ResetRunningOnStartup(ctx); err != nil {
		t.Fatal(err)
	}
	if j, e := up.ClaimNextJob(ctx, "+1 minute"); e != nil || j != nil {
		t.Fatal(j, e)
	}
	c, e := up.GetRunControl(ctx, "direction", "knowledge_article:restart")
	if e != nil || !c.Paused || c.Revision != 2 {
		t.Fatal(c, e)
	}
}

func TestRunControlAuditFailureRollsBackOwnerDecision(t *testing.T) {
	s, _, _, _ := knowledgeStoreFixture(t)
	ctx := t.Context()
	if _, e := s.DB.Exec(`CREATE TRIGGER reject_control_audit BEFORE INSERT ON run_control_actions BEGIN SELECT RAISE(FAIL,'audit disk unavailable'); END`); e != nil {
		t.Fatal(e)
	}
	if e := s.ChangeRunControl(ctx, "lane", "knowledge", "pause", "应全部回滚", uuid.NewString(), 1, 0); e == nil {
		t.Fatal("expected audit failure")
	}
	c, e := s.GetRunControl(ctx, "lane", "knowledge")
	if e != nil || c.Paused || c.Revision != 1 {
		t.Fatal(c, e)
	}
	id := seedRun(t, s, "rollback", `{}`)
	if e = s.ChangeRunControl(ctx, "job", id, "stop", "应全部回滚", uuid.NewString(), 1, 0); e == nil {
		t.Fatal("expected audit failure")
	}
	if e = s.CheckRunControl(ctx, id); e != nil {
		t.Fatal(e)
	}
}
