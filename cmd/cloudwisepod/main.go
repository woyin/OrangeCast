package main

import (
	"context"
	"flag"
	"fmt"
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
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "backup":
			runBackup(os.Args[2:])
			return
		case "restore":
			runRestore(os.Args[2:])
			return
		}
		if os.Args[1] != "serve" {
			fmt.Fprintf(os.Stderr, "未知命令 %q。可用命令：serve（默认）、backup <目标文件>、restore <备份包> [--force]\n", os.Args[1])
			os.Exit(2)
		}
	}
	runServe()
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
	fs := flag.NewFlagSet("backup", flag.ExitOnError)
	fs.Parse(args)
	dest := ""
	if fs.NArg() >= 1 {
		dest = fs.Arg(0)
	}
	if dest == "" {
		fmt.Fprintln(os.Stderr, "用法：cloudwisepod backup <dest.tar.gz>")
		os.Exit(2)
	}
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("配置错误: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		log.Fatalf("创建数据目录: %v", err)
	}
	s, err := store.Open(cfg.DBPath)
	if err != nil {
		log.Fatalf("打开数据库: %v", err)
	}
	defer s.Close()

	if filepath.Ext(dest) == "" {
		dest += ".tar.gz"
	}
	m, err := backup.Create(context.Background(), s, cfg.EvidenceDir, dest)
	if err != nil {
		log.Fatalf("备份失败: %v", err)
	}
	fmt.Printf("备份完成：%s（DB sha256=%s，证据 %d 个）\n", dest, m.DBSHA256, len(m.Evidence))
}

// runRestore cloudwisepod restore <backup.tar.gz> [--force]
// 恢复到 DATA_DIR（默认要求目标为空，--force 显式覆盖）。
func runRestore(args []string) {
	fs := flag.NewFlagSet("restore", flag.ExitOnError)
	force := fs.Bool("force", false, "目标目录已有数据时显式覆盖")
	fs.Parse(args)
	src := ""
	if fs.NArg() >= 1 {
		src = fs.Arg(0)
	}
	if src == "" {
		fmt.Fprintln(os.Stderr, "用法：cloudwisepod restore <backup.tar.gz> [--force]")
		os.Exit(2)
	}
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("配置错误: %v", err)
	}
	m, err := backup.Restore(context.Background(), src, cfg.DataDir, *force)
	if err != nil {
		log.Fatalf("恢复失败: %v", err)
	}
	fmt.Printf("恢复完成：%s（证据 %d 个，DB sha256=%s）\n", cfg.DataDir, len(m.Evidence), m.DBSHA256)
}
