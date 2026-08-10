package main

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"syscall"
	"testing"
	"time"

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

// TestMainDispatchesBackupAndRestore 验证 CLI 入口将 backup/restore 命令
// 分发到对应的真实工作流，并在进程环境中完成一次备份与恢复。
func TestMainDispatchesBackupAndRestore(t *testing.T) {
	cfg := testCfg(t)
	t.Setenv("DATA_DIR", cfg.DataDir)
	t.Setenv("SESSION_SECRET", "test-session-secret")

	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })

	dest := filepath.Join(cfg.BackupDir, "dispatch-backup")
	os.Args = []string{"cloudwisepod", "backup", dest}
	main()
	archive := dest + ".tar.gz"
	if _, err := os.Stat(archive); err != nil {
		t.Fatalf("backup 命令应生成归档: %v", err)
	}

	restoreDir := t.TempDir()
	t.Setenv("DATA_DIR", restoreDir)
	os.Args = []string{"cloudwisepod", "restore", archive}
	main()
	if _, err := os.Stat(filepath.Join(restoreDir, "cloudwisepod.db")); err != nil {
		t.Fatalf("restore 命令应恢复数据库: %v", err)
	}
}

// TestMainServeStartsAndStops 验证默认 HTTP 服务入口能启动路由并响应
// SIGTERM，完成 worker、刷新器与 HTTP server 的优雅关闭。
func TestMainServeStartsAndStops(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("寻找可用端口: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()

	dataDir := t.TempDir()
	t.Setenv("DATA_DIR", dataDir)
	t.Setenv("PORT", strconv.Itoa(port))
	t.Setenv("SESSION_SECRET", "test-session-secret")
	t.Setenv("PUBLIC_URL", "http://127.0.0.1:"+strconv.Itoa(port))

	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })
	os.Args = []string{"cloudwisepod", "serve"}

	done := make(chan struct{})
	go func() {
		main()
		close(done)
	}()

	client := &http.Client{Timeout: 200 * time.Millisecond}
	url := "http://127.0.0.1:" + strconv.Itoa(port) + "/login"
	deadline := time.Now().Add(10 * time.Second)
	var response *http.Response
	for time.Now().Before(deadline) {
		response, err = client.Get(url)
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("serve 应启动 HTTP 路由: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET /login status = %d, want %d", response.StatusCode, http.StatusOK)
	}

	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatalf("发送 SIGTERM: %v", err)
	}
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("serve 收到 SIGTERM 后未退出")
	}
	if _, err := os.Stat(filepath.Join(dataDir, "cloudwisepod.db")); err != nil {
		t.Fatalf("serve 应初始化数据库: %v", err)
	}
}

// TestBackupCore_StoreOpenFails 验证 DB 路径不可打开时 backupCore 报错。
// 覆盖 backupCore 中 "打开数据库" 错误分支。
func TestBackupCore_StoreOpenFails(t *testing.T) {
	dir := t.TempDir()
	// 让 DB 路径父目录被文件占用 → store.Open 失败
	blocker := filepath.Join(dir, "blockfile")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Port:         "0",
		DBPath:       filepath.Join(blocker, "cloudwisepod.db"),
		DataDir:      dir,
		EvidenceDir:  filepath.Join(dir, "evidence"),
		BackupDir:    filepath.Join(dir, "backups"),
		NarrationDir: filepath.Join(dir, "narrations"),
		TempDir:      filepath.Join(dir, "tmp"),
	}
	if _, err := backupCore(cfg, filepath.Join(cfg.BackupDir, "b.tar.gz")); err == nil {
		t.Fatal("DB 父目录为文件时 backupCore 应报错")
	}
}

