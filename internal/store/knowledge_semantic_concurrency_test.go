package store

import (
	"context"
	"errors"
	"fmt"
	"github.com/woyin/orangecast/internal/models"
	"reflect"
	"runtime"
	"testing"
	"time"
)

func matrixConcurrencyFixture(t *testing.T) (*Store, *KnowledgeEmbeddingConfig, embeddingEpoch, KnowledgeSearchQuery) {
	t.Helper()
	s, cfg, doc := embeddingIndexFixture(t)
	enableEmbeddingDoc(t, s, cfg, doc)
	for i := 0; i < 3; i++ {
		if _, err := s.CreateOwnerNote(t.Context(), models.OwnerNote{SourceType: "document", SourceID: doc.ID, Kind: "owner_reflection", Content: fmt.Sprintf("并发理解 %d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	windows, err := s.PrepareKnowledgeEmbeddingBatch(t.Context(), cfg.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.AdoptKnowledgeEmbeddings(t.Context(), cfg.ID, windows, fakeEmbeddingResult(cfg, windows)); err != nil {
		t.Fatal(err)
	}
	current, err := s.GetKnowledgeEmbeddingConfig(t.Context(), cfg.ID)
	if err != nil {
		t.Fatal(err)
	}
	epoch, err := s.embeddingEpoch(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return s, current, epoch, KnowledgeSearchQuery{Kind: "notes"}
}

func waitMatrixLoad(t *testing.T, s *Store, old *embeddingMatrixLoad) *embeddingMatrixLoad {
	t.Helper()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		s.embeddingMatrixMu.Lock()
		load := s.embeddingMatrixLoad
		s.embeddingMatrixMu.Unlock()
		if load != nil && load != old {
			return load
		}
		select {
		case <-deadline.C:
			t.Fatal("matrix load did not start")
		case <-tick.C:
		}
	}
}

type matrixAnswer struct {
	matrix *knowledgeEmbeddingMatrix
	err    error
}

func TestKnowledgeSemanticConcurrentMatrixSingleLoadAndWaiterCancellation(t *testing.T) {
	s, cfg, epoch, q := matrixConcurrencyFixture(t)
	conn, err := s.DB.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	answers := make(chan matrixAnswer, 8)
	go func() { m, e := s.embeddingMatrixFor(t.Context(), cfg, q, epoch); answers <- matrixAnswer{m, e} }()
	owner := waitMatrixLoad(t, s, nil)
	cancelled, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()
	if _, err = s.embeddingMatrixFor(cancelled, cfg, q, epoch); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	select {
	case <-owner.Done:
		t.Fatal("waiter cancelled owner")
	default:
	}
	started := make(chan struct{}, 7)
	for i := 0; i < 7; i++ {
		go func() {
			started <- struct{}{}
			m, e := s.embeddingMatrixFor(t.Context(), cfg, q, epoch)
			answers <- matrixAnswer{m, e}
		}()
	}
	for i := 0; i < 7; i++ {
		<-started
	}
	for i := 0; i < 16; i++ {
		runtime.Gosched()
	}
	if err = conn.Close(); err != nil {
		t.Fatal(err)
	}
	var first *knowledgeEmbeddingMatrix
	for i := 0; i < 8; i++ {
		select {
		case a := <-answers:
			if a.err != nil {
				t.Fatal(a.err)
			}
			if first == nil {
				first = a.matrix
			}
			if a.matrix != first {
				t.Fatal("duplicate matrix allocation")
			}
		case <-time.After(3 * time.Second):
			t.Fatal("matrix waiter stuck")
		}
	}
	if first.Objects != 3 || len(first.Entries) != 3 {
		t.Fatal(first.Objects, len(first.Entries))
	}
	s.embeddingMatrixMu.Lock()
	remaining := s.embeddingMatrixLoad
	s.embeddingMatrixMu.Unlock()
	if remaining != nil {
		t.Fatal("completed flight retained")
	}
}

func TestKnowledgeSemanticCancelledLoaderDoesNotPoisonWaiter(t *testing.T) {
	s, cfg, epoch, q := matrixConcurrencyFixture(t)
	conn, err := s.DB.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ownerCtx, cancel := context.WithCancel(t.Context())
	defer cancel()
	ownerResult := make(chan error, 1)
	go func() { _, e := s.embeddingMatrixFor(ownerCtx, cfg, q, epoch); ownerResult <- e }()
	owner := waitMatrixLoad(t, s, nil)
	waiterResult := make(chan matrixAnswer, 1)
	go func() { m, e := s.embeddingMatrixFor(t.Context(), cfg, q, epoch); waiterResult <- matrixAnswer{m, e} }()
	cancel()
	if err = <-ownerResult; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	waitMatrixLoad(t, s, owner)
	if err = conn.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case a := <-waiterResult:
		if a.err != nil || a.matrix.Objects != 3 {
			t.Fatal(a.err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("surviving waiter stuck")
	}
}

func TestKnowledgeSemanticLoadRejectsStaleEpochAndRetries(t *testing.T) {
	s, cfg, epoch, q := matrixConcurrencyFixture(t)
	if _, err := s.DB.Exec(`UPDATE knowledge_embedding_vectors SET content_hash='new-generation' WHERE config_id=?`, cfg.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.embeddingMatrixFor(t.Context(), cfg, q, epoch); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	s.embeddingMatrixMu.Lock()
	cached := s.embeddingMatrix
	flight := s.embeddingMatrixLoad
	s.embeddingMatrixMu.Unlock()
	if cached != nil || flight != nil {
		t.Fatal("stale load published or stuck")
	}
	fresh, err := s.embeddingEpoch(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.embeddingMatrixFor(t.Context(), cfg, q, fresh); err != nil {
		t.Fatal(err)
	}
}

func TestKnowledgeSemanticParallelScoresExactAndCancelGate(t *testing.T) {
	matrix := &knowledgeEmbeddingMatrix{}
	query := make([]float32, 1024)
	for j := range query {
		query[j] = float32(j%7-3) / 32
	}
	want := make([]float64, 1200)
	for i := range want {
		vector := make([]float32, len(query))
		for j := range vector {
			vector[j] = float32((i+j)%11-5) / 32
			want[i] += float64(vector[j]) * float64(query[j])
		}
		matrix.Documents = append(matrix.Documents, semanticDocumentRank{Key: fmt.Sprint(i), Revision: 1})
		matrix.Entries = append(matrix.Entries, embeddingMatrixEntry{Vector: vector, Document: i})
	}
	got, err := semanticWindowScores(t.Context(), matrix, query)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("parallel scores changed: %v", err)
	}
	// An occupied large-compute gate must not prevent an independent request cancelling.
	semanticComputeSlots <- struct{}{}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	_, err = semanticWindowScores(ctx, matrix, query)
	cancel()
	<-semanticComputeSlots
	if runtime.GOMAXPROCS(0) > 1 && !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	matrix.Entries[len(matrix.Entries)-1].Vector = []float32{1}
	if _, err = semanticWindowScores(t.Context(), matrix, query); !errors.Is(err, ErrInvalidEditorialState) {
		t.Fatal(err)
	}
}
