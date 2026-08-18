package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/woyin/orangecast/internal/config"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/queue"
	"github.com/woyin/orangecast/internal/rss"
	"github.com/woyin/orangecast/internal/server"
	"github.com/woyin/orangecast/internal/store"
)

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
	srv.StartAutomaticDiscovery(workerCtx)

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
