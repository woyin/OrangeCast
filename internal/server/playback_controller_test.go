package server

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSharedPlaybackControllerBehavior(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node unavailable")
	}
	src, err := os.ReadFile("static/playback-controller.js")
	if err != nil {
		t.Fatal(err)
	}
	test, err := os.ReadFile("testdata/playback-controller-test.js")
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "test.js")
	if err := os.WriteFile(file, append(src, test...), 0600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(node, file).CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
}
