package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad_MissingSessionSecret(t *testing.T) {
	os.Unsetenv("SESSION_SECRET")
	_, err := Load()
	if err == nil {
		t.Fatal("缺少 SESSION_SECRET 应返回错误")
	}
}

func TestLoad_Defaults(t *testing.T) {
	os.Setenv("SESSION_SECRET", "test-secret")
	defer os.Unsetenv("SESSION_SECRET")
	os.Unsetenv("PORT")
	os.Unsetenv("DATA_DIR")
	os.Unsetenv("PUBLIC_URL")

	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.Port != "8080" {
		t.Errorf("Port 默认应为 8080，实际 %s", c.Port)
	}
	if c.DataDir == "" {
		t.Error("DataDir 不应为空")
	}
	if c.DBPath == "" {
		t.Error("DBPath 不应为空")
	}
	if c.EvidenceDir == "" {
		t.Error("EvidenceDir 不应为空")
	}
	if c.BackupDir == "" {
		t.Error("BackupDir 不应为空")
	}
	// DBPath 应在 DataDir 下
	if !filepath.IsAbs(c.DBPath) && c.DataDir != "" {
		expectedDB := filepath.Join(c.DataDir, "cloudwisepod.db")
		if c.DBPath != expectedDB {
			t.Errorf("DBPath 默认应为 %s，实际 %s", expectedDB, c.DBPath)
		}
	}
}

func TestLoad_PublicSchemeIsHTTPS(t *testing.T) {
	os.Setenv("SESSION_SECRET", "test-secret")
	defer os.Unsetenv("SESSION_SECRET")

	os.Setenv("PUBLIC_URL", "https://cwp.example.com")
	c, _ := Load()
	if !c.PublicSchemeIsHTTPS() {
		t.Error("https URL 应返回 true")
	}

	os.Setenv("PUBLIC_URL", "http://localhost:8080")
	c, _ = Load()
	if c.PublicSchemeIsHTTPS() {
		t.Error("http URL 应返回 false")
	}
	os.Unsetenv("PUBLIC_URL")
}

func TestLoad_TrustedProxies(t *testing.T) {
	os.Setenv("SESSION_SECRET", "test-secret")
	defer os.Unsetenv("SESSION_SECRET")

	os.Setenv("TRUSTED_PROXIES", "127.0.0.1/32, 10.0.0.0/8")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(c.TrustedProxies) != 2 {
		t.Errorf("应解析 2 个 CIDR，实际 %d", len(c.TrustedProxies))
	}
	os.Unsetenv("TRUSTED_PROXIES")
}

func TestLoad_TrustedProxies_Invalid(t *testing.T) {
	os.Setenv("SESSION_SECRET", "test-secret")
	defer os.Unsetenv("SESSION_SECRET")

	os.Setenv("TRUSTED_PROXIES", "not-a-cidr")
	_, err := Load()
	if err == nil {
		t.Fatal("非法 CIDR 应返回错误")
	}
	os.Unsetenv("TRUSTED_PROXIES")
}

func TestEnsureDirs(t *testing.T) {
	dir := t.TempDir()
	c := &Config{
		DataDir:      filepath.Join(dir, "data"),
		EvidenceDir:  filepath.Join(dir, "data/evidence"),
		TempDir:      filepath.Join(dir, "data/tmp"),
		BackupDir:    filepath.Join(dir, "data/backups"),
		NarrationDir: filepath.Join(dir, "data/narrations"),
	}
	if err := c.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{c.DataDir, c.EvidenceDir, c.TempDir, c.BackupDir, c.NarrationDir} {
		if fi, err := os.Stat(d); err != nil || !fi.IsDir() {
			t.Errorf("目录 %s 未创建", d)
		}
	}
}
