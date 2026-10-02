package store

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestUnderstandingCommandBoundsAndIdentity(t *testing.T) {
	s, _, _, _ := knowledgeStoreFixture(t)
	q := createQuestion(t, s)
	base := SaveUnderstandingCommand{QuestionID: q.ID, QuestionRevision: q.Revision, RequestKey: uuid.NewString(), Answer: "Owner answer"}
	cases := map[string]func(*SaveUnderstandingCommand){
		"request identity":        func(c *SaveUnderstandingCommand) { c.RequestKey = "bad" },
		"empty answer":            func(c *SaveUnderstandingCommand) { c.Answer = "  " },
		"answer bound":            func(c *SaveUnderstandingCommand) { c.Answer = strings.Repeat("a", 40001) },
		"uncertainty bound":       func(c *SaveUnderstandingCommand) { c.Uncertainty = strings.Repeat("a", 10001) },
		"next step bound":         func(c *SaveUnderstandingCommand) { c.NextStep = strings.Repeat("a", 10001) },
		"question identity bound": func(c *SaveUnderstandingCommand) { c.QuestionID = strings.Repeat("a", 201) },
		"parent identity bound":   func(c *SaveUnderstandingCommand) { c.ParentID = strings.Repeat("a", 201) },
		"provider count bound":    func(c *SaveUnderstandingCommand) { c.ApprovedProviders = make([]string, 101) },
		"reference count bound":   func(c *SaveUnderstandingCommand) { c.References = make([]UnderstandingReference, 101) },
		"reference identity bound": func(c *SaveUnderstandingCommand) {
			c.References = []UnderstandingReference{{Kind: "note", ObjectID: strings.Repeat("a", 301), Version: 1}}
		},
		"reference kind bound": func(c *SaveUnderstandingCommand) {
			c.References = []UnderstandingReference{{Kind: strings.Repeat("a", 41), Version: 1}}
		},
		"reference version": func(c *SaveUnderstandingCommand) { c.References = []UnderstandingReference{{Kind: "note", Version: 0}} },
		"duplicate reference": func(c *SaveUnderstandingCommand) {
			c.References = []UnderstandingReference{{Kind: "note", ObjectID: "n", Version: 1}, {Kind: "note", ObjectID: "n", Version: 1}}
		},
		"invalid policy":             func(c *SaveUnderstandingCommand) { c.ModelDataPolicy = "unknown" },
		"missing approved providers": func(c *SaveUnderstandingCommand) { c.ModelDataPolicy = "approved_providers_only" },
		"empty provider":             func(c *SaveUnderstandingCommand) { c.ApprovedProviders = []string{" "} },
		"provider bound":             func(c *SaveUnderstandingCommand) { c.ApprovedProviders = []string{strings.Repeat("a", 101)} },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			c := base
			change(&c)
			if _, e := s.SaveUnderstanding(t.Context(), c); !errors.Is(e, ErrInvalidEditorialState) {
				t.Fatal(e)
			}
		})
	}
	c := base
	c.QuestionID = "missing"
	if _, e := s.SaveUnderstanding(t.Context(), c); !errors.Is(e, ErrNotFound) {
		t.Fatal(e)
	}
	c = base
	c.ParentID = "missing"
	if _, e := s.SaveUnderstanding(t.Context(), c); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	c = base
	c.HeadRevision = 1
	if _, e := s.SaveUnderstanding(t.Context(), c); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	c = base
	c.ModelDataPolicy = "approved_providers_only"
	c.ApprovedProviders = []string{" POD "}
	v, e := s.SaveUnderstanding(t.Context(), c)
	if e != nil || v.ApprovedProviders[0] != "pod" {
		t.Fatal(v, e)
	}
	key := uuid.NewString()
	if e = s.ChooseCurrentUnderstanding(t.Context(), q.ID, v.ID, q.Revision, 1, key); e != nil {
		t.Fatal(e)
	}
	if e = s.ChooseCurrentUnderstanding(t.Context(), q.ID, v.ID, q.Revision, 1, key); e != nil {
		t.Fatal(e)
	}
	if e = s.ChooseCurrentUnderstanding(t.Context(), q.ID, v.ID, q.Revision, 2, key); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	if e = s.ChooseCurrentUnderstanding(t.Context(), q.ID, v.ID, q.Revision, 2, "bad"); !errors.Is(e, ErrInvalidEditorialState) {
		t.Fatal(e)
	}
	if e = s.ChooseCurrentUnderstanding(t.Context(), q.ID, v.ID, q.Revision+1, 2, uuid.NewString()); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	if e = s.ChooseCurrentUnderstanding(t.Context(), q.ID, "missing", q.Revision, 2, uuid.NewString()); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	history, e := s.HistoryUnderstanding(t.Context(), q.ID, 0, 100)
	if e != nil || len(history) != 1 {
		t.Fatal(history, e)
	}
	history, e = s.HistoryUnderstanding(t.Context(), q.ID, 1, 1)
	if e != nil || len(history) != 0 {
		t.Fatal(history, e)
	}
}

