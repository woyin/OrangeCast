package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
)

func confirmStudySource(t *testing.T, s *Store, questionID, sourceID, action string) {
	t.Helper()
	question, err := s.GetLearningQuestion(t.Context(), questionID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ChangeLearningQuestion(t.Context(), questionID, question.Revision, LearningQuestionChange{Action: action, Link: provider.LearningQuestionLink{Kind: "source", SourceType: "document", SourceID: sourceID}}); err != nil {
		t.Fatal(err)
	}
}
func TestQuestionStudyScopeConfirmedPermissionAndCurrentBodies(t *testing.T) {
	s, session, source := questionStudyFixture(t)
	confirmStudySource(t, s, session.QuestionID, source.SourceID, "suggest")
	if _, err := s.FreezeQuestionStudyScope(t.Context(), session.ID, "我的问题", "pod", nil); !errors.Is(err, ErrInvalidEditorialState) {
		t.Fatal("suggestion authorized scope", err)
	}
	confirmStudySource(t, s, session.QuestionID, source.SourceID, "link")
	note, err := s.CreateOwnerNote(t.Context(), models.OwnerNote{SourceType: "document", SourceID: source.SourceID, Kind: "owner_reflection", Content: "我的条件解释"})
	if err != nil {
		t.Fatal(err)
	}
	scope, err := s.FreezeQuestionStudyScope(t.Context(), session.ID, "我的问题", "pod", nil)
	if err != nil || len(scope.Materials) != 2 {
		t.Fatal(scope, err)
	}
	estimate, err := provider.EstimateQuestionStudy(*scope)
	if err != nil {
		t.Fatal(err)
	}
	for _, material := range scope.Materials {
		if material.Kind == "document" && (material.SnapshotID == "" || len(material.Segments) != 1) {
			t.Fatal("missing real evidence", material)
		}
	}
	if _, err = s.UpdateOwnerNote(t.Context(), note.ID, "我的新解释", note.CitationsJSON, note.ReferencesJSON, note.Revision); err != nil {
		t.Fatal(err)
	}
	next, err := s.FreezeQuestionStudyScope(t.Context(), session.ID, "我的问题", "pod", nil)
	if err != nil {
		t.Fatal(err)
	}
	newEstimate, err := provider.EstimateQuestionStudy(*next)
	if err != nil || newEstimate.InputFingerprint == estimate.InputFingerprint {
		t.Fatal("changed input retained old identity", newEstimate, err)
	}
	if _, err = s.FreezeQuestionStudyScope(t.Context(), session.ID, "我的问题", "pod", []string{"not-confirmed"}); !errors.Is(err, ErrConflict) {
		t.Fatal("arbitrary key widened scope", err)
	}
	if _, err = s.DB.Exec(`UPDATE documents SET model_data_policy='local_only' WHERE id=?`, source.SourceID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.FreezeQuestionStudyScope(t.Context(), session.ID, "我的问题", "pod", nil); !errors.Is(err, ErrInvalidEditorialState) {
		t.Fatal("private source admitted", err)
	}
}
func TestQuestionStudyScopeCapacityWholeMaterialAndHistoryPermission(t *testing.T) {
	s, session, source := questionStudyFixture(t)
	confirmStudySource(t, s, session.QuestionID, source.SourceID, "link")
	for i := 0; i < 9; i++ {
		doc, err := s.CreatePastedDocument(t.Context(), fmt.Sprintf("来源%d", i), "适用条件段落。")
		if err != nil {
			t.Fatal(err)
		}
		confirmStudySource(t, s, session.QuestionID, doc.ID, "link")
	}
	long, err := s.CreatePastedDocument(t.Context(), "超长表达", strings.Repeat("完整表达", 4000))
	if err != nil {
		t.Fatal(err)
	}
	confirmStudySource(t, s, session.QuestionID, long.ID, "link")
	scope, err := s.FreezeQuestionStudyScope(t.Context(), session.ID, "比较来源", "pod", nil)
	if err != nil || len(scope.Materials) != 8 {
		t.Fatal(scope, err)
	}
	if !strings.Contains(strings.Join(scope.Omissions, " "), "8个来源") {
		t.Fatal(scope.Omissions)
	}
	for _, material := range scope.Materials {
		if material.SourceID == long.ID {
			t.Fatal("long source silently sliced", material)
		}
	}
	historyDoc, err := s.CreatePastedDocument(t.Context(), "历史私有来源", "历史条件")
	if err != nil {
		t.Fatal(err)
	}
	turn, _, err := reserveStudyForTest(t, s, session, "b80485da-a339-42ca-b1b5-69d23ac57520", []QuestionStudySource{{"document", historyDoc.ID}})
	if err != nil {
		t.Fatal(err)
	}
	accepted, _ := json.Marshal(map[string]string{"body": "history-private-sentinel"})
	if _, err = s.DB.Exec(`UPDATE question_study_turns SET state='accepted',accepted_json=? WHERE id=?`, string(accepted), turn.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec(`UPDATE documents SET model_data_policy='local_only' WHERE id=?`, historyDoc.ID); err != nil {
		t.Fatal(err)
	}
	scope, err = s.FreezeQuestionStudyScope(t.Context(), session.ID, "比较来源", "pod", nil)
	if err != nil || len(scope.History) != 0 || !strings.Contains(strings.Join(scope.Omissions, " "), "历史回答") {
		t.Fatal(scope, err)
	}
	messages, err := provider.QuestionStudyMessages(*scope)
	if err != nil || strings.Contains(messages[1].Content, "history-private-sentinel") {
		t.Fatal(messages, err)
	}
}

func TestQuestionStudyScopePurgedEvidenceAndOversizeExplanation(t *testing.T) {
	s, session, source := questionStudyFixture(t)
	confirmStudySource(t, s, session.QuestionID, source.SourceID, "link")
	if _, err := s.CreateOwnerNote(t.Context(), models.OwnerNote{SourceType: "document", SourceID: source.SourceID, Kind: "owner_reflection", Content: "无引用的个人想法"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.FreezeSourceSnapshot(t.Context(), models.SourceDocument, source.SourceID); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkSourceSnapshotsPurged(t.Context(), models.SourceDocument, source.SourceID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.FreezeQuestionStudyScope(t.Context(), session.ID, "我的问题", "pod", nil); !errors.Is(err, ErrInvalidEditorialState) {
		t.Fatal("purged source authorized uncited Owner expression", err)
	}
	long, err := s.CreatePastedDocument(t.Context(), "完整表达", strings.Repeat("表达", 8000))
	if err != nil {
		t.Fatal(err)
	}
	confirmStudySource(t, s, session.QuestionID, long.ID, "link")
	result, err := s.Retrieve(t.Context(), KnowledgeRetrieveQuery{Search: KnowledgeSearchQuery{SourceType: "document", SourceID: long.ID}})
	if err != nil || len(result.Hits) != 1 {
		t.Fatal(result, err)
	}
	scope, err := s.FreezeQuestionStudyScope(t.Context(), session.ID, "我的问题", "pod", []string{result.Hits[0].Key})
	if !errors.Is(err, ErrInvalidEditorialState) || scope == nil || len(scope.Materials) != 0 || !strings.Contains(strings.Join(scope.Omissions, " "), "未截断") {
		t.Fatal(scope, err)
	}
}

func TestQuestionStudyScopeInvalidReferenceAndHistoryCapacity(t *testing.T) {
	s, session, source := questionStudyFixture(t)
	confirmStudySource(t, s, session.QuestionID, source.SourceID, "link")
	snapshot, err := s.FreezeSourceSnapshot(t.Context(), models.SourceDocument, source.SourceID)
	if err != nil {
		t.Fatal(err)
	}
	_, _, segments, err := s.SnapshotContent(t.Context(), snapshot.ID)
	if err != nil || len(segments) == 0 {
		t.Fatal(err)
	}
	citations, _ := json.Marshal([]string{segments[0].ID})
	note, err := s.CreateOwnerNote(t.Context(), models.OwnerNote{SourceType: "document", SourceID: source.SourceID, Kind: "source_note", Content: "来源观点", CitationsJSON: string(citations)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec(`UPDATE owner_notes SET citations_json='["not-a-real-segment"]' WHERE id=?`, note.ID); err != nil {
		t.Fatal(err)
	}
	scope, err := s.FreezeQuestionStudyScope(t.Context(), session.ID, "我的问题", "pod", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, material := range scope.Materials {
		if material.Key == "note:"+note.ID || material.Kind == "source_note" {
			t.Fatal("invalid evidence externalized", material)
		}
	}
	turn, _, err := reserveStudyForTest(t, s, session, "e5142b15-e044-4d94-b73f-6023dca8422d", []QuestionStudySource{source})
	if err != nil {
		t.Fatal(err)
	}
	accepted, _ := json.Marshal(map[string]string{"body": strings.Repeat("x", 17*1024)})
	if _, err = s.DB.Exec(`UPDATE question_study_turns SET state='accepted',accepted_json=? WHERE id=?`, string(accepted), turn.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.FreezeQuestionStudyScope(t.Context(), session.ID, "我的问题", "pod", nil); !errors.Is(err, ErrInvalidEditorialState) {
		t.Fatal("oversized accepted history silently truncated", err)
	}
}
