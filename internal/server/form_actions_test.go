package server

import (
	"os/exec"
	"path/filepath"
	"testing"
)

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
