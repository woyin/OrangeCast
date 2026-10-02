package server

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/store"
	"net"
	"net/http"
	"os"
	"testing"
	"time"
)

// TestUnderstandingPurgeBrowserHarness uses scratch data and real purge triggers; no external AI runs.
func TestUnderstandingPurgeBrowserHarness(t *testing.T) {
	if os.Getenv("CWP_UNDERSTANDING_PURGE_BROWSER") != "1" {
		t.Skip("explicit browser harness only")
	}
	for _, signal := range []string{"/tmp/cwp-understanding-purge.trigger", "/tmp/cwp-understanding-purge.stop"} {
		if err := os.Remove(signal); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
	srv := newTestServer(t)
	claimOwnerAndLogin(t, srv, browserAcceptanceEmail, browserAcceptancePassword)
	ep, noteID := seedKnowledgeLearning(t, srv)
	note, e := srv.store.GetOwnerNote(t.Context(), noteID)
	if e != nil {
		t.Fatal(e)
	}
	q, e := srv.store.CreateLearningQuestion(t.Context(), store.LearningQuestion{Body: "浏览器验证来源清理与我的理解"})
	if e != nil {
		t.Fatal(e)
	}
	v, e := srv.store.SaveUnderstanding(t.Context(), store.SaveUnderstandingCommand{QuestionID: q.ID, QuestionRevision: q.Revision, RequestKey: uuid.NewString(), Answer: "Owner答案：来源清理后仍保留我的解释。", References: []store.UnderstandingReference{{Kind: "note", ObjectID: noteID, Version: note.Revision}}})
	if e != nil {
		t.Fatal(e)
	}
	if e = srv.store.ChooseCurrentUnderstanding(t.Context(), q.ID, v.ID, q.Revision, 1, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	ln, e := net.Listen("tcp", "127.0.0.1:18094")
	if e != nil {
		t.Fatal(e)
	}
	server := &http.Server{Handler: srv.Router(), ReadHeaderTimeout: 5 * time.Second}
	go server.Serve(ln)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if e := server.Shutdown(ctx); e != nil {
			t.Error(e)
		}
	}()
	raw, _ := json.Marshal(map[string]string{"url": "http://127.0.0.1:18094/questions/" + q.ID + "/understandings", "email": browserAcceptanceEmail, "password": browserAcceptancePassword, "reference_body": note.Content, "snapshot_id": v.ID, "source_id": ep})
	fmt.Printf("UNDERSTANDING_PURGE_BROWSER_READY=%s\n", raw)
	purged := false
	for {
		if _, e = os.Stat("/tmp/cwp-understanding-purge.trigger"); e == nil && !purged {
			if _, e = srv.store.DB.ExecContext(t.Context(), `DELETE FROM episodes WHERE id=?`, ep); e != nil {
				t.Fatal(e)
			}
			v, e = srv.store.GetUnderstandingSnapshot(t.Context(), v.ID)
			if e != nil || len(v.References) != 1 || !v.References[0].Purged || v.References[0].Body != "" {
				t.Fatal(v, e)
			}
			purged = true
			fmt.Println("UNDERSTANDING_PURGE_BROWSER_PURGED=true")
		}
		if _, e = os.Stat("/tmp/cwp-understanding-purge.stop"); e == nil {
			if !purged {
				t.Fatal("purge was not exercised")
			}
			return
		}
		select {
		case <-t.Context().Done():
			t.Fatal(t.Context().Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
}