func TestUnderstandingTransactionWriteFailuresAreAtomic(t *testing.T) {
	for _, table := range []string{"understanding_heads", "understanding_snapshots", "understanding_references"} {
		t.Run(table, func(t *testing.T) {
			s, _, _, note := knowledgeStoreFixture(t)
			q := createQuestion(t, s)
			if _, e := s.DB.Exec(`CREATE TRIGGER understanding_test_failure BEFORE INSERT ON ` + table + ` BEGIN SELECT RAISE(ABORT,'injected storage failure'); END`); e != nil {
				t.Fatal(e)
			}
			c := SaveUnderstandingCommand{QuestionID: q.ID, QuestionRevision: q.Revision, RequestKey: uuid.NewString(), Answer: "must not survive", References: []UnderstandingReference{{Kind: "note", ObjectID: note, Version: 1}}}
			if _, e := s.SaveUnderstanding(t.Context(), c); e == nil {
				t.Fatal("write failure accepted")
			}
			var count int
			if e := s.DB.QueryRow(`SELECT count(*) FROM understanding_snapshots WHERE question_id=?`, q.ID).Scan(&count); e != nil || count != 0 {
				t.Fatal(count, e)
			}
			h, e := s.UnderstandingHead(t.Context(), q.ID)
			if e != nil || h.Revision != 0 {
				t.Fatal(h, e)
			}
			if _, e = s.DB.Exec(`DROP TRIGGER understanding_test_failure`); e != nil {
				t.Fatal(e)
			}
			if _, e = s.SaveUnderstanding(t.Context(), c); e != nil {
				t.Fatal("same request must be retryable after rollback", e)
			}
		})
	}
	s, _, _, _ := knowledgeStoreFixture(t)
	q := createQuestion(t, s)
	v := saveUnderstandingFixture(t, s, q, "unselected", 0, nil)
	if _, e := s.DB.Exec(`CREATE TRIGGER understanding_test_choose_failure BEFORE INSERT ON understanding_choose_requests BEGIN SELECT RAISE(ABORT,'injected storage failure'); END`); e != nil {
		t.Fatal(e)
	}
	key := uuid.NewString()
	if e := s.ChooseCurrentUnderstanding(t.Context(), q.ID, v.ID, q.Revision, 1, key); e == nil {
		t.Fatal("choose write failure accepted")
	}
	h, e := s.UnderstandingHead(t.Context(), q.ID)
	if e != nil || h.Revision != 1 || h.CurrentSnapshotID != "" {
		t.Fatal(h, e)
	}
	if _, e = s.DB.Exec(`DROP TRIGGER understanding_test_choose_failure`); e != nil {
		t.Fatal(e)
	}
	if e = s.ChooseCurrentUnderstanding(t.Context(), q.ID, v.ID, q.Revision, 1, key); e != nil {
		t.Fatal(e)
	}
}

