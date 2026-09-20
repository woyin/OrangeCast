package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
)

// TestJourneyR23KnowledgeToPublishedArticle keeps the production seams intact
// from two processed episodes through a reviewed article and history record.
func TestJourneyR23KnowledgeToPublishedArticle(t *testing.T) {
	srv := newTestServer(t)
	ctx := t.Context()
	session := claimOwnerAndLogin(t, srv, "journey-writing@example.com", "password123")
	if err := os.MkdirAll(srv.cfg.EvidenceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	rawAudio := filepath.Join(t.TempDir(), "writing.wav")
	if err := writeJourneyWAV(rawAudio); err != nil {
		t.Fatal(err)
	}

	analyzer := &journeyAnalyzer{}
	writer := &journeyArticleWriter{}
	claimReviewer := &journeyClaimReviewer{}
	styleReviewer := &journeyStyleReviewer{}
	bundle := &provider.ProviderBundle{
		Transcription: journeyTranscriber{}, Analysis: analyzer, Highlight: journeyHighlight{}, Narration: journeyNarration{},
		Curator: journeyCurator{}, Writer: writer, ClaimReviewer: claimReviewer, StyleEditor: styleReviewer,
	}
	srv.worker.WithBundleResolver(func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		return bundle, nil
	}).WithRawAudioResolver(func(context.Context, *models.ProcessingJob) (string, func(), error) {
		return rawAudio, func() {}, nil
	})
	srv.fetchFeed = func(feedURL string) (*models.Podcast, []models.Episode, error) {
		return &models.Podcast{FeedURL: feedURL, Title: "双集素材"}, []models.Episode{
			{GUID: "writing-1", Title: "证据如何沉淀", AudioURL: "https://media.example.com/one.mp3"},
			{GUID: "writing-2", Title: "观点如何成文", AudioURL: "https://media.example.com/two.mp3"},
		}, nil
	}
	if rec := postForm(t, srv, session, "/podcasts/new", "feed_url=https://feed.example.com/writing.xml"); rec.Code != http.StatusSeeOther {
		t.Fatalf("subscribe: %d %s", rec.Code, rec.Body.String())
	}
	podcasts, err := srv.store.ListPodcasts(ctx)
	if err != nil || len(podcasts) != 1 {
		t.Fatalf("podcast: %v %+v", err, podcasts)
	}
	episodes, err := srv.store.ListEpisodes(ctx, podcasts[0].ID)
	if err != nil || len(episodes) != 2 {
		t.Fatalf("episodes: %v %+v", err, episodes)
	}
	var keyPointIDs []string
	for _, episode := range episodes {
		if rec := postForm(t, srv, session, "/api/process", "source_type=episode&source_id="+episode.ID); rec.Code != http.StatusSeeOther {
			t.Fatalf("enqueue %s: %d %s", episode.ID, rec.Code, rec.Body.String())
		}
		drainJourneyJobs(t, srv, 10)
		card, err := srv.store.GetCurrentVersion(ctx, models.SourceEpisode, episode.ID, store.KindKnowledgeCard)
		if err != nil {
			t.Fatal(err)
		}
		rows, err := srv.store.ListKeyPointRowsByCardVersion(ctx, models.SourceEpisode, episode.ID, card.Version)
		if err != nil || len(rows) != 1 {
			t.Fatalf("keypoints for %s: %v %+v", episode.ID, err, rows)
		}
		kp, err := srv.store.GetKeyPoint(ctx, rows[0].ID)
		if err != nil || kp.QualityStatus != models.KeyPointReady {
			t.Fatalf("ready keypoint for %s: %v %+v", episode.ID, err, kp)
		}
		keyPointIDs = append(keyPointIDs, kp.ID)
	}

	noteText := "先保留可回溯证据，再把观点写成自己的文章。"
	if rec := postForm(t, srv, session, "/api/owner-notes", url.Values{
		"source_type": {"episode"}, "source_id": {episodes[0].ID}, "kind": {"owner_reflection"},
		"content": {noteText}, "citations_json": {"[]"}, "references_json": {`["seg-0001"]`},
	}.Encode()); rec.Code != http.StatusSeeOther {
		t.Fatalf("save note: %d %s", rec.Code, rec.Body.String())
	}
	notes, err := srv.store.ListOwnerNotes(ctx, models.SourceEpisode, episodes[0].ID)
	if err != nil || len(notes) != 1 {
		t.Fatalf("note: %v %+v", err, notes)
	}
	profile, err := srv.store.EnsureDefaultEditorialProfile(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if rec := postForm(t, srv, session, "/creation/selections", url.Values{
		"profile_id": {profile.ID}, "title": {"两集知识与个人理解"},
		"material_ids": {strings.Join(keyPointIDs, "\n")}, "note_ids": {notes[0].ID}, "confirm": {"1"},
	}.Encode()); rec.Code != http.StatusSeeOther {
		t.Fatalf("selection: %d %s", rec.Code, rec.Body.String())
	}
	selections, err := srv.store.ListCreationSelections(ctx, profile.ID)
	if err != nil || len(selections) != 1 || len(selections[0].MaterialIDs) != 2 || len(selections[0].NoteIDs) != 1 {
		t.Fatalf("selection snapshot: %v %+v", err, selections)
	}

	if rec := postForm(t, srv, session, "/workbench/ideation", url.Values{
		"profile_id": {profile.ID}, "intent": {"把两集学习结果写成可发布文章"},
		"constraints_json": {`{"form":"article"}`}, "selection_ids": {selections[0].ID},
	}.Encode()); rec.Code != http.StatusSeeOther {
		t.Fatalf("ideation session: %d %s", rec.Code, rec.Body.String())
	}
	sessions, err := srv.store.ListIdeationSessions(ctx, profile.ID)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("ideation sessions: %v %+v", err, sessions)
	}
	ideationID := sessions[0].ID
	for i, question := range []string{"两集材料共同支持什么？", "怎样加入我的判断并形成文章主张？"} {
		if rec := postForm(t, srv, session, "/workbench/ideation/round", url.Values{
			"session_id": {ideationID}, "input": {question}, "nonce": {fmt.Sprintf("journey-round-%d", i+1)},
		}.Encode()); rec.Code != http.StatusOK {
			t.Fatalf("ideation round %d: %d %s", i+1, rec.Code, rec.Body.String())
		}
		rounds, err := srv.store.ListIdeationRounds(ctx, ideationID)
		if err != nil || len(rounds) != i+1 {
			t.Fatalf("rounds %d: %v %+v", i+1, err, rounds)
		}
		current := rounds[len(rounds)-1]
		if rec := postForm(t, srv, session, "/workbench/ideation/diagnose", url.Values{
			"session_id": {ideationID}, "round_id": {current.ID},
		}.Encode()); rec.Code != http.StatusSeeOther {
			t.Fatalf("diagnose round %d: %d %s", i+1, rec.Code, rec.Body.String())
		}
		if err := srv.worker.ProcessOne(ctx); err != nil {
			t.Fatal(err)
		}
	}
	rounds, err := srv.store.ListIdeationRounds(ctx, ideationID)
	if err != nil || len(rounds) != 2 || rounds[1].OutputDiagnosisID == "" {
		t.Fatalf("multi-round diagnosis: %v %+v", err, rounds)
	}
	if rec := postForm(t, srv, session, "/workbench/ideation/promote", url.Values{
		"session_id": {ideationID}, "round_id": {rounds[1].ID}, "claim_index": {"0"},
	}.Encode()); rec.Code != http.StatusSeeOther {
		t.Fatalf("promote claim: %d %s", rec.Code, rec.Body.String())
	}
	proposals, err := srv.store.ListCreationProposals(ctx, profile.ID)
	if err != nil || len(proposals) != 1 {
		t.Fatalf("proposal: %v %+v", err, proposals)
	}
	ownerClaim := "只有把来源证据、跨集综合和个人判断分层，收听才能稳定转化为作品。"
	if rec := postForm(t, srv, session, "/workbench/creation-proposals/accept", url.Values{
		"creation_proposal_id": {proposals[0].ID}, "owner_claim": {ownerClaim},
	}.Encode()); rec.Code != http.StatusSeeOther {
		t.Fatalf("accept proposal: %d %s", rec.Code, rec.Body.String())
	}
	if err := srv.worker.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	briefs, err := srv.store.ListCreationBriefs(ctx, profile.ID)
	if err != nil || len(briefs) != 1 || briefs[0].CurrentVersion < 2 {
		t.Fatalf("curated brief: %v %+v", err, briefs)
	}
	brief := briefs[0]
	selectedJSON, _ := json.Marshal(keyPointIDs)
	if rec := postForm(t, srv, session, "/workbench/creation-briefs/edit", url.Values{
		"brief_id": {brief.ID}, "expected_version": {fmt.Sprint(brief.CurrentVersion)}, "owner_claim": {ownerClaim},
		"outline": {"一、来源证据\n二、跨集综合\n三、个人判断"}, "selected_material_ids": {string(selectedJSON)},
		"rejected_material_ids": {"[]"}, "style": {"清晰、克制"}, "target_length": {"1200"},
		"claim_type": {"synthesis"}, "unresolved_questions_json": {"[]"}, "notes": {"保留个人反思身份"},
	}.Encode()); rec.Code != http.StatusOK {
		t.Fatalf("edit brief: %d %s", rec.Code, rec.Body.String())
	}
	brief, err = srv.store.GetCreationBrief(ctx, brief.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rec := postForm(t, srv, session, "/workbench/creation-briefs/confirm", url.Values{
		"creation_brief_id": {brief.ID}, "expected_version": {fmt.Sprint(brief.CurrentVersion)},
	}.Encode()); rec.Code != http.StatusSeeOther {
		t.Fatalf("confirm brief: %d %s", rec.Code, rec.Body.String())
	}
	brief, err = srv.store.GetCreationBrief(ctx, brief.ID)
	if err != nil || brief.Status != "confirmed" {
		t.Fatalf("confirmed brief: %v %+v", err, brief)
	}
	if rec := postForm(t, srv, session, "/workbench/creation-briefs/write", url.Values{
		"creation_brief_id": {brief.ID}, "expected_version": {fmt.Sprint(brief.CurrentVersion)},
	}.Encode()); rec.Code != http.StatusSeeOther {
		t.Fatalf("enqueue writer: %d %s", rec.Code, rec.Body.String())
	}
	if err := srv.worker.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	drafts, err := srv.store.ListArticleDrafts(ctx, profile.ID)
	if err != nil || len(drafts) != 1 || drafts[0].CurrentRevisionID == nil {
		var failed []string
		rows, _ := srv.store.DB.QueryContext(ctx, `SELECT job_type || ':' || COALESCE(last_error,'') FROM processing_jobs WHERE status='failed'`)
		if rows != nil {
			defer rows.Close()
			for rows.Next() {
				var item string
				_ = rows.Scan(&item)
				failed = append(failed, item)
			}
		}
		t.Fatalf("article draft: %v %+v failed=%v", err, drafts, failed)
	}
	draft := drafts[0]
	baseRevision, err := srv.store.GetArticleRevision(ctx, *draft.CurrentRevisionID)
	if err != nil || !strings.Contains(baseRevision.Markdown, noteText) {
		t.Fatalf("initial article must use owner note: %v %+v", err, baseRevision)
	}

	for _, path := range []string{"/workbench/reviews/claims", "/workbench/reviews/style"} {
		if rec := postForm(t, srv, session, path, "revision_id="+baseRevision.ID); rec.Code != http.StatusSeeOther {
			t.Fatalf("enqueue first review %s: %d %s", path, rec.Code, rec.Body.String())
		}
	}
	drainJourneyJobs(t, srv, 3)
	readiness, err := srv.store.EvaluateArticlePublicationReadiness(ctx, baseRevision.ID)
	if err != nil || readiness.Ready {
		t.Fatalf("failed reviews must block publication: %v %+v", err, readiness)
	}
	if rec := postForm(t, srv, session, "/workbench/revise", "revision_id="+baseRevision.ID); rec.Code != http.StatusSeeOther {
		t.Fatalf("enqueue revision: %d %s", rec.Code, rec.Body.String())
	}
	if err := srv.worker.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	revisions, err := srv.store.ListArticleRevisions(ctx, draft.ID)
	if err != nil || len(revisions) != 2 || revisions[0].ID == baseRevision.ID || writer.revisionCalls != 1 || len(writer.lastRevision.RevisionFeedback) < 2 {
		t.Fatalf("AI revision: %v revisions=%+v writer=%+v", err, revisions, writer)
	}
	current := revisions[0]
	for _, path := range []string{"/workbench/reviews/claims", "/workbench/reviews/style"} {
		if rec := postForm(t, srv, session, path, "revision_id="+current.ID); rec.Code != http.StatusSeeOther {
			t.Fatalf("enqueue re-review %s: %d %s", path, rec.Code, rec.Body.String())
		}
	}
	drainJourneyJobs(t, srv, 3)
	readiness, err = srv.store.EvaluateArticlePublicationReadiness(ctx, current.ID)
	if err != nil || !readiness.Ready {
		t.Fatalf("re-reviewed article must be ready: %v %+v", err, readiness)
	}
	export := doWithCookie(srv, session, http.MethodGet, "/workbench/revisions/"+current.ID+"/package?format=markdown")
	if export.Code != http.StatusOK || !strings.Contains(export.Body.String(), "## 来源") || !strings.Contains(export.Body.String(), "综合两期素材") {
		t.Fatalf("article export: %d %s", export.Code, export.Body.String())
	}
	if rec := postForm(t, srv, session, "/workbench/record-article-history", "revision_id="+current.ID+"&status=unpublished"); rec.Code != http.StatusSeeOther {
		t.Fatalf("record history: %d %s", rec.Code, rec.Body.String())
	}
	history, err := srv.store.GetArticleHistoryForRevision(ctx, current.ID)
	if err != nil || history.Status != "unpublished" || history.ArticleDraftID != draft.ID || history.ArticleVersion != current.Version {
		t.Fatalf("article history: %v %+v", err, history)
	}
}

// TestJourneyR23LearningToDigest drives the learning path through the real
// router, durable queue and worker. Only model and media boundaries are fake.
func TestJourneyR23LearningToDigest(t *testing.T) {
	srv := newTestServer(t)
	ctx := t.Context()
	session := claimOwnerAndLogin(t, srv, "journey-learning@example.com", "password123")

	if err := os.MkdirAll(srv.cfg.EvidenceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	rawAudio := filepath.Join(t.TempDir(), "episode.wav")
	if err := writeJourneyWAV(rawAudio); err != nil {
		t.Fatal(err)
	}

	srv.fetchFeed = func(feedURL string) (*models.Podcast, []models.Episode, error) {
		return &models.Podcast{FeedURL: feedURL, Title: "学习与创作"}, []models.Episode{{
			GUID: "journey-episode", Title: "如何把收听沉淀成作品", AudioURL: "https://media.example.com/episode.mp3",
		}}, nil
	}
	if rec := postForm(t, srv, session, "/podcasts/new", "feed_url=https://feed.example.com/journey.xml"); rec.Code != http.StatusSeeOther {
		t.Fatalf("subscribe: %d %s", rec.Code, rec.Body.String())
	}
	podcasts, err := srv.store.ListPodcasts(ctx)
	if err != nil || len(podcasts) != 1 {
		t.Fatalf("podcast: %v %+v", err, podcasts)
	}
	episodes, err := srv.store.ListEpisodes(ctx, podcasts[0].ID)
	if err != nil || len(episodes) != 1 {
		t.Fatalf("episode: %v %+v", err, episodes)
	}
	episodeID := episodes[0].ID

	bundle := journeyLearningBundle()
	srv.worker.WithBundleResolver(func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		return bundle, nil
	}).WithRawAudioResolver(func(context.Context, *models.ProcessingJob) (string, func(), error) {
		return rawAudio, func() {}, nil
	})

	if rec := postForm(t, srv, session, "/api/process", "source_type=episode&source_id="+episodeID); rec.Code != http.StatusSeeOther {
		t.Fatalf("enqueue processing: %d %s", rec.Code, rec.Body.String())
	}
	drainJourneyJobs(t, srv, 10)

	card, err := srv.store.GetCurrentVersion(ctx, models.SourceEpisode, episodeID, store.KindKnowledgeCard)
	if err != nil {
		t.Fatalf("knowledge card: %v", err)
	}
	kps, err := srv.store.ListKeyPointRowsByCardVersion(ctx, models.SourceEpisode, episodeID, card.Version)
	if err != nil || len(kps) != 1 {
		t.Fatalf("ready keypoint: %v %+v", err, kps)
	}
	readyKP, err := srv.store.GetKeyPoint(ctx, kps[0].ID)
	if err != nil || readyKP.QualityStatus != models.KeyPointReady {
		t.Fatalf("ready keypoint status: %v %+v", err, readyKP)
	}
	plan, err := srv.store.GetLatestDJPlanForSource(ctx, models.SourceEpisode, episodeID)
	if err != nil || len(plan.Items) == 0 {
		t.Fatalf("DJ plan: %v %+v", err, plan)
	}
	narrations, err := srv.store.ListCurrentNarrationsForSource(ctx, models.SourceEpisode, episodeID)
	if err != nil || len(narrations) == 0 {
		t.Fatalf("narration: %v %+v", err, narrations)
	}

	csrf := journeyCSRF(t, srv, session)
	progressBody, _ := json.Marshal(map[string]any{
		"source_type": "episode", "source_id": episodeID, "plan_id": plan.ID,
		"plan_version": plan.Version, "item_position": 1, "highlight_id": "hl-journey",
		"item_offset_seconds": 2.5, "speed": 1.25, "seq": 1,
	})
	progressReq := httptest.NewRequest(http.MethodPost, "/api/listening-progress", strings.NewReader(string(progressBody)))
	progressReq.Header.Set("Content-Type", "application/json")
	progressReq.Header.Set("X-CSRF-Token", csrf)
	progressReq.AddCookie(session)
	progressReq.AddCookie(&http.Cookie{Name: "cwp_csrf", Value: csrf})
	progressRec := httptest.NewRecorder()
	srv.Router().ServeHTTP(progressRec, progressReq)
	if progressRec.Code != http.StatusOK {
		t.Fatalf("save progress: %d %s", progressRec.Code, progressRec.Body.String())
	}
	progress, err := srv.store.GetListeningProgress(ctx, models.SourceEpisode, episodeID)
	if err != nil || progress.PlanID != plan.ID || progress.Seq != 1 {
		t.Fatalf("progress: %v %+v", err, progress)
	}

	noteText := "我的理解：先保留可回溯证据，再把观点写成自己的文章。"
	if rec := postForm(t, srv, session, "/api/owner-notes", url.Values{
		"source_type": {"episode"}, "source_id": {episodeID}, "kind": {"owner_reflection"},
		"content": {noteText}, "citations_json": {"[]"}, "references_json": {`["seg-0001"]`},
	}.Encode()); rec.Code != http.StatusSeeOther {
		t.Fatalf("save owner note: %d %s", rec.Code, rec.Body.String())
	}
	notes, err := srv.store.ListOwnerNotes(ctx, models.SourceEpisode, episodeID)
	if err != nil || len(notes) != 1 {
		t.Fatalf("owner note: %v %+v", err, notes)
	}

	if rec := postForm(t, srv, session, "/api/digest", "source_type=episode&source_id="+episodeID); rec.Code != http.StatusSeeOther {
		t.Fatalf("enqueue digest: %d %s", rec.Code, rec.Body.String())
	}
	digestJob := pendingJourneyJob(t, srv, models.JobDigest)
	if _, err := srv.store.MarkJobRunning(ctx, digestJob.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.store.DB.ExecContext(ctx, `UPDATE processing_jobs SET lease_until=datetime('now','-10 minutes') WHERE id=?`, digestJob.ID); err != nil {
		t.Fatal(err)
	}
	if err := srv.store.ResetRunningOnStartup(ctx); err != nil {
		t.Fatal(err)
	}
	if err := srv.worker.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}

	digest, err := srv.store.GetCurrentEpisodeDigest(ctx, models.SourceEpisode, episodeID)
	if err != nil {
		t.Fatalf("digest: %v", err)
	}
	blocks, err := srv.store.ListDigestBlocks(ctx, digest.ID)
	if err != nil {
		t.Fatal(err)
	}
	var editable *models.DigestBlock
	foundNote := false
	for _, block := range blocks {
		if block.Type == models.DigestBlockParaphrase {
			editable = block
		}
		if block.Type == models.DigestBlockNote && block.NoteID == notes[0].ID && block.Text == noteText {
			foundNote = true
		}
	}
	if editable == nil || !foundNote {
		t.Fatalf("digest must preserve cited prose and owner note: %+v", blocks)
	}

	if rec := postForm(t, srv, session, "/digest/"+digest.ID+"/edit", url.Values{
		"base_version": {"1"}, "block_id": {editable.ID}, "title": {"收听之后的知识作品"},
		"text": {"节目强调，学习成果应保留证据来源，并经过个人判断。"},
	}.Encode()); rec.Code != http.StatusSeeOther {
		t.Fatalf("edit digest: %d %s", rec.Code, rec.Body.String())
	}
	revised, err := srv.store.GetCurrentEpisodeDigest(ctx, models.SourceEpisode, episodeID)
	if err != nil || revised.Version != 2 || revised.ParentDigestID != digest.ID {
		t.Fatalf("digest revision: %v %+v", err, revised)
	}
	export := doWithCookie(srv, session, http.MethodGet, "/digest/"+revised.ID+"/markdown")
	if export.Code != http.StatusOK || !strings.Contains(export.Body.String(), "收听之后的知识作品") || !strings.Contains(export.Body.String(), noteText) {
		t.Fatalf("digest export: %d %s", export.Code, export.Body.String())
	}

	if rec := postForm(t, srv, session, "/api/purge", "source_type=episode&source_id="+episodeID); rec.Code != http.StatusSeeOther {
		t.Fatalf("purge: %d %s", rec.Code, rec.Body.String())
	}
	if _, err := srv.store.GetEpisodeByID(ctx, episodeID); err != store.ErrNotFound {
		t.Fatalf("purged episode must be gone: %v", err)
	}
	if rec := doWithCookie(srv, session, http.MethodGet, "/digest/"+revised.ID+"/markdown"); rec.Code != http.StatusNotFound {
		t.Fatalf("purged digest export must be invalidated: %d", rec.Code)
	}
}

type journeyTranscriber struct{}

func (journeyTranscriber) Transcribe(string) (*provider.TranscriptResult, error) {
	return &provider.TranscriptResult{
		Language: "zh", Text: "学习成果需要保留证据来源，并经过个人判断。",
		Segments: []provider.Segment{{ID: "seg-0001", Start: 0, End: 8, Text: "学习成果需要保留证据来源，并经过个人判断。"}},
		Model:    "journey-transcriber",
	}, nil
}
func (journeyTranscriber) Name() string { return "journey" }

type journeyAnalyzer struct{}

func (journeyAnalyzer) Analyze(string, []provider.Segment) (*provider.AnalyzeResult, error) {
	return &provider.AnalyzeResult{Card: &provider.KnowledgeCard{
		Title: "收听到创作", Summary: provider.CitedText{Text: "把来源证据和个人判断分开沉淀。", Citations: []string{"seg-0001"}},
		KeyPoints: []provider.KeyPoint{{Content: "学习成果要同时保留来源证据和个人判断", Description: "为后续创作留下可审校材料", Citations: []string{"seg-0001"}}},
		Chapters:  []provider.Chapter{{Title: "沉淀", Gist: "形成可复用材料", Citations: []string{"seg-0001"}}},
		Quotes:    []provider.Quote{{Text: "学习成果需要保留证据来源，并经过个人判断。", Citations: []string{"seg-0001"}}},
	}}, nil
}
func (journeyAnalyzer) AssessKeypointQuality(context.Context, provider.KeypointQualityInput) (*provider.KeypointQualityVerdict, provider.TaskUsage, error) {
	return &provider.KeypointQualityVerdict{Decision: models.KPQualityReady, Reasons: []string{"引用原文直接支持"}}, provider.TaskUsage{}, nil
}
func (journeyAnalyzer) DiagnoseIdeation(_ context.Context, req provider.IdeationDiagnosisRequest) (*provider.IdeationDiagnosis, provider.TaskUsage, error) {
	if len(req.Materials) < 2 {
		return &provider.IdeationDiagnosis{Gaps: []string{"需要至少两份来源材料"}}, provider.TaskUsage{}, nil
	}
	ids := []string{req.Materials[0].ID, req.Materials[1].ID}
	return &provider.IdeationDiagnosis{
		Supports: []provider.DiagnosisItem{
			{MaterialID: ids[0], Text: "第一集支持保留来源证据。"},
			{MaterialID: ids[1], Text: "第二集支持把观点组织成文。"},
		},
		Supplements: []provider.DiagnosisItem{{MaterialID: ids[1], Text: "跨集材料可以形成互补论证。"}},
		ProposedClaims: []provider.ProposedClaimItem{{
			Claim: "分层保存来源证据、跨集综合与个人判断，才能把收听稳定转化为作品。", MaterialIDs: ids,
		}},
	}, provider.TaskUsage{}, nil
}
func (journeyAnalyzer) Name() string { return "journey" }

type journeyCurator struct{}

func (journeyCurator) Curate(_ context.Context, req provider.CuratorRequest) (*provider.CuratorResult, error) {
	selected := make([]string, 0, len(req.Materials))
	for _, material := range req.Materials {
		selected = append(selected, material.KeyPointID)
	}
	target := 1200
	return &provider.CuratorResult{
		Thesis: req.Thesis, ClaimType: "synthesis", Audience: req.Audience,
		Outline: "一、来源证据\n二、跨集综合\n三、个人判断", SelectedKeyPointIDs: selected,
		Style: "清晰、克制", TargetLength: &target, Notes: "明确区分来源与个人判断",
	}, nil
}
func (journeyCurator) Name() string { return "journey" }

type journeyArticleWriter struct {
	revisionCalls int
	lastRevision  provider.ClaimAwareWritingRequest
}

func (w *journeyArticleWriter) WriteArticle(context.Context, provider.ArticleWritingRequest) (*provider.ArticleWritingResult, error) {
	return nil, fmt.Errorf("journey must use claim-aware writer")
}

func (w *journeyArticleWriter) WriteArticleWithClaims(_ context.Context, req provider.ClaimAwareWritingRequest) (*provider.ClaimAwareWritingResult, provider.TaskUsage, error) {
	return journeyInitialArticle(req), provider.TaskUsage{}, nil
}

func (w *journeyArticleWriter) WriteArticleRevisionWithClaims(_ context.Context, req provider.ClaimAwareWritingRequest) (*provider.ClaimAwareWritingResult, provider.TaskUsage, error) {
	w.revisionCalls++
	w.lastRevision = req
	markdown := req.ExistingMarkdown
	claimMap := make([]provider.ClaimMapEntry, 0, len(req.ExistingClaimMap))
	for _, entry := range req.ExistingClaimMap {
		if entry.ClaimKind == provider.ClaimOwner {
			markdown = strings.Replace(markdown, entry.Excerpt, "", 1)
			continue
		}
		claimMap = append(claimMap, entry)
	}
	if len(req.OwnerNotes) > 0 {
		owner := "经过审校后，我的判断是：" + req.OwnerNotes[0].Content
		markdown += "\n\n" + owner
		claimMap = append(claimMap, provider.ClaimMapEntry{Excerpt: owner, ClaimKind: provider.ClaimOwner, MaterialIDs: []string{req.OwnerNotes[0].ID}})
	}
	return &provider.ClaimAwareWritingResult{Title: "从收听到作品（修订版）", Markdown: markdown, ClaimMap: claimMap}, provider.TaskUsage{}, nil
}

func (*journeyArticleWriter) Name() string { return "journey" }

func journeyInitialArticle(req provider.ClaimAwareWritingRequest) *provider.ClaimAwareWritingResult {
	sourceExcerpt := "两期节目都强调，学习成果需要保留来源证据。"
	synthesisExcerpt := "综合两期素材，我认为收听记录只有进入可审校的写作流程，才会成为长期资产。"
	markdown := "# 从收听到作品\n\n" + sourceExcerpt + "\n\n" + synthesisExcerpt
	claimMap := []provider.ClaimMapEntry{
		{Excerpt: sourceExcerpt, ClaimKind: provider.ClaimSource, MaterialIDs: []string{req.Materials[0].KeyPointID}, SourceTitle: req.Materials[0].SourceTitle, CitationRefs: req.Materials[0].Citations},
		{Excerpt: synthesisExcerpt, ClaimKind: provider.ClaimSynthesis, MaterialIDs: []string{req.Materials[0].KeyPointID, req.Materials[1].KeyPointID}},
	}
	if len(req.OwnerNotes) > 0 {
		owner := "我的笔记提醒我：" + req.OwnerNotes[0].Content
		markdown += "\n\n" + owner
		claimMap = append(claimMap, provider.ClaimMapEntry{Excerpt: owner, ClaimKind: provider.ClaimOwner, MaterialIDs: []string{req.OwnerNotes[0].ID}})
	}
	return &provider.ClaimAwareWritingResult{Title: "从收听到作品", Markdown: markdown, ClaimMap: claimMap}
}

type journeyClaimReviewer struct{ calls int }

func (r *journeyClaimReviewer) ReviewClaims(_ context.Context, req provider.ClaimReviewRequest) (*provider.ClaimReviewResult, provider.TaskUsage, error) {
	r.calls++
	if r.calls == 1 {
		excerpt := "我的笔记提醒我：先保留可回溯证据，再把观点写成自己的文章。"
		return &provider.ClaimReviewResult{Status: provider.ClaimReviewFailed, Findings: []provider.ClaimReviewFinding{{
			Excerpt: excerpt, IssueKind: "outside_brief", Detail: "应更明确说明这是 Owner 经审校后承担的判断",
		}}}, provider.TaskUsage{}, nil
	}
	return &provider.ClaimReviewResult{Status: provider.ClaimReviewPassed}, provider.TaskUsage{}, nil
}
func (*journeyClaimReviewer) Name() string { return "journey" }

type journeyStyleReviewer struct{ calls int }

func (r *journeyStyleReviewer) ReviewStyle(context.Context, provider.StyleReviewRequest) (*provider.StyleReviewResult, error) {
	r.calls++
	if r.calls == 1 {
		return &provider.StyleReviewResult{Status: "failed", Issues: []string{"个人判断的措辞需要更清晰"}}, nil
	}
	return &provider.StyleReviewResult{Status: "passed"}, nil
}
func (*journeyStyleReviewer) Name() string { return "journey" }

type journeyHighlight struct{}

func (journeyHighlight) GenerateHighlights([]provider.Segment) (*provider.HighlightSet, error) {
	return &provider.HighlightSet{Highlights: []provider.Highlight{{ID: "hl-journey", Gist: "先听来源，再形成自己的判断。", Citations: []string{"seg-0001"}}}}, nil
}
func (journeyHighlight) Name() string { return "journey" }

type journeyNarration struct{}

func (journeyNarration) Synthesize(text, voice, outPath string) (*provider.NarrationResult, error) {
	if err := writeJourneyWAV(outPath); err != nil {
		return nil, err
	}
	return &provider.NarrationResult{AudioPath: outPath, DurationSeconds: 0.05, CharCount: len([]rune(text)), Voice: "test", Model: "journey-tts"}, nil
}
func (journeyNarration) Available() bool { return true }
func (journeyNarration) Name() string    { return "journey" }

type journeyDigestWriter struct{}

func (journeyDigestWriter) ComposeDigest(_ context.Context, req provider.DigestWritingRequest) (*provider.DigestWritingResult, error) {
	blocks := []provider.DigestBlockDraft{
		{Type: provider.DigestBlockParaphraseStr, Text: "节目强调，学习成果需要保留证据来源。", Citations: []string{"seg-0001"}},
		{Type: provider.DigestBlockAIExpansionStr, Text: "AI 展开：可以把这套方法看作一条从收听到创作的路径。"},
	}
	for _, note := range req.Notes {
		blocks = append(blocks, provider.DigestBlockDraft{Type: provider.DigestBlockNoteStr, Text: note.Text, NoteID: note.NoteID})
	}
	return &provider.DigestWritingResult{Title: "从收听到知识作品", Blocks: blocks}, nil
}
func (journeyDigestWriter) WeaveDigestFacts(context.Context, provider.DigestWeaveRequest) (*provider.DigestWeaveResult, error) {
	return &provider.DigestWeaveResult{}, nil
}

func journeyLearningBundle() *provider.ProviderBundle {
	return &provider.ProviderBundle{
		Transcription: journeyTranscriber{}, Analysis: journeyAnalyzer{}, Highlight: journeyHighlight{},
		Narration: journeyNarration{}, DigestWriter: journeyDigestWriter{},
	}
}

func journeyCSRF(t *testing.T, srv *Server, session *http.Cookie) string {
	t.Helper()
	rec := doWithCookie(srv, session, http.MethodGet, "/dashboard")
	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == "cwp_csrf" {
			return cookie.Value
		}
	}
	t.Fatal("missing CSRF cookie")
	return ""
}