// TestBackupCore_EnsureDirsFails 验证数据目录不可创建时 backupCore 报错。
// 覆盖 backupCore 中 "创建数据目录" 错误分支（DataDir 被文件占用）。
func TestBackupCore_EnsureDirsFails(t *testing.T) {
	dir := t.TempDir()
	// DataDir 被文件占用 → EnsureDirs 的 MkdirAll 失败
	blocker := filepath.Join(dir, "data-blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Port:         "0",
		DBPath:       filepath.Join(blocker, "cloudwisepod.db"),
		DataDir:      blocker, // 文件，非目录
		EvidenceDir:  filepath.Join(blocker, "evidence"),
		BackupDir:    filepath.Join(blocker, "backups"),
		NarrationDir: filepath.Join(blocker, "narrations"),
		TempDir:      filepath.Join(blocker, "tmp"),
	}
	if _, err := backupCore(cfg, filepath.Join(cfg.BackupDir, "b.tar.gz")); err == nil {
		t.Fatal("DataDir 为文件时 backupCore 应报错")
	}
}

// TestRunBackup_ConfigErrorExits 验证 runBackup 配置错误时 log.Fatalf 退出。
// 覆盖 runBackup 中 "配置错误" log.Fatalf 分支（缺 SESSION_SECRET）。
func TestRunBackup_ConfigErrorExits(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		cmd := exec.Command(os.Args[0], "-test.run=TestRunBackup_ConfigErrorExits")
		cmd.Env = append(os.Environ(),
			"GO_WANT_HELPER_PROCESS=1",
			"SESSION_SECRET=", // 空 → config.Load 报错
			"DATA_DIR="+t.TempDir(),
		)
		err := cmd.Run()
		if exitErr, ok := err.(*exec.ExitError); !ok || exitErr.ExitCode() != 1 {
			t.Fatalf("runBackup 配置错误应退出码 1，实际 %v", err)
		}
		return
	}
	os.Args = []string{"cloudwisepod", "backup", "out.tar.gz"}
	runBackup([]string{"out.tar.gz"})
}

// TestRunRestore_ConfigErrorExits 验证 runRestore 配置错误时 log.Fatalf 退出。
// 覆盖 runRestore 中 "配置错误" log.Fatalf 分支（缺 SESSION_SECRET）。
func TestRunRestore_ConfigErrorExits(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		cmd := exec.Command(os.Args[0], "-test.run=TestRunRestore_ConfigErrorExits")
		cmd.Env = append(os.Environ(),
			"GO_WANT_HELPER_PROCESS=1",
			"SESSION_SECRET=", // 空 → config.Load 报错
			"DATA_DIR="+t.TempDir(),
		)
		err := cmd.Run()
		if exitErr, ok := err.(*exec.ExitError); !ok || exitErr.ExitCode() != 1 {
			t.Fatalf("runRestore 配置错误应退出码 1，实际 %v", err)
		}
		return
	}
	runRestore([]string{"in.tar.gz"})
}

// TestRunServe_ConfigErrorExits 验证 runServe 配置错误时 log.Fatalf 退出。
// 覆盖 runServe 中 "配置错误" log.Fatalf 分支（缺 SESSION_SECRET）。
func TestRunServe_ConfigErrorExits(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		cmd := exec.Command(os.Args[0], "-test.run=TestRunServe_ConfigErrorExits")
		cmd.Env = append(os.Environ(),
			"GO_WANT_HELPER_PROCESS=1",
			"SESSION_SECRET=", // 空 → config.Load 报错
		)
		err := cmd.Run()
		if exitErr, ok := err.(*exec.ExitError); !ok || exitErr.ExitCode() != 1 {
			t.Fatalf("runServe 配置错误应退出码 1，实际 %v", err)
		}
		return
	}
	runServe()
}

// TestRunBackup_FailureExits 验证 runBackup 备份失败时 log.Fatalf 退出。
// 覆盖 runBackup 中备份失败 log.Fatalf 分支（目标路径不可创建）。
func TestRunBackup_FailureExits(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		cmd := exec.Command(os.Args[0], "-test.run=TestRunBackup_FailureExits")
		cmd.Env = append(os.Environ(),
			"GO_WANT_HELPER_PROCESS=1",
			"SESSION_SECRET=test-secret",
			"DATA_DIR="+t.TempDir(),
		)
		err := cmd.Run()
		if exitErr, ok := err.(*exec.ExitError); !ok || exitErr.ExitCode() != 1 {
			t.Fatalf("runBackup 备份失败应退出码 1，实际 %v", err)
		}
		return
	}
	// 目标路径父目录被文件占用 → 备份失败
	blocker := filepath.Join(os.TempDir(), "cwp-block-"+strconv.Itoa(os.Getpid()))
	os.WriteFile(blocker, []byte("x"), 0o644)
	defer os.Remove(blocker)
	runBackup([]string{filepath.Join(blocker, "sub", "out.tar.gz")})
}

