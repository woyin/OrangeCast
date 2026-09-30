package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPodConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name, url, key, model string
		valid                 bool
	}{
		{"absent", "", "", "", true}, {"complete", "https://api.example.com/v1/", "secret", "model", true}, {"partial", "https://api.example.com", "", "model", false}, {"scheme", "file:///tmp/x", "secret", "model", false}, {"credentials", "https://user:secret@api.example.com/v1", "secret", "model", false}, {"query", "https://api.example.com/v1?token=secret", "secret", "model", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("SESSION_SECRET", "test")
			t.Setenv("POD_BASE_URL", tc.url)
			t.Setenv("POD_API_KEY", tc.key)
			t.Setenv("POD_MODEL", tc.model)
			cfg, err := Load()
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v error=%v", tc.valid, err)
			}
			if err == nil && tc.key != "" && (!cfg.PodAvailable() || strings.HasSuffix(cfg.PodBaseURL, "/")) {
				t.Fatalf("configuration: %+v", cfg)
			}
		})
	}
}

func TestLoadEnvironmentFileLiteralAndPrecedence(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	t.Setenv("CWP_TEST_EXISTING", "process")
	for _, name := range []string{"CWP_TEST_LITERAL", "CWP_TEST_QUOTED", "CWP_TEST_EMPTY"} {
		old, found := os.LookupEnv(name)
		os.Unsetenv(name)
		t.Cleanup(func() {
			if found {
				os.Setenv(name, old)
			} else {
				os.Unsetenv(name)
			}
		})
	}
	if err := os.WriteFile(path, []byte("# comment\nexport CWP_TEST_EXISTING=file\nCWP_TEST_LITERAL=$(do-not-execute) # inline\nCWP_TEST_QUOTED='literal # $value' # note\nCWP_TEST_EMPTY=\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := LoadEnvironmentFile(path); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("CWP_TEST_EXISTING") != "process" || os.Getenv("CWP_TEST_LITERAL") != "$(do-not-execute)" || os.Getenv("CWP_TEST_QUOTED") != "literal # $value" {
		t.Fatal("environment must remain literal and process values take precedence")
	}
	if err := LoadEnvironmentFile(filepath.Join(t.TempDir(), "missing")); err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{"invalid", "1INVALID=secret", "A='secret", "A='secret' trailing"} {
		if err := os.WriteFile(path, []byte(line), 0600); err != nil {
			t.Fatal(err)
		}
		err := LoadEnvironmentFile(path)
		if err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatalf("format errors must omit values: %v", err)
		}
	}
}