func TestUnderstandingMissingReferencesAndCancelledReads(t *testing.T) {
	s, _, ep, _ := knowledgeStoreFixture(t)
	q := createQuestion(t, s)
	for _, r := range []UnderstandingReference{{Kind: "note", ObjectID: "missing", Version: 1}, {Kind: "evidence", ObjectID: "missing", Version: 1}, {Kind: "article", ObjectID: "missing", Version: 1}, {Kind: "keypoint", ObjectID: "missing", Version: 1}, {Kind: "source", ObjectID: "episode:missing", Version: 1}, {Kind: "source", ObjectID: "document:missing", Version: 1}} {
		if _, e := s.ReadUnderstandingReference(t.Context(), r); !errors.Is(e, ErrNotFound) {
			t.Fatal(r, e)
		}
	}
	for _, r := range []UnderstandingReference{{Kind: "unknown", Version: 1}, {Kind: "source", ObjectID: "episode", Version: 1}, {Kind: "source", ObjectID: "episode:", Version: 1}, {Kind: "source", ObjectID: "unknown:id", Version: 1}, {Kind: "note", Version: 0}} {
		if _, e := s.ReadUnderstandingReference(t.Context(), r); !errors.Is(e, ErrInvalidEditorialState) {
			t.Fatal(r, e)
		}
	}
	if _, e := s.ReadCurrentSourceUnderstandingReference(t.Context(), "unknown", ep); !errors.Is(e, ErrInvalidEditorialState) {
		t.Fatal(e)
	}
	if _, e := s.ReadCurrentSourceUnderstandingReference(t.Context(), "episode", "missing"); e == nil {
		t.Fatal("missing source accepted")
	}
	if _, e := s.GetUnderstandingSnapshot(t.Context(), "missing"); !errors.Is(e, ErrNotFound) {
		t.Fatal(e)
	}
	if _, e := s.GetCurrentUnderstanding(t.Context(), "missing"); !errors.Is(e, ErrNotFound) {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, e := s.SaveUnderstanding(ctx, SaveUnderstandingCommand{QuestionID: q.ID, RequestKey: uuid.NewString(), Answer: "cancelled"}); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	if e := s.ChooseCurrentUnderstanding(ctx, q.ID, "missing", q.Revision, 0, uuid.NewString()); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	if _, e := s.GetUnderstandingSnapshot(ctx, "missing"); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	if _, e := s.HistoryUnderstanding(ctx, q.ID, 0, 1); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	if _, e := s.ReadUnderstandingReference(ctx, UnderstandingReference{Kind: "note", Version: 1}); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	if _, e := s.UnderstandingMaySend(ctx, "missing", 1, "pod"); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	if _, e := s.UnderstandingKnowledgeMaterial(ctx, "missing", "pod"); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
}

func TestUnderstandingReferenceVersionCannotFollowNewCurrent(t *testing.T) {
	s, _, ep, _ := knowledgeStoreFixture(t)
	old, e := s.ReadCurrentSourceUnderstandingReference(t.Context(), "episode", ep)
	if e != nil || old.Version != 1 {
		t.Fatal(old, e)
	}
	var job string
	if e = s.DB.QueryRow(`SELECT id FROM processing_jobs WHERE source_id=? LIMIT 1`, ep).Scan(&job); e != nil {
		t.Fatal(e)
	}
	version, e := s.CreateArtifactVersion(t.Context(), "episode", ep, KindTranscript, "test", "test", "2", job, `{"segments":[{"id":"new","start":0,"end":1,"text":"new current"}]}`)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.SetCurrentVersion(t.Context(), "episode", ep, KindTranscript, version); e != nil {
		t.Fatal(e)
	}
	exact, e := s.ReadUnderstandingReference(t.Context(), old)
	if e != nil || exact.Version != 1 || exact.ObjectID != old.ObjectID {
		t.Fatal(exact, e)
	}
	if _, e = s.DB.Exec(`UPDATE keypoint_index SET card_version=1 WHERE source_id=?`, ep); e != nil {
		t.Fatal(e)
	}
	var kp string
	var kv int
	if e = s.DB.QueryRow(`SELECT id,card_version FROM keypoint_index WHERE source_id=?`, ep).Scan(&kp, &kv); e != nil {
		t.Fatal(e)
	}
	ref := UnderstandingReference{Kind: "keypoint", ObjectID: kp, Version: kv}
	if _, e = s.ReadUnderstandingReference(t.Context(), ref); e != nil {
		t.Fatal(e)
	}
	ref.Version++
	if _, e = s.ReadUnderstandingReference(t.Context(), ref); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	var snapshotID, title string
	if e = s.DB.QueryRow(`SELECT id,title FROM source_snapshots WHERE source_type='episode' AND source_id=? AND content_version=1`, ep).Scan(&snapshotID, &title); e != nil {
		t.Fatal(e)
	}
	evidence, e := s.ReadUnderstandingReference(t.Context(), UnderstandingReference{Kind: "evidence", ObjectID: snapshotID, Version: 1})
	if e != nil || evidence.Body != title || evidence.SourceID != ep {
		t.Fatal(evidence, e)
	}
	if _, e = s.DB.Exec(`UPDATE source_snapshots SET status='purged' WHERE id=?`, snapshotID); e != nil {
		t.Fatal(e)
	}
	if _, e = s.ReadUnderstandingReference(t.Context(), UnderstandingReference{Kind: "evidence", ObjectID: snapshotID, Version: 1}); !errors.Is(e, ErrNotFound) {
		t.Fatal(e)
	}
}