// TestRunServe_EnsureDirsErrorExits 验证 runServe 创建数据目录失败时 log.Fatalf 退出。
// 覆盖 runServe 中 "创建数据目录" log.Fatalf 分支。
func TestRunServe_EnsureDirsErrorExits(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		dir := t.TempDir()
		blocker := filepath.Join(dir, "blocked")
		os.WriteFile(blocker, []byte("x"), 0o644)
		cmd := exec.Command(os.Args[0], "-test.run=TestRunServe_EnsureDirsErrorExits")
		cmd.Env = append(os.Environ(),
			"GO_WANT_HELPER_PROCESS=1",
			"SESSION_SECRET=test-secret",
			"DATA_DIR="+filepath.Join(dir, "blocked", "data"),
		)
		err := cmd.Run()
		if exitErr, ok := err.(*exec.ExitError); !ok || exitErr.ExitCode() != 1 {
			t.Fatalf("runServe EnsureDirs 失败应退出码 1，实际 %v", err)
		}
		return
	}
	// 子进程：DATA_DIR 父目录被文件占用 → EnsureDirs 失败 → log.Fatalf 退出
	runServe()
}

// TestRunRestore_FailureExits 验证 runRestore 恢复失败时 log.Fatalf 退出。
// 覆盖 runRestore 中恢复失败 log.Fatalf 分支（备份包不存在）。
func TestRunRestore_FailureExits(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		cmd := exec.Command(os.Args[0], "-test.run=TestRunRestore_FailureExits")
		cmd.Env = append(os.Environ(),
			"GO_WANT_HELPER_PROCESS=1",
			"SESSION_SECRET=test-secret",
			"DATA_DIR="+t.TempDir(),
		)
		err := cmd.Run()
		if exitErr, ok := err.(*exec.ExitError); !ok || exitErr.ExitCode() != 1 {
			t.Fatalf("runRestore 恢复失败应退出码 1，实际 %v", err)
		}
		return
	}
	// 备份包不存在 → Restore 失败 → log.Fatalf 退出
	runRestore([]string{filepath.Join(t.TempDir(), "missing.tar.gz")})
}

// TestRunServe_OpenDBErrorExits 验证 runServe 打开数据库失败时 log.Fatalf 退出。
// 覆盖 runServe 中 "打开数据库" log.Fatalf 分支（DB_PATH 父目录为文件）。
func TestRunServe_OpenDBErrorExits(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		dir := t.TempDir()
		blocker := filepath.Join(dir, "db-blocker")
		os.WriteFile(blocker, []byte("x"), 0o644)
		cmd := exec.Command(os.Args[0], "-test.run=TestRunServe_OpenDBErrorExits")
		cmd.Env = append(os.Environ(),
			"GO_WANT_HELPER_PROCESS=1",
			"SESSION_SECRET=test-secret",
			"DATA_DIR="+filepath.Join(dir, "data"),
			"DB_PATH="+filepath.Join(blocker, "cloudwisepod.db"),
		)
		err := cmd.Run()
		if exitErr, ok := err.(*exec.ExitError); !ok || exitErr.ExitCode() != 1 {
			t.Fatalf("runServe 打开数据库失败应退出码 1，实际 %v", err)
		}
		return
	}
	// 子进程：DB_PATH 父目录被文件占用 → store.Open 失败 → log.Fatalf 退出
	runServe()
}

