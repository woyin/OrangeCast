package main

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/woyin/orangecast/internal/config"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/store"
)

// TestParseCommand 验证 CLI 命令分发解析。
func TestParseCommand(t *testing.T) {
	cases := []struct {
		name string
		args []string
		cmd  string
		rest []string
	}{
		{"no args", nil, "", nil},
		{"serve default", []string{"serve"}, "serve", []string{}},
		{"backup", []string{"backup", "dest.tar.gz"}, "backup", []string{"dest.tar.gz"}},
		{"restore", []string{"restore", "src.tar.gz", "--force"}, "restore", []string{"src.tar.gz", "--force"}},
		{"unknown", []string{"bogus"}, "bogus", []string{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, rest := parseCommand(c.args)
			if got != c.cmd {
				t.Errorf("parseCommand(%v) cmd = %q, want %q", c.args, got, c.cmd)
			}
			if !reflect.DeepEqual(rest, c.rest) {
				t.Errorf("parseCommand(%v) rest = %v, want %v", c.args, rest, c.rest)
			}
		})
	}
}

// TestEnsureArchiveExt 验证备份目标路径扩展名补全。
func TestEnsureArchiveExt(t *testing.T) {
	if got := ensureArchiveExt("backup"); got != "backup.tar.gz" {
		t.Errorf("ensureArchiveExt(backup)=%q want backup.tar.gz", got)
	}
	if got := ensureArchiveExt("backup.tar.gz"); got != "backup.tar.gz" {
		t.Errorf("已带扩展名应原样，实际 %q", got)
	}
	if got := ensureArchiveExt("/path/to/x"); got != "/path/to/x.tar.gz" {
		t.Errorf("绝对路径无扩展应补全，实际 %q", got)
	}
}

func TestParseBackupArgs(t *testing.T) {
	if dest, err := parseBackupArgs([]string{"out.tar.gz"}); err != nil || dest != "out.tar.gz" {
		t.Errorf("parseBackupArgs([out.tar.gz]) = %q, %v", dest, err)
	}
	if _, err := parseBackupArgs(nil); err == nil {
		t.Error("无参数应报错")
	}
	if _, err := parseBackupArgs([]string{"-bad-flag"}); err == nil {
		t.Error("非法 flag 应报错")
	}
}

func TestParseRestoreArgs(t *testing.T) {
	src, force, err := parseRestoreArgs([]string{"in.tar.gz"})
	if err != nil || src != "in.tar.gz" || force {
		t.Errorf("parseRestoreArgs([in.tar.gz]) = %q, %v, %v", src, force, err)
	}
	src, force, err = parseRestoreArgs([]string{"--force", "in.tar.gz"})
	if err != nil || src != "in.tar.gz" || !force {
		t.Errorf("带 --force 应解析为 force=true，实际 %q, %v, %v", src, force, err)
	}
	if _, _, err := parseRestoreArgs(nil); err == nil {
		t.Error("无参数应报错")
	}
	if _, _, err := parseRestoreArgs([]string{"-bad"}); err == nil {
		t.Error("非法 flag 应报错")
	}
}

// testCfg 构造一个指向临时数据目录的配置（含已初始化 DB）。
func testCfg(t *testing.T) *config.Config {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.Config{
		Port:         "0",
		DBPath:       filepath.Join(dir, "cloudwisepod.db"),
		DataDir:      dir,
		EvidenceDir:  filepath.Join(dir, "evidence"),
		BackupDir:    filepath.Join(dir, "backups"),
		NarrationDir: filepath.Join(dir, "narrations"),
		TempDir:      filepath.Join(dir, "tmp"),
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	s, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("打开测试 DB: %v", err)
	}
	defer s.Close()
	// 造一条证据，使备份包非空
	p, _ := s.CreatePodcast(context.Background(), "https://f.xml", "P", "", "")
	s.MergeEpisodes(context.Background(), p.ID, []models.Episode{{GUID: "g1", Title: "e", AudioURL: "https://a.mp3"}})
	eps, _ := s.ListEpisodes(context.Background(), p.ID)
	s.UpsertEvidenceAudio(context.Background(), models.SourceEpisode, eps[0].ID, "ep.mp3", "mp3", 10, "abc")
	os.WriteFile(filepath.Join(cfg.EvidenceDir, "ep.mp3"), []byte("audio"), 0o644)
	return cfg
}

// TestBackupCore 验证备份核心逻辑生成非空 manifest 与备份包。
func TestBackupCore(t *testing.T) {
	cfg := testCfg(t)
	dest := filepath.Join(cfg.BackupDir, "backup")
	m, err := backupCore(cfg, dest)
	if err != nil {
		t.Fatalf("backupCore: %v", err)
	}
	if m.DBSHA256 == "" {
		t.Error("manifest 应含 DB sha256")
	}
	if len(m.Evidence) != 1 {
		t.Errorf("应打包 1 个证据，实际 %d", len(m.Evidence))
	}
	if _, err := os.Stat(dest + ".tar.gz"); err != nil {
		t.Errorf("备份包应生成: %v", err)
	}
}

// TestBackupCore_InvalidDest 验证备份目标不可创建时 backupCore 报错。
func TestBackupCore_InvalidDest(t *testing.T) {
	cfg := testCfg(t)
	// 目标父目录被文件占用 → EnsureDirs/备份失败
	blocker := filepath.Join(cfg.BackupDir, "block")
	os.WriteFile(blocker, []byte("x"), 0o644)
	if _, err := backupCore(cfg, filepath.Join(blocker, "sub", "b.tar.gz")); err == nil {
		t.Error("不可创建的目标目录应报错")
	}
}

// TestRestoreCore 验证恢复核心把备份包恢复到目标目录并可打开。
func TestRestoreCore(t *testing.T) {
	cfg := testCfg(t)
	dest := filepath.Join(cfg.BackupDir, "backup.tar.gz")
	if _, err := backupCore(cfg, filepath.Join(cfg.BackupDir, "backup")); err != nil {
		t.Fatalf("backupCore: %v", err)
	}
	// 恢复到全新目标目录
	restoreCfg := &config.Config{DataDir: t.TempDir()}
	m, err := restoreCore(restoreCfg, dest, false)
	if err != nil {
		t.Fatalf("restoreCore: %v", err)
	}
	if len(m.Evidence) != 1 {
		t.Errorf("应恢复 1 个证据，实际 %d", len(m.Evidence))
	}
	if _, err := os.Stat(filepath.Join(restoreCfg.DataDir, "cloudwisepod.db")); err != nil {
		t.Errorf("恢复的 DB 应存在: %v", err)
	}
}
