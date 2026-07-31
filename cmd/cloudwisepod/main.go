package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/breestealth/wisepod/internal/config"
	"github.com/breestealth/wisepod/internal/provider"
	"github.com/breestealth/wisepod/internal/queue"
	"github.com/breestealth/wisepod/internal/rss"
	"github.com/breestealth/wisepod/internal/server"
	"github.com/breestealth/wisepod/internal/store"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("配置错误: %v", err)
	}

	// 数据库
	s, err := store.Open(cfg.DBPath)
	if err != nil {
		log.Fatalf("打开数据库: %v", err)
	}
	defer s.Close()

	// 确保数据目录存在
	if err := os.MkdirAll(cfg.TempDir, 0o755); err != nil {
		log.Fatalf("创建临时目录: %v", err)
	}

	// provider 选择器 + worker + cron 刷新器
	selector := provider.NewSelector(cfg.GroqAPIKey, cfg.OpenAIAPIKey)
	worker := queue.NewWorker(s, selector, cfg.TempDir)
	refresher := rss.NewRefresher(s)
	refresher.Start()
	defer refresher.Stop()

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
	_ = httpServer.Shutdown(ctx)
}
