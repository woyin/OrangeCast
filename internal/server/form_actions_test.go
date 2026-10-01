package server

import (
	"os/exec"
	"path/filepath"
	"testing"
)

func TestCommonFormActions(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node unavailable")
	}
	if out, err := exec.Command(node, filepath.Join("testdata", "form-actions-common.cjs")).CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
}

func TestLearningReviewCommonForms(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node unavailable")
	}
	if out, err := exec.Command(node, filepath.Join("testdata", "review-forms.cjs")).CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
}

func TestKnowledgeFormNamedActionDoesNotReplaceEndpoint(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node unavailable")
	}
	cmd := exec.Command(node, filepath.Join("testdata", "form-actions.cjs"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(out))
	}
}

func TestQuestionFormNamedActionDraftIsolationAndConflict(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node unavailable")
	}
	if out, err := exec.Command(node, filepath.Join("testdata", "question-forms.cjs")).CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
}

func TestAutomationFormsPreserveReasonAndOnlyPollVisibleStatus(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node unavailable")
	}
	if out, e := exec.Command(node, filepath.Join("testdata", "automation-forms.cjs")).CombinedOutput(); e != nil {
		t.Fatal(e, string(out))
	}
}

func TestKnowledgeGenerationPreservesListeningNavigationAndScope(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node unavailable")
	}
	if out, e := exec.Command(node, filepath.Join("testdata", "knowledge-generation.cjs")).CombinedOutput(); e != nil {
		t.Fatal(e, string(out))
	}
}

func TestVoiceRecoveredDraftOpensEditorWithoutPaidRequest(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node unavailable")
	}
	if out, e := exec.Command(node, filepath.Join("testdata", "voice-recovery.cjs")).CombinedOutput(); e != nil {
		t.Fatal(e, string(out))
	}
}

func TestListeningReflectionRootEditor(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node unavailable")
	}
	if out, err := exec.Command(node, filepath.Join("testdata", "listening-reflections.cjs")).CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
}

func TestKnowledgeQueryEmbeddingController(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node unavailable")
	}
	if output, err := exec.Command(node, filepath.Join("testdata", "knowledge-semantic.cjs")).CombinedOutput(); err != nil {
		t.Fatal(err, string(output))
	}
}