// TestRunServe_ListenErrorExits 验证 runServe 监听失败时 log.Fatalf 退出。
// 覆盖 runServe 中 "服务器错误" log.Fatalf 分支（PORT 非法使 ListenAndServe 立即失败）。
// 与端口占用不同：非法端口号在地址解析阶段即报错，不依赖 SO_REUSEADDR，稳定可测。
func TestRunServe_ListenErrorExits(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		dir := t.TempDir()
		cmd := exec.Command(os.Args[0], "-test.run=TestRunServe_ListenErrorExits")
		cmd.Env = append(os.Environ(),
			"GO_WANT_HELPER_PROCESS=1",
			"SESSION_SECRET=test-secret",
			"DATA_DIR="+filepath.Join(dir, "data"),
			"PORT=notaport",
		)
		err := cmd.Run()
		if exitErr, ok := err.(*exec.ExitError); !ok || exitErr.ExitCode() != 1 {
			t.Fatalf("runServe 监听失败应退出码 1，实际 %v", err)
		}
		return
	}
	// 子进程：PORT 非法 → httpServer.ListenAndServe 立即返回错误 → log.Fatalf 退出
	runServe()
}

// TestRestoreCore_Fails 验证恢复缺失备份包时 restoreCore 包装错误返回。
// 覆盖 restoreCore 中 "恢复失败" 错误分支。
func TestRestoreCore_Fails(t *testing.T) {
	cfg := &config.Config{DataDir: t.TempDir()}
	_, err := restoreCore(cfg, filepath.Join(t.TempDir(), "missing.tar.gz"), false)
	if err == nil {
		t.Fatal("缺失备份包应报错")
	}
	if !strings.Contains(err.Error(), "恢复失败") {
		t.Errorf("错误应包装 '恢复失败'，实际 %v", err)
	}
}

// TestRunBackup_BadArgsExits 验证 runBackup 参数错误时通过 os.Exit(2) 退出。
// 覆盖 runBackup 中 "用法" 错误分支。
func TestRunBackup_BadArgsExits(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		cmd := exec.Command(os.Args[0], "-test.run=TestRunBackup_BadArgsExits")
		cmd.Env = append(os.Environ(), "GO_WANT_HELPER_PROCESS=1")
		err := cmd.Run()
		if exitErr, ok := err.(*exec.ExitError); !ok || exitErr.ExitCode() != 2 {
			t.Fatalf("runBackup 参数错误应退出码 2，实际 %v", err)
		}
		return
	}
	// 子进程：直接调用 runBackup(nil) 应 os.Exit(2)
	runBackup(nil)
}

// TestRunRestore_BadArgsExits 验证 runRestore 参数错误时通过 os.Exit(2) 退出。
// 覆盖 runRestore 中 "用法" 错误分支。
func TestRunRestore_BadArgsExits(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		cmd := exec.Command(os.Args[0], "-test.run=TestRunRestore_BadArgsExits")
		cmd.Env = append(os.Environ(), "GO_WANT_HELPER_PROCESS=1")
		err := cmd.Run()
		if exitErr, ok := err.(*exec.ExitError); !ok || exitErr.ExitCode() != 2 {
			t.Fatalf("runRestore 参数错误应退出码 2，实际 %v", err)
		}
		return
	}
	runRestore(nil)
}

// TestMainUnknownCommandExits 验证未知命令时 main 向 stderr 输出用法并退出码 2。
// 覆盖 main 中 default 分支（未知命令）。
func TestMainUnknownCommandExits(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		cmd := exec.Command(os.Args[0], "-test.run=TestMainUnknownCommandExits")
		cmd.Env = append(os.Environ(), "GO_WANT_HELPER_PROCESS=1")
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		err := cmd.Run()
		if exitErr, ok := err.(*exec.ExitError); !ok || exitErr.ExitCode() != 2 {
			t.Fatalf("未知命令应退出码 2，实际 %v", err)
		}
		if !strings.Contains(stderr.String(), "未知命令") {
			t.Errorf("stderr 应含 '未知命令'，实际 %q", stderr.String())
		}
		return
	}
	oldArgs := os.Args
	os.Args = []string{"cloudwisepod", "totally-bogus-command"}
	defer func() { os.Args = oldArgs }()
	main()
}
