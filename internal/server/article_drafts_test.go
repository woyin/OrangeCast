package server

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestArticleDraftStableIdentityAndExplicitRecovery(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node unavailable")
	}
	source, err := os.ReadFile("static/article-drafts.js")
	if err != nil {
		t.Fatal(err)
	}
	test, err := os.ReadFile("testdata/article-drafts-test.js")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "article-drafts.js")
	if err = os.WriteFile(path, append(source, test...), 0600); err != nil {
		t.Fatal(err)
	}
	if out, e := exec.Command(node, path).CombinedOutput(); e != nil {
		t.Fatalf("%v: %s", e, out)
	}
}
