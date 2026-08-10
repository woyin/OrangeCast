package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/woyin/orangecast/internal/backup"
	"github.com/woyin/orangecast/internal/config"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/queue"
	"github.com/woyin/orangecast/internal/rss"
	"github.com/woyin/orangecast/internal/server"
	"github.com/woyin/orangecast/internal/store"
)

func main() {
	cmd, rest := parseCommand(os.Args[1:])
	switch cmd {
	case "backup":
		runBackup(rest)
	case "restore":
		runRestore(rest)
	case "serve", "":
		runServe()
	default:
		fmt.Fprintf(os.Stderr, "未知命令 %q。可用命令：serve（默认）、backup <目标文件>、restore <备份包> [--force]\n", cmd)
		os.Exit(2)
	}
}

// parseCommand 解析 CLI 参数，返回命令与剩余参数。
// 空参数（默认 runServe）或 "serve" 返回 ("serve"/"", rest)；backup/restore 返回对应命令。
// 便于单元测试 CLI 分发逻辑。
func parseCommand(args []string) (cmd string, rest []string) {
	if len(args) == 0 {
		return "", nil
	}
	switch args[0] {
	case "backup", "restore":
		return args[0], args[1:]
	case "serve":
		return "serve", args[1:]
	default:
		return args[0], args[1:]
	}
}

// runServe 启动 HTTP 服务（默认命令）。
func runServe() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("配置错误: %v", err)
	}

	// 统一数据目录（ADR-0010）：DB / evidence / tmp / backups
	if err := cfg.EnsureDirs(); err != nil {
		log.Fatalf("创建数据目录: %v", err)
	}

	// 数据库
	s, err := store.Open(cfg.DBPath)
	if err != nil {
		log.Fatalf("打开数据库: %v", err)
	}
	defer s.Close()

	// provider 选择器 + worker + cron 刷新器
	selector := provider.NewSelector(cfg.GroqAPIKey, cfg.OpenAIAPIKey)
	// 从 SQLite settings 覆盖 key/URL（可页面配置，ADR-0009 扩展）
	if st, err := s.GetSettings(context.Background()); err == nil {
		selector.ApplySettingsFrom(st)
	}
	// Narration 解说音轨（ADR-0019）：自托管 Kokoro TTS，独立于 groq/openai。
	// 引擎未安装时 Available()==false，worker 跳过合成、不阻塞主流程。
	selector.WithNarration(provider.NewKokoroProvider(cfg.KokoroBinary, cfg.KokoroVoice, cfg.KokoroModel))
	worker := queue.NewWorker(s, selector, cfg.TempDir, cfg.EvidenceDir, cfg.NarrationDir)
	refresher := rss.NewRefresher(s)
	refresher.Start()
	defer refresher.Stop()

	// SQLite 驱动 worker：启动恢复 + 周期领取（ADR-0006）
	workerCtx, workerCancel := context.WithCancel(context.Background())
	go worker.Run(workerCtx)
	defer workerCancel()

	// HTTP server
	srv, err := server.New(cfg, s, worker, refresher, selector)
	if err != nil {
		log.Fatalf("初始化 server: %v", err)
	}

	httpServer := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      srv.Router(),
		ReadTimeout:  15 * time.Minute, // 上传大文件需要
		WriteTimeout: 15 * time.Minute,
	}

	// 优雅关闭
	go func() {
		log.Printf("CloudWisePod 启动于 :%s", cfg.Port)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("服务器错误: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("正在关闭…")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	workerCancel()
	_ = httpServer.Shutdown(ctx)
}

// runBackup cloudwisepod backup <dest.tar.gz>
// 生成一致性备份包（数据库快照 + EvidenceAudio + manifest），不含密钥。
func runBackup(args []string) {
	dest, err := parseBackupArgs(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "用法：cloudwisepod backup <dest.tar.gz>")
		os.Exit(2)
	}
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("配置错误: %v", err)
	}
	m, err := backupCore(cfg, dest)
	if err != nil {
		log.Fatalf("%v", err)
	}
	fmt.Printf("备份完成：%s（DB sha256=%s，证据 %d 个）\n", dest, m.DBSHA256, len(m.Evidence))
}

// backupCore 执行备份核心逻辑（可测试，不调用 os.Exit）。
// 返回生成的 Manifest。调用方负责错误处理与退出。
func backupCore(cfg *config.Config, dest string) (*backup.Manifest, error) {
	if err := cfg.EnsureDirs(); err != nil {
		return nil, fmt.Errorf("创建数据目录: %w", err)
	}
	s, err := store.Open(cfg.DBPath)
	if err != nil {
		return nil, fmt.Errorf("打开数据库: %w", err)
	}
	defer s.Close()

	dest = ensureArchiveExt(dest)
	m, err := backup.Create(context.Background(), s, cfg.EvidenceDir, dest)
	if err != nil {
		return nil, fmt.Errorf("备份失败: %w", err)
	}
	return &m, nil
}

// parseBackupArgs 解析 backup 子命令位置参数，返回目标文件路径。
// flag.ExitOnError 会直接 os.Exit，故单独用 ContinueOnError 解析以便单元测试。
func parseBackupArgs(args []string) (string, error) {
	fs := flag.NewFlagSet("backup", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		return "", err
	}
	if fs.NArg() < 1 {
		return "", errors.New("缺少目标文件")
	}
	return fs.Arg(0), nil
}

// ensureArchiveExt 无扩展名时补 .tar.gz（纯函数，便于测试）。
func ensureArchiveExt(dest string) string {
	if filepath.Ext(dest) == "" {
		return dest + ".tar.gz"
	}
	return dest
}

// runRestore cloudwisepod restore <backup.tar.gz> [--force]
// 恢复到 DATA_DIR（默认要求目标为空，--force 显式覆盖）。
func runRestore(args []string) {
	src, force, err := parseRestoreArgs(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "用法：cloudwisepod restore <backup.tar.gz> [--force]")
		os.Exit(2)
	}
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("配置错误: %v", err)
	}
	m, err := restoreCore(cfg, src, force)
	if err != nil {
		log.Fatalf("%v", err)
	}
	fmt.Printf("恢复完成：%s（证据 %d 个，DB sha256=%s）\n", cfg.DataDir, len(m.Evidence), m.DBSHA256)
}

// restoreCore 执行恢复核心逻辑（可测试，不调用 os.Exit）。
// 返回恢复的 Manifest。调用方负责错误处理与退出。
func restoreCore(cfg *config.Config, src string, force bool) (*backup.Manifest, error) {
	m, err := backup.Restore(context.Background(), src, cfg.DataDir, force)
	if err != nil {
		return nil, fmt.Errorf("恢复失败: %w", err)
	}
	return &m, nil
}

// parseRestoreArgs 解析 restore 子命令参数，返回备份包路径与 force 标识。
func parseRestoreArgs(args []string) (src string, force bool, err error) {
	fs := flag.NewFlagSet("restore", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.BoolVar(&force, "force", false, "目标目录已有数据时显式覆盖")
	if err := fs.Parse(args); err != nil {
		return "", false, err
	}
	if fs.NArg() < 1 {
		return "", false, errors.New("缺少备份包")
	}
	return fs.Arg(0), force, nil
}
