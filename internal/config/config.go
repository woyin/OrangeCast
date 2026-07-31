package config

import (
	"fmt"
	"os"
)

// Config 应用配置，从环境变量读取。
type Config struct {
	Port           string
	DBPath         string
	SessionSecret  string
	TempDir        string // 音频临时落盘目录
	GroqAPIKey     string
	OpenAIAPIKey   string
	BaseURL        string // 站点根 URL，用于绝对路径
}

// Load 从环境变量加载配置。缺失关键项返回错误（生产不静默回退）。
func Load() (*Config, error) {
	c := &Config{
		Port:          envOrDefault("PORT", "8080"),
		DBPath:        envOrDefault("DB_PATH", "./data/cloudwisepod.db"),
		SessionSecret: os.Getenv("SESSION_SECRET"),
		TempDir:       envOrDefault("TEMP_DIR", os.TempDir()),
		GroqAPIKey:    os.Getenv("GROQ_API_KEY"),
		OpenAIAPIKey:  os.Getenv("OPENAI_API_KEY"),
		BaseURL:       envOrDefault("BASE_URL", "http://localhost:8080"),
	}
	if c.SessionSecret == "" {
		return nil, fmt.Errorf("SESSION_SECRET 必须设置")
	}
	return c, nil
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
