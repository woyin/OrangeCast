package config

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
)

// Config 应用配置，从环境变量读取。
type Config struct {
	Port           string
	DBPath         string
	SessionSecret  string
	TempDir        string // 临时文件目录（下载/转码中间产物）
	GroqAPIKey     string
	OpenAIAPIKey   string
	PublicURL      string   // 站点公开 URL（Secure Cookie 判定 + 绝对链接）
	TrustedProxies []string // 受信任反向代理 CIDR（仅这些来源的转发头被信任）
	DataDir        string   // 统一数据目录（ADR-0010）：DB + evidence + tmp + backups
	EvidenceDir    string   // 持久 EvidenceAudio 目录（DATA_DIR/evidence）
	BackupDir      string   // 备份输出目录（DATA_DIR/backups）
	NarrationDir   string   // Narration 解说音轨目录（DATA_DIR/narrations，ADR-0019）
	KokoroBinary   string   // Kokoro TTS 二进制路径（默认 PATH 查找 kokoro，ADR-0019）
	KokoroVoice    string   // Kokoro 默认音色（默认 af_heart）
	KokoroModel    string   // Kokoro 模型文件路径（可选，某些发行版需要）
}

// Load 从环境变量加载配置。缺失关键项返回错误（生产不静默回退）。
func Load() (*Config, error) {
	c := &Config{
		Port:          envOrDefault("PORT", "8080"),
		SessionSecret: os.Getenv("SESSION_SECRET"),
		GroqAPIKey:    os.Getenv("GROQ_API_KEY"),
		OpenAIAPIKey:  os.Getenv("OPENAI_API_KEY"),
		PublicURL:     envOrDefault("PUBLIC_URL", envOrDefault("BASE_URL", "http://localhost:8080")),
	}
	// 统一数据目录（ADR-0010）：DB/evidence/tmp/backups 全部落在 DATA_DIR 之下。
	c.DataDir = envOrDefault("DATA_DIR", envOrDefault("DB_PATH_DIR", "./data"))
	// 兼容旧 DB_PATH / TEMP_DIR 环境变量：未显式设置时使用 DATA_DIR 布局。
	c.DBPath = envOrDefault("DB_PATH", filepath.Join(c.DataDir, "cloudwisepod.db"))
	c.TempDir = envOrDefault("TEMP_DIR", filepath.Join(c.DataDir, "tmp"))
	c.EvidenceDir = filepath.Join(c.DataDir, "evidence")
	c.BackupDir = filepath.Join(c.DataDir, "backups")
	c.NarrationDir = envOrDefault("NARRATION_DIR", filepath.Join(c.DataDir, "narrations"))
	c.KokoroBinary = envOrDefault("KOKORO_BINARY", "kokoro")
	c.KokoroVoice = envOrDefault("KOKORO_VOICE", "af_heart")
	c.KokoroModel = os.Getenv("KOKORO_MODEL")

	if c.SessionSecret == "" {
		return nil, fmt.Errorf("SESSION_SECRET 必须设置")
	}
	// 受信任代理：逗号分隔的 CIDR（如 127.0.0.1/32, 10.0.0.0/8）
	if raw := os.Getenv("TRUSTED_PROXIES"); raw != "" {
		for _, part := range strings.Split(raw, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			if _, _, err := net.ParseCIDR(part); err != nil {
				return nil, fmt.Errorf("TRUSTED_PROXIES 含非法 CIDR %q: %w", part, err)
			}
			c.TrustedProxies = append(c.TrustedProxies, part)
		}
	}
	return c, nil
}

// PublicSchemeIsHTTPS 返回 PUBLIC_URL 是否使用 https（用于 Secure Cookie 判定）。
func (c *Config) PublicSchemeIsHTTPS() bool {
	return strings.HasPrefix(c.PublicURL, "https://")
}

// EnsureDirs 创建 DataDir/EvidenceDir/TempDir/BackupDir。
func (c *Config) EnsureDirs() error {
	for _, d := range []string{c.DataDir, c.EvidenceDir, c.TempDir, c.BackupDir, c.NarrationDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return fmt.Errorf("创建目录 %s: %w", d, err)
		}
	}
	return nil
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