func pendingJourneyJob(t *testing.T, srv *Server, kind models.JobType) *models.ProcessingJob {
	t.Helper()
	jobs, err := srv.store.ListQueuedOrRunning(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, job := range jobs {
		if job.JobType == kind {
			return job
		}
	}
	t.Fatalf("missing pending %s job: %+v", kind, jobs)
	return nil
}

func drainJourneyJobs(t *testing.T, srv *Server, max int) {
	t.Helper()
	for i := 0; i < max; i++ {
		jobs, err := srv.store.ListQueuedOrRunning(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if len(jobs) == 0 {
			return
		}
		if err := srv.worker.ProcessOne(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	jobs, _ := srv.store.ListQueuedOrRunning(t.Context())
	t.Fatalf("journey jobs did not drain: %+v", jobs)
}

func writeJourneyWAV(path string) error {
	dataSize := uint32(800)
	riffSize := uint32(36) + dataSize
	buf := make([]byte, 0, 44+int(dataSize))
	buf = append(buf, "RIFF"...)
	buf = append(buf, byte(riffSize), byte(riffSize>>8), byte(riffSize>>16), byte(riffSize>>24))
	buf = append(buf, "WAVEfmt "...)
	buf = append(buf, 16, 0, 0, 0, 1, 0, 1, 0)
	buf = append(buf, 0x40, 0x1f, 0, 0, 0x80, 0x3e, 0, 0, 2, 0, 16, 0)
	buf = append(buf, "data"...)
	buf = append(buf, byte(dataSize), byte(dataSize>>8), byte(dataSize>>16), byte(dataSize>>24))
	buf = append(buf, make([]byte, dataSize)...)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, buf, 0o644)
}
