package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/store"
)

// The fixture first completes the actual same-question module chain. These
// terminal faults use production Owner routes; no paid facts are fabricated.
func TestPersonalLearningV4SameFixtureStopAndPurge(t *testing.T) {
	srv, cookie, f, qCalls, aCalls := personalV4Seed(t)
	personalV4RunJourney(t, srv, cookie, f, qCalls, aCalls)
	ctx := t.Context()
	ledger := func() string {
		t.Helper()
		rows, err := srv.store.DB.QueryContext(ctx, `SELECT * FROM usage_records ORDER BY id`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		names, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		var result []map[string]any
		for rows.Next() {
			values := make([]any, len(names))
			pointers := make([]any, len(names))
			for i := range values {
				pointers[i] = &values[i]
			}
			if err = rows.Scan(pointers...); err != nil {
				t.Fatal(err)
			}
			row := map[string]any{}
			for i, name := range names {
				if b, ok := values[i].([]byte); ok {
					row[name] = string(b)
				} else {
					row[name] = values[i]
				}
			}
			result = append(result, row)
		}
		if err = rows.Err(); err != nil {
			t.Fatal(err)
		}
		if len(result) != 8 {
			t.Fatal("journey did not produce eight genuine receipts", len(result))
		}
		b, err := json.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	beforeLedger := ledger()
	beforeQ, beforeA := qCalls.Load(), aCalls.Load()
	head, err := srv.store.UnderstandingHead(ctx, f.Questions[0])
	if err != nil {
		t.Fatal(err)
	}
	understanding, err := srv.store.GetUnderstandingSnapshot(ctx, head.CurrentSnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	reflection, err := srv.store.GetOwnerNote(ctx, f.ReflectionNote)
	if err != nil {
		t.Fatal(err)
	}
	exports, err := srv.store.ListLearningExports(ctx)
	if err != nil || len(exports) != 1 {
		t.Fatal(exports, err)
	}
	exportURL := "/learning-exports/" + exports[0].ID + "/download"

	// Freeze an independent device pack and an unsent Owner-authored operation.
	device := uuid.NewString()
	response := queueJSONRequest(t, srv, cookie, "POST", "/api/offline/enable", fmt.Sprintf(`{"device_id":%q}`, device), true)
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	response = queueJSONRequest(t, srv, cookie, "POST", "/api/offline/manifest", fmt.Sprintf(`{"device_id":%q,"sources":[{"source_type":"episode","source_id":%q}]}`, device, f.Sources[0]), true)
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	var manifest OfflineManifest
	if err = json.Unmarshal(response.Body.Bytes(), &manifest); err != nil || len(manifest.Files) == 0 {
		t.Fatal(manifest, err)
	}
	var audioURL string
	for _, file := range manifest.Files {
		if file.Kind == "audio" {
			audioURL = file.URL
			break
		}
	}
	if audioURL == "" {
		t.Fatal("no actual audio file in pack")
	}
	if response = doWithCookie(srv, cookie, "GET", audioURL); response.Code != 200 {
		t.Fatal("pack not usable before fault", response.Code, response.Body.String())
	}
	pending := store.OfflineOperation{SchemaVersion: 1, Namespace: manifest.Namespace, UUID: uuid.NewString(), Kind: "organizing_draft", SourceType: models.SourceEpisode, SourceID: f.Sources[0], SnapshotID: f.Snapshots[0], EditedAt: time.Now().Unix(), Payload: json.RawMessage(`{"content":"同题清理后仍可复制的未同步个人文字"}`)}
	pendingJSON, _ := json.Marshal(pending)

	session, err := srv.store.StartQuestionStudySession(ctx, f.Questions[0], uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	client, err := srv.selector.QuestionStudy("journey-generate", "journey-review")
	if err != nil {
		t.Fatal(err)
	}
	turn, job, _, err := srv.store.SubmitQuestionStudyTurn(ctx, session.ID, session.Revision, "停止后保留的Owner下一问", uuid.NewString(), []string{"note:" + f.Notes[0], "note:" + f.Notes[1], "note:" + f.Notes[2]}, client.Config())
	if err != nil {
		t.Fatal(err)
	}
	var controlRevision int
	err = srv.store.DB.QueryRowContext(ctx, `SELECT control_revision FROM processing_jobs WHERE id=?`, job.ID).Scan(&controlRevision)
	if err != nil {
		t.Fatal(err)
	}
	response = postForm(t, srv, cookie, "/automation/action", url.Values{"kind": {"job"}, "target": {job.ID}, "action": {"stop"}, "reason": {"Owner明确停止当前同题尝试"}, "expected_revision": {strconv.Itoa(controlRevision)}, "request_key": {uuid.NewString()}}.Encode())
	if response.Code != http.StatusSeeOther {
		t.Fatal("real Owner stop", response.Code, response.Body.String())
	}
	_ = srv.worker.ProcessOne(ctx)
	stopped, err := srv.store.GetQuestionStudyTurn(ctx, turn.ID)
	if err != nil || stopped.OwnerInput != turn.OwnerInput {
		t.Fatal("stop lost Owner question", stopped, err)
	}
	currentOwnerNote, err := srv.store.GetOwnerNote(ctx, f.ReflectionNote)
	if err != nil || currentOwnerNote.Content != reflection.Content {
		t.Fatal("stop lost existing reflection", currentOwnerNote, err)
	}
	currentUnderstanding, err := srv.store.GetUnderstandingSnapshot(ctx, head.CurrentSnapshotID)
	if err != nil || currentUnderstanding.Answer != understanding.Answer {
		t.Fatal("stop lost immutable Owner understanding", currentUnderstanding, err)
	}
	if ledger() != beforeLedger || qCalls.Load() != beforeQ || aCalls.Load() != beforeA {
		t.Fatal("stop altered paid facts or dispatched", qCalls.Load(), aCalls.Load())
	}

	// Purge is the authenticated production endpoint, including filesystem cleanup.
	response = postForm(t, srv, cookie, "/api/purge", url.Values{"source_type": {"episode"}, "source_id": {f.Sources[0]}}.Encode())
	if response.Code != http.StatusSeeOther {
		t.Fatal("real source purge", response.Code, response.Body.String())
	}
	if _, err = srv.store.GetEpisodeByID(ctx, f.Sources[0]); err != store.ErrNotFound {
		t.Fatal("source remained", err)
	}
	stopped, err = srv.store.GetQuestionStudyTurn(ctx, turn.ID)
	if err != nil || !stopped.Purged || stopped.OwnerInput != "" || stopped.FrozenJSON != "{}" || stopped.AcceptedJSON != "" {
		t.Fatal("source-bound private turn was repopulated", stopped, err)
	}
	execution, err := srv.store.GetJobExecution(ctx, job.ID)
	if err != nil || execution.InputSnapshotJSON != "{}" || execution.CheckpointJSON != "" {
		t.Fatal("purge retained dispatch material", execution, err)
	}
	if _, err = srv.store.GetOwnerNote(ctx, f.ReflectionNote); err != store.ErrNotFound {
		t.Fatal("source-bound reflection should follow explicit privacy purge", err)
	}
	currentUnderstanding, err = srv.store.GetUnderstandingSnapshot(ctx, head.CurrentSnapshotID)
	if err != nil || currentUnderstanding.Answer != understanding.Answer || len(currentUnderstanding.References) == 0 {
		t.Fatal("purge lost Owner understanding", currentUnderstanding, err)
	}
	for _, ref := range currentUnderstanding.References {
		if ref.SourceID == f.Sources[0] && (!ref.Purged || ref.Body != "") {
			t.Fatal("purge left frozen reference text", ref)
		}
	}
	if _, err = srv.store.GetKnowledgeArticle(ctx, f.Articles[0]); err != store.ErrNotFound {
		t.Fatal("direct-source derived article survived", err)
	}
	drafts, err := srv.store.ListOfflineOrganizingDrafts(ctx)
	if err != nil || len(drafts) != 1 || drafts[0].Content != "同旅程离线整理：明确区分自己的理解" || drafts[0].SourceAvailable {
		t.Fatal("purge failed to keep copyable Owner draft with unavailable anchor", drafts, err)
	}
	response = doWithCookie(srv, cookie, "GET", exportURL)
	if response.Code < 400 {
		t.Fatal("stale ZIP still downloadable", response.Code)
	}
	response = doWithCookie(srv, cookie, "GET", "/api/offline/status?device_id="+url.QueryEscape(device))
	if response.Code < 400 {
		t.Fatal("old offline pack still authorized", response.Code)
	}
	response = doWithCookie(srv, cookie, "GET", audioURL)
	if response.Code < 400 {
		t.Fatal("old offline audio still served", response.Code)
	}
	response = offlineAcceptanceSync(t, srv, cookie, device, manifest, pending)
	var rejected struct {
		Results []struct {
			UUID   string `json:"uuid"`
			Status string `json:"status"`
		} `json:"results"`
	}
	if err = json.Unmarshal(response.Body.Bytes(), &rejected); err != nil || len(rejected.Results) != 1 || rejected.Results[0].UUID != pending.UUID || (rejected.Results[0].Status != "unavailable" && rejected.Results[0].Status != "conflict") {
		t.Fatal("purged source accepted old pending operation", response.Code, response.Body.String(), err)
	}
	drafts, err = srv.store.ListOfflineOrganizingDrafts(ctx)
	if err != nil || len(drafts) != 1 {
		t.Fatal("rejected operation created another draft", drafts, err)
	}
	afterPending, _ := json.Marshal(pending)
	if !reflect.DeepEqual(pendingJSON, afterPending) || !strings.Contains(string(afterPending), "同题清理后仍可复制的未同步个人文字") {
		t.Fatal("rejected command changed retained caller draft")
	}
	_ = srv.worker.ProcessOne(ctx)
	if ledger() != beforeLedger || qCalls.Load() != beforeQ || aCalls.Load() != beforeA {
		t.Fatal("purge changed genuine paid facts or invoked model", qCalls.Load(), aCalls.Load())
	}
	t.Logf("SAME_FIXTURE_FAULTS question=%s article=%s receipts=8 model_before=%d/%d model_after=%d/%d stop=OwnerHTTP purge=OwnerHTTP oldZIP=denied offlinePack=denied sourceDerived=cleared OwnerUnderstanding=retained committedOfflineDraft=retained callerPending=unchanged", f.Questions[0], f.Articles[0], beforeQ, beforeA, qCalls.Load(), aCalls.Load())
}
