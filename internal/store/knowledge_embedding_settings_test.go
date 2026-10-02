package store

import (
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/models"
)

// This fixture exercises validation logic. It is not a real quality measurement.
func qualityGateFixture(t *testing.T, s *Store, id string) EmbeddingQualityReport {
	t.Helper()
	identity, err := s.KnowledgeEmbeddingQualityIdentity(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	return EmbeddingQualityReport{Identity: identity, QueryManifestSHA256: strings.Repeat("a", 64), CorpusManifestSHA256: strings.Repeat("b", 64), RelevanceManifestSHA256: strings.Repeat("c", 64), SourceRevision: "test-fixture-not-quality-proof", Machine: "test-fixture", Method: "test-only declared fields", Fusion: "rrf-k60-lexical-semantic-v1", RealMeasurements: true, Queries: 40, Samples: 20, PerformanceDimensions: 2048, Windows10k: 10000, Windows50k: 50000, FTSRecall10: .5, HybridRecall10: .6, RewriteFTSRecall10: .4, RewriteHybridRecall10: .6, Cold10kP95MS: 100, Cold50kP95MS: 400, Warm10kP95MS: 50, Warm50kP95MS: 100, EstimatedCostKnown: true, ActualCostKnown: true, PeakRSSBytes: 1024}
}

func TestEmbeddingSettingsCASAtomicScopePriceCapacity(t *testing.T) {
	s, cfg, doc := embeddingIndexFixture(t)
	ctx := t.Context()
	price := int64(123)
	cmd := EmbeddingSettingsCommand{ConfigID: cfg.ID, ExpectedRevision: 1, IndexAuthorized: true, WindowCapacity: 100, InputCentsPerMillion: &price, Sources: []EmbeddingSource{{"document", doc.ID}}}
	updated, err := s.UpdateKnowledgeEmbeddingSettings(ctx, cmd)
	if err != nil || updated.Revision != 2 || !updated.Enabled || updated.SemanticEnabled || updated.WindowCapacity != 100 {
		t.Fatal(updated, err)
	}
	p, err := s.GetModelPrice(ctx, cfg.Provider, cfg.Model)
	if err != nil || p.InputCentsPerMillion != 123 || p.OutputCentsPerMillion != 0 {
		t.Fatal(p, err)
	}
	price = 999
	cmd.WindowCapacity = 200
	if _, err = s.UpdateKnowledgeEmbeddingSettings(ctx, cmd); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	p, _ = s.GetModelPrice(ctx, cfg.Provider, cfg.Model)
	if p.InputCentsPerMillion != 123 {
		t.Fatal("stale CAS changed price", p)
	}
	cmd.ExpectedRevision = 2
	cmd.Sources = []EmbeddingSource{{"document", "missing"}}
	if _, err = s.UpdateKnowledgeEmbeddingSettings(ctx, cmd); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	updated, _ = s.GetKnowledgeEmbeddingConfig(ctx, cfg.ID)
	if updated.Revision != 2 || updated.WindowCapacity != 100 {
		t.Fatal("scope failure committed", updated)
	}
	cmd.Sources = []EmbeddingSource{{"document", doc.ID}, {"document", doc.ID}}
	if _, err = s.UpdateKnowledgeEmbeddingSettings(ctx, cmd); !errors.Is(err, ErrInvalidEditorialState) {
		t.Fatal(err)
	}
	cmd.Sources = []EmbeddingSource{{"document", doc.ID}}
	cmd.WindowCapacity = 50001
	if _, err = s.UpdateKnowledgeEmbeddingSettings(ctx, cmd); !errors.Is(err, ErrInvalidEditorialState) {
		t.Fatal(err)
	}
	cmd.WindowCapacity = 1
	cmd.InputCentsPerMillion = nil
	updated, err = s.UpdateKnowledgeEmbeddingSettings(ctx, cmd)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.GetModelPrice(ctx, cfg.Provider, cfg.Model); !errors.Is(err, ErrNotFound) {
		t.Fatal("unknown price turned free", err)
	}
}

func TestEmbeddingQualityGateFailClosedAndInvalidation(t *testing.T) {
	s, cfg, doc := embeddingIndexFixture(t)
	ctx := t.Context()
	enableEmbeddingDoc(t, s, cfg, doc)
	if err := s.SetKnowledgeSemanticEnabled(ctx, cfg.ID, 2, true); !errors.Is(err, ErrInvalidEditorialState) {
		t.Fatal("missing report enabled", err)
	}
	r := qualityGateFixture(t, s, cfg.ID)
	r.Cold10kP95MS = 611.7
	r.Passed = true
	saved, err := s.SaveKnowledgeEmbeddingQualityReport(ctx, cfg.ID, r)
	if err != nil || saved.Passed {
		t.Fatal("caller forced pass", saved, err)
	}
	if err = s.SetKnowledgeSemanticEnabled(ctx, cfg.ID, 2, true); !errors.Is(err, ErrInvalidEditorialState) {
		t.Fatal("failed cold report enabled", err)
	}
	r = qualityGateFixture(t, s, cfg.ID)
	saved, err = s.SaveKnowledgeEmbeddingQualityReport(ctx, cfg.ID, r)
	if err != nil || !saved.Passed {
		t.Fatal(saved, err)
	}
	if err = s.SetKnowledgeSemanticEnabled(ctx, cfg.ID, 1, true); !errors.Is(err, ErrConflict) {
		t.Fatal("stale activation accepted", err)
	}
	if err = s.SetKnowledgeSemanticEnabled(ctx, cfg.ID, 2, true); err != nil {
		t.Fatal(err)
	}
	_, reason, err := s.KnowledgeEmbeddingQualityGate(ctx, cfg.ID)
	if err != nil || reason != "" {
		t.Fatal(reason, err)
	}
	if err = s.SetModelPrice(ctx, models.ModelPrice{Provider: cfg.Provider, Model: cfg.Model, InputCentsPerMillion: 1}); err != nil {
		t.Fatal(err)
	}
	_, reason, err = s.KnowledgeEmbeddingQualityGate(ctx, cfg.ID)
	if err != nil || !strings.Contains(reason, "失效") {
		t.Fatal("price not bound", reason, err)
	}
	if err = s.SetKnowledgeSemanticEnabled(ctx, cfg.ID, 2, true); !errors.Is(err, ErrInvalidEditorialState) {
		t.Fatal("stale report enabled", err)
	}
	result, err := s.Retrieve(ctx, KnowledgeRetrieveQuery{Search: KnowledgeSearchQuery{Text: "主动回忆"}, Semantic: true, EmbeddingConfigID: cfg.ID})
	if err != nil || result.Method != "fts" || !strings.Contains(result.Degradation, "失效") {
		t.Fatal(result, err)
	}
	r = qualityGateFixture(t, s, cfg.ID)
	if _, err = s.SaveKnowledgeEmbeddingQualityReport(ctx, cfg.ID, r); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreatePastedDocument(ctx, "新来源", "新增范围外正文仍使清单身份失效"); err != nil {
		t.Fatal(err)
	}
	_, reason, err = s.KnowledgeEmbeddingQualityGate(ctx, cfg.ID)
	if err != nil || !strings.Contains(reason, "失效") {
		t.Fatal("corpus addition reused report", reason, err)
	}
	if _, err = s.SaveKnowledgeEmbeddingQualityReport(ctx, cfg.ID, r); !errors.Is(err, ErrConflict) {
		t.Fatal("old identity imported", err)
	}
}

func TestEmbeddingQualityReportRejectsIncompleteOrSynthetic(t *testing.T) {
	s, cfg, _ := embeddingIndexFixture(t)
	cases := []func(*EmbeddingQualityReport){func(r *EmbeddingQualityReport) { r.RealMeasurements = false }, func(r *EmbeddingQualityReport) { r.Samples = 19 }, func(r *EmbeddingQualityReport) { r.Queries = 39 }, func(r *EmbeddingQualityReport) { r.PerformanceDimensions = 192 }, func(r *EmbeddingQualityReport) { r.Windows50k = 1000 }, func(r *EmbeddingQualityReport) { r.FTSRecall10 = math.NaN() }, func(r *EmbeddingQualityReport) { r.Cold50kP95MS = 0 }, func(r *EmbeddingQualityReport) { r.PeakRSSBytes = 0 }, func(r *EmbeddingQualityReport) { r.QueryManifestSHA256 = "unknown" }, func(r *EmbeddingQualityReport) { r.ActualCents = -1 }, func(r *EmbeddingQualityReport) { r.Fusion = "different" }}
	for i, change := range cases {
		r := qualityGateFixture(t, s, cfg.ID)
		change(&r)
		if _, err := s.SaveKnowledgeEmbeddingQualityReport(t.Context(), cfg.ID, r); !errors.Is(err, ErrInvalidEditorialState) {
			t.Fatalf("case %d accepted %v", i, err)
		}
	}
	var n int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM knowledge_embedding_quality_reports`).Scan(&n); err != nil || n != 0 {
		t.Fatal(n, err)
	}
}

func TestEmbeddingQualityIdentityIncludesCoverageEventsAndUnderstandingChoices(t *testing.T) {
	s, cfg, doc := embeddingIndexFixture(t)
	ctx := t.Context()
	enableEmbeddingDoc(t, s, cfg, doc)
	before, err := s.KnowledgeEmbeddingQualityIdentity(ctx, cfg.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec(`UPDATE knowledge_embedding_events SET reason='window_too_large' WHERE config_id=?`, cfg.ID); err != nil {
		t.Fatal(err)
	}
	after, err := s.KnowledgeEmbeddingQualityIdentity(ctx, cfg.ID)
	if err != nil || before == after {
		t.Fatal("skip reason did not invalidate", before, after, err)
	}
	cmd := EmbeddingSettingsCommand{ConfigID: cfg.ID, ExpectedRevision: 2, IndexAuthorized: true, WindowCapacity: 100, Sources: []EmbeddingSource{{"document", doc.ID}}, UnderstandingSnapshotIDs: []string{"missing-understanding"}}
	if _, err = s.UpdateKnowledgeEmbeddingSettings(ctx, cmd); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	current, _ := s.GetKnowledgeEmbeddingConfig(ctx, cfg.ID)
	if current.Revision != 2 {
		t.Fatal("understanding selection failure committed scope", current)
	}
}

func TestEmbeddingSettingsWriteFailureRollsBackChosenScopePriceAndVectors(t *testing.T) {
	s, cfg, doc := embeddingIndexFixture(t)
	ctx := t.Context()
	enableEmbeddingDoc(t, s, cfg, doc)
	windows, err := s.PrepareKnowledgeEmbeddingBatch(ctx, cfg.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.AdoptKnowledgeEmbeddings(ctx, cfg.ID, windows, fakeEmbeddingResult(cfg, windows)); err != nil {
		t.Fatal(err)
	}
	if err = s.SetModelPrice(ctx, models.ModelPrice{Provider: cfg.Provider, Model: cfg.Model, InputCentsPerMillion: 7}); err != nil {
		t.Fatal(err)
	}
	before, err := s.KnowledgeEmbeddingQualityIdentity(ctx, cfg.ID)
	if err != nil {
		t.Fatal(err)
	}
	// A real SQLite write failure after the config CAS must not commit its earlier writes.
	if _, err = s.DB.Exec(`CREATE TRIGGER fail_embedding_price BEFORE INSERT ON model_prices BEGIN SELECT RAISE(ABORT,'price storage unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	price := int64(99)
	cmd := EmbeddingSettingsCommand{ConfigID: cfg.ID, ExpectedRevision: 2, IndexAuthorized: false, WindowCapacity: 1, InputCentsPerMillion: &price}
	if _, err = s.UpdateKnowledgeEmbeddingSettings(ctx, cmd); err == nil {
		t.Fatal("failed price write accepted")
	}
	after, err := s.KnowledgeEmbeddingQualityIdentity(ctx, cfg.ID)
	if err != nil || before != after {
		t.Fatal("failed write changed identity", before, after, err)
	}
	status, err := s.KnowledgeEmbeddingStatus(ctx, cfg.ID)
	if err != nil || !status.Config.Enabled || status.Config.Revision != 2 || status.IndexedWindows != len(windows) || len(status.Sources) != 1 {
		t.Fatal(status, err)
	}
	p, err := s.GetModelPrice(ctx, cfg.Provider, cfg.Model)
	if err != nil || p.InputCentsPerMillion != 7 {
		t.Fatal(p, err)
	}
	var jobs int
	s.DB.QueryRow(`SELECT COUNT(*) FROM processing_jobs WHERE job_type='knowledge_embedding'`).Scan(&jobs)
	if jobs != 0 {
		t.Fatal("failed edit created paid job", jobs)
	}
}
func TestEmbeddingSettingsScopePreviewRejectsPermissionWithoutChangingIt(t *testing.T) {
	s, cfg, doc := embeddingIndexFixture(t)
	ctx := t.Context()
	if err := s.SetSourceProductionPolicy(ctx, models.SourceDocument, doc.ID, "internal", models.ModelDataApprovedProvidersOnly); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSourceApprovedProviders(ctx, models.SourceDocument, doc.ID, []string{"pod"}); err != nil {
		t.Fatal(err)
	}
	refs := []EmbeddingSource{{"document", doc.ID}, {"document", "gone"}}
	preview, err := s.PreviewKnowledgeEmbeddingScope(ctx, cfg.ID, refs)
	if err != nil || len(preview) != 2 || preview[0].MaySend || preview[1].MaySend || !strings.Contains(preview[1].Reason, "不存在") {
		t.Fatal(preview, err)
	}
	if err = s.SetSourceApprovedProviders(ctx, models.SourceDocument, doc.ID, []string{cfg.Provider}); err != nil {
		t.Fatal(err)
	}
	preview, err = s.PreviewKnowledgeEmbeddingScope(ctx, cfg.ID, refs)
	if err != nil || !preview[0].MaySend {
		t.Fatal(preview, err)
	}
	// Malformed restored permission JSON is never treated as external permission.
	if _, err = s.DB.Exec(`UPDATE documents SET approved_providers_json='{bad}' WHERE id=?`, doc.ID); err != nil {
		t.Fatal(err)
	}
	preview, err = s.PreviewKnowledgeEmbeddingScope(ctx, cfg.ID, refs)
	if err != nil || preview[0].MaySend {
		t.Fatal(preview, err)
	}
	for _, bad := range [][]EmbeddingSource{{{"bad-type", "id"}}, {{"document", strings.Repeat("a", 201)}}, make([]EmbeddingSource, 1001)} {
		if _, err = s.PreviewKnowledgeEmbeddingScope(ctx, cfg.ID, bad); !errors.Is(err, ErrInvalidEditorialState) {
			t.Fatal(err)
		}
	}
	if _, err = s.PreviewKnowledgeEmbeddingScope(ctx, "missing", nil); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}
func TestEmbeddingSettingsUnderstandingSelectionIsCurrentExplicitAndVersionBound(t *testing.T) {
	s, cfg, _ := embeddingIndexFixture(t)
	ctx := t.Context()
	q := createQuestion(t, s)
	v, err := s.SaveUnderstanding(ctx, SaveUnderstandingCommand{QuestionID: q.ID, QuestionRevision: q.Revision, RequestKey: uuid.NewString(), Answer: "我尚无来源的个人解释", ModelDataPolicy: "external_allowed"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ChooseCurrentUnderstanding(ctx, q.ID, v.ID, q.Revision, 1, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	choices, err := s.ListEmbeddingUnderstandingChoices(ctx, cfg.ID)
	if err != nil || len(choices) != 1 || choices[0].Selected || !choices[0].MaySend {
		t.Fatal(choices, err)
	}
	cmd := EmbeddingSettingsCommand{ConfigID: cfg.ID, ExpectedRevision: 1, IndexAuthorized: true, WindowCapacity: 10, UnderstandingSnapshotIDs: []string{v.ID}}
	saved, err := s.UpdateKnowledgeEmbeddingSettings(ctx, cmd)
	if err != nil || saved.Revision != 2 {
		t.Fatal(saved, err)
	}
	choices, err = s.ListEmbeddingUnderstandingChoices(ctx, cfg.ID)
	if err != nil || !choices[0].Selected {
		t.Fatal(choices, err)
	}
	windows, err := s.PrepareKnowledgeEmbeddingBatch(ctx, cfg.ID)
	if err != nil || len(windows) != 1 || len(windows[0].Sources) != 0 {
		t.Fatal("source-less idea not explicitly indexable", windows, err)
	}
	before, _ := s.KnowledgeEmbeddingQualityIdentity(ctx, cfg.ID)
	if _, err = s.DB.Exec(`DELETE FROM knowledge_embedding_understandings WHERE config_id=?`, cfg.ID); err != nil {
		t.Fatal(err)
	}
	after, _ := s.KnowledgeEmbeddingQualityIdentity(ctx, cfg.ID)
	if before == after {
		t.Fatal("explicit understanding not in identity")
	}
	windows, err = s.PrepareKnowledgeEmbeddingBatch(ctx, cfg.ID)
	if err != nil || len(windows) != 0 {
		t.Fatal("unchosen understanding escaped scope", windows, err)
	}
}
func TestEmbeddingQualityCorruptReportAndClosedStorageFailClosed(t *testing.T) {
	s, cfg, doc := embeddingIndexFixture(t)
	ctx := t.Context()
	enableEmbeddingDoc(t, s, cfg, doc)
	report := qualityGateFixture(t, s, cfg.ID)
	saved, err := s.SaveKnowledgeEmbeddingQualityReport(ctx, cfg.ID, report)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetKnowledgeSemanticEnabled(ctx, cfg.ID, 2, true); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec(`UPDATE knowledge_embedding_quality_reports SET report_json='{invalid}' WHERE id=?`, saved.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.SetKnowledgeSemanticEnabled(ctx, cfg.ID, 2, true); !errors.Is(err, ErrInvalidEditorialState) {
		t.Fatal("corrupt report enabled", err)
	}
	if _, _, err = s.KnowledgeEmbeddingQualityGate(ctx, cfg.ID); err == nil {
		t.Fatal("corrupt report interpreted valid")
	}
	result, err := s.Retrieve(ctx, KnowledgeRetrieveQuery{Search: KnowledgeSearchQuery{Text: "主动回忆"}, Semantic: true, EmbeddingConfigID: cfg.ID})
	if err != nil || result.Method != "fts" || result.Degradation == "" {
		t.Fatal(result, err)
	}
	if err = s.SetKnowledgeSemanticEnabled(ctx, cfg.ID, 2, false); err != nil {
		t.Fatal("cannot explicitly disable with corrupt report", err)
	}
	if _, err = s.KnowledgeEmbeddingQualityIdentity(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if err = s.DB.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.KnowledgeEmbeddingQualityGate(ctx, cfg.ID); err == nil {
		t.Fatal("closed store authorized")
	}
	if err = s.SetKnowledgeSemanticEnabled(ctx, cfg.ID, 2, true); err == nil {
		t.Fatal("closed store enabled")
	}
	if _, err = s.ListEmbeddingUnderstandingChoices(ctx, cfg.ID); err == nil {
		t.Fatal("closed store returned choices")
	}
}

func TestEmbeddingQualityRestoredReportFlagsCannotOverrideMeasurements(t *testing.T) {
	s, cfg, doc := embeddingIndexFixture(t)
	ctx := t.Context()
	enableEmbeddingDoc(t, s, cfg, doc)
	original := qualityGateFixture(t, s, cfg.ID)
	saved, err := s.SaveKnowledgeEmbeddingQualityReport(ctx, cfg.ID, original)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name         string
		mutate       func(*EmbeddingQualityReport)
		storedPassed bool
	}{
		{"cold measure exceeds gate", func(r *EmbeddingQualityReport) { r.Cold50kP95MS = 999 }, true},
		{"payload declined", func(r *EmbeddingQualityReport) { r.Passed = false }, true},
		{"missing provenance", func(r *EmbeddingQualityReport) { r.SourceRevision = "" }, true},
		{"different identity", func(r *EmbeddingQualityReport) { r.Identity = strings.Repeat("f", 64) }, true},
		{"stored declined", func(r *EmbeddingQualityReport) {}, false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			r := *saved
			test.mutate(&r)
			payload, _ := json.Marshal(r)
			if _, e := s.DB.Exec(`UPDATE knowledge_embedding_quality_reports SET report_json=?,passed=? WHERE id=?`, string(payload), test.storedPassed, saved.ID); e != nil {
				t.Fatal(e)
			}
			if e := s.SetKnowledgeSemanticEnabled(ctx, cfg.ID, 2, true); !errors.Is(e, ErrInvalidEditorialState) {
				t.Fatal("restored report enabled", e)
			}
			_, reason, e := s.KnowledgeEmbeddingQualityGate(ctx, cfg.ID)
			if e != nil || reason == "" {
				t.Fatal("invalid artifact admitted", reason, e)
			}
			current, _ := s.GetKnowledgeEmbeddingConfig(ctx, cfg.ID)
			if current.SemanticEnabled {
				t.Fatal("invalid artifact changed switch")
			}
		})
	}
}
