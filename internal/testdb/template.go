// Package testdb copies a closed, migrated SQLite baseline into independent test directories.
// It never shares a mutable connection or skips the application's database opener.
package testdb

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// createClone is the file-writing seam used to exercise disk and close failures.
var createClone = func(path string) (io.WriteCloser, error) {
	return os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
}

var templates = struct {
	sync.Mutex
	files map[string][]byte
}{files: map[string][]byte{}}

// Open clones a schema-only database, then invokes the normal opener for connection setup.
// The opener must initialize an empty database without test data and close all handles on Close.
// Migration/startup/upgrade tests must call their application opener directly instead.
func Open[T interface{ Close() error }](path, migrationDir string, opener func(string) (T, error)) (T, error) {
	var zero T
	key, err := fingerprint(migrationDir)
	if err != nil {
		return zero, err
	}
	templates.Lock()
	data := templates.files[key]
	if data == nil {
		data, err = buildTemplate(opener)
		if err == nil {
			templates.files[key] = data
		}
	}
	templates.Unlock()
	if err != nil {
		return zero, err
	}
	file, err := createClone(path)
	if err != nil {
		return zero, err
	}
	if _, err = file.Write(data); err != nil {
		file.Close()
		return zero, err
	}
	if err = file.Close(); err != nil {
		return zero, err
	}
	return opener(path)
}

func fingerprint(dir string) (string, error) {
	absolute := filepath.Clean(dir)
	entries, err := os.ReadDir(absolute)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	count := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(absolute, entry.Name()))
		if err != nil {
			return "", err
		}
		fmt.Fprintf(hash, "%d:%s:%d:", len(entry.Name()), entry.Name(), len(body))
		hash.Write(body)
		count++
	}
	if count == 0 {
		return "", fmt.Errorf("test database requires actual SQL migrations")
	}
	return absolute + ":" + fmt.Sprintf("%x", hash.Sum(nil)), nil
}

func buildTemplate[T interface{ Close() error }](opener func(string) (T, error)) ([]byte, error) {
	dir, err := os.MkdirTemp("", "cwp-closed-test-schema-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "template.db")
	db, err := opener(path)
	if err != nil {
		return nil, err
	}
	if err = db.Close(); err != nil {
		return nil, err
	}
	// Never copy a live WAL or a partial main file.
	if info, err := os.Stat(path + "-wal"); err == nil && info.Size() > 0 {
		return nil, fmt.Errorf("test template still contains an uncheckpointed WAL")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) < 16 || string(data[:16]) != "SQLite format 3\x00" {
		return nil, fmt.Errorf("test template is not a closed SQLite database")
	}
	return data, nil
}
