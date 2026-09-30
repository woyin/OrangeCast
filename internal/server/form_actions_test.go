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
