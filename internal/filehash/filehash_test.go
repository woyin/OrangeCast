package filehash

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSHA256(t *testing.T) {
	path := filepath.Join(t.TempDir(), "payload.bin")
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := SHA256(path)
	if err != nil {
		t.Fatalf("SHA256: %v", err)
	}
	const want = "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
	if got != want {
		t.Fatalf("SHA256 = %q, want %q", got, want)
	}
}

func TestSHA256MissingFile(t *testing.T) {
	if _, err := SHA256(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("missing file should return an error")
	}
}
