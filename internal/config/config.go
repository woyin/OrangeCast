// Package config 负责应用配置的加载与统一数据目录布局（ADR-0010）。
//
// 配置从环境变量读取（见 README「配置」表）：SESSION_SECRET / GROQ_API_KEY /
// PORT / DATA_DIR / PUBLIC_URL / TRUSTED_PROXIES 等。DATA_DIR 是唯一数据根目录，
// DB / evidence / tmp / backups / narrations 全部落在其下（可通过 DB_PATH/TEMP_DIR/
// NARRATION_DIR 覆盖）。EnsureDirs 在启动与备份/恢复前创建目录树。
package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Config 应用配置，从环境变量读取。
type Config struct {
	EmbeddingBaseURL            string
	EmbeddingAPIKey             string
	EmbeddingModel              string
	EmbeddingDimensions         int
	VoiceDir                    string
	VoiceASRProvider            string
	VoiceASRModel               string
	VoiceASRBaseURL             string
	VoiceASRAPIKey              string
	Port                        string
	DBPath                      string
	SessionSecret               string
	TempDir                     string // 临时文件目录（下载/转码中间产物）
	GroqAPIKey                  string
	OpenAIAPIKey                string
	PodBaseURL                  string // 自动知识文章的独立 OpenAI 兼容文本连接。
	PodAPIKey                   string
	PodReviewModel              string
	PodModel                    string
	PodDiscoveryModel           string
	PodSelectionModel           string
	PodWriteModel               string
	PodQuestionStudyModel       string
	PodQuestionStudyReviewModel string
	PodLearningReviewModel      string
	PublicURL                   string   // 站点公开 URL（Secure Cookie 判定 + 绝对链接）
	TrustedProxies              []string // 受信任反向代理 CIDR（仅这些来源的转发头被信任）
	DataDir                     string   // 统一数据目录（ADR-0010）：DB + evidence + tmp + backups
	EvidenceDir                 string   // 持久 EvidenceAudio 目录（DATA_DIR/evidence）
	BackupDir                   string   // 备份输出目录（DATA_DIR/backups）
	NarrationDir                string   // Narration 解说音轨目录（DATA_DIR/narrations，ADR-0019）
	KokoroBinary                string   // Kokoro TTS 二进制路径（默认 PATH 查找 kokoro，ADR-0019）
	KokoroVoice                 string   // Kokoro 默认音色（默认 af_heart）
	KokoroModel                 string   // Kokoro 模型文件路径（可选，某些发行版需要）
	KokoroLanguage              string   // 语言（en|zh，D03；中文音色 zf_/zm_ 需 misaki[zh]）
	KokoroTimeoutSeconds        int      // 单次合成超时秒数（D03，默认 120）
}

// Load 从环境变量加载配置。缺失关键项返回错误（生产不静默回退）。
func Load() (*Config, error) {
	c := &Config{
		EmbeddingBaseURL:            strings.TrimRight(strings.TrimSpace(os.Getenv("LEARNING_EMBEDDING_BASE_URL")), "/"),
		EmbeddingAPIKey:             strings.TrimSpace(os.Getenv("LEARNING_EMBEDDING_API_KEY")),
		EmbeddingModel:              strings.TrimSpace(os.Getenv("LEARNING_EMBEDDING_MODEL")),
		VoiceASRProvider:            strings.TrimSpace(os.Getenv("VOICE_ASR_PROVIDER")),
		VoiceASRModel:               strings.TrimSpace(os.Getenv("VOICE_ASR_MODEL")),
		VoiceASRBaseURL:             strings.TrimRight(strings.TrimSpace(os.Getenv("VOICE_ASR_BASE_URL")), "/"),
		VoiceASRAPIKey:              strings.TrimSpace(os.Getenv("VOICE_ASR_API_KEY")),
		Port:                        envOrDefault("PORT", "8080"),
		SessionSecret:               os.Getenv("SESSION_SECRET"),
		GroqAPIKey:                  os.Getenv("GROQ_API_KEY"),
		OpenAIAPIKey:                os.Getenv("OPENAI_API_KEY"),
		PodBaseURL:                  strings.TrimRight(strings.TrimSpace(os.Getenv("POD_BASE_URL")), "/"),
		PodAPIKey:                   strings.TrimSpace(os.Getenv("POD_API_KEY")),
		PodModel:                    strings.TrimSpace(os.Getenv("POD_MODEL")),
		PodReviewModel:              strings.TrimSpace(os.Getenv("POD_REVIEW_MODEL")),
		PodDiscoveryModel:           strings.TrimSpace(os.Getenv("POD_DISCOVERY_MODEL")),
		PodSelectionModel:           strings.TrimSpace(os.Getenv("POD_SELECTION_MODEL")),
		PodWriteModel:               strings.TrimSpace(os.Getenv("POD_WRITE_MODEL")),
		PodLearningReviewModel:      strings.TrimSpace(os.Getenv("POD_LEARNING_REVIEW_MODEL")),
		PodQuestionStudyModel:       strings.TrimSpace(os.Getenv("POD_QUESTION_STUDY_MODEL")),
		PodQuestionStudyReviewModel: strings.TrimSpace(os.Getenv("POD_QUESTION_STUDY_REVIEW_MODEL")),
		PublicURL:                   envOrDefault("PUBLIC_URL", envOrDefault("BASE_URL", "http://localhost:8080")),
	}
	// 统一数据目录（ADR-0010）：DB/evidence/tmp/backups 全部落在 DATA_DIR 之下。
	c.DataDir = envOrDefault("DATA_DIR", envOrDefault("DB_PATH_DIR", "./data"))
	// 兼容旧 DB_PATH / TEMP_DIR 环境变量：未显式设置时使用 DATA_DIR 布局。
	c.DBPath = envOrDefault("DB_PATH", filepath.Join(c.DataDir, "cloudwisepod.db"))
	c.TempDir = envOrDefault("TEMP_DIR", filepath.Join(c.DataDir, "tmp"))
	c.EvidenceDir = filepath.Join(c.DataDir, "evidence")
	c.VoiceDir = filepath.Join(c.DataDir, "voice-notes")
	c.BackupDir = filepath.Join(c.DataDir, "backups")
	c.NarrationDir = envOrDefault("NARRATION_DIR", filepath.Join(c.DataDir, "narrations"))
	c.KokoroBinary = envOrDefault("KOKORO_BINARY", "kokoro")
	c.KokoroVoice = envOrDefault("KOKORO_VOICE", "af_heart")
	c.KokoroModel = os.Getenv("KOKORO_MODEL")
	c.KokoroLanguage = envOrDefault("KOKORO_LANGUAGE", "en")
	// 默认 120s；显式配置仅在合法正整数时覆盖（非法值不静默归零）。
	c.KokoroTimeoutSeconds = 120
	if v, err := strconv.Atoi(os.Getenv("KOKORO_TIMEOUT_SECONDS")); err == nil && v > 0 {
		c.KokoroTimeoutSeconds = v
	}

	if c.SessionSecret == "" {
		return nil, fmt.Errorf("SESSION_SECRET 必须设置")
	}
	if err := c.ValidatePod(); err != nil {
		return nil, err
	}
	if raw := os.Getenv("LEARNING_EMBEDDING_DIMENSIONS"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 2048 {
			return nil, fmt.Errorf("LEARNING_EMBEDDING_DIMENSIONS 必须为1至2048的整数")
		}
		c.EmbeddingDimensions = n
	}
	if err := c.ValidateEmbedding(); err != nil {
		return nil, err
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
	if c.VoiceASRProvider != "" && c.VoiceASRProvider != "groq" && c.VoiceASRProvider != "openai" {
		return nil, fmt.Errorf("VOICE_ASR_PROVIDER 必须是 groq 或 openai")
	}
	if (c.VoiceASRBaseURL == "") != (c.VoiceASRAPIKey == "") {
		return nil, fmt.Errorf("VOICE_ASR_BASE_URL 与 VOICE_ASR_API_KEY 必须成组配置")
	}
	if c.VoiceASRBaseURL != "" {
		u, e := url.Parse(c.VoiceASRBaseURL)
		if e != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return nil, fmt.Errorf("VOICE_ASR_BASE_URL 须为不含认证信息、查询参数或片段的HTTP(S)地址")
		}
	}
	if c.VoiceASRBaseURL != "" && (c.VoiceASRProvider == "" || c.VoiceASRModel == "") {
		return nil, fmt.Errorf("自定义语音连接须明确设置 provider 和 model")
	}
	return c, nil
}

// PublicSchemeIsHTTPS 返回 PUBLIC_URL 是否使用 https（用于 Secure Cookie 判定）。
func (c *Config) PublicSchemeIsHTTPS() bool {
	return strings.HasPrefix(c.PublicURL, "https://")
}

// EnsureDirs 创建 DataDir/EvidenceDir/TempDir/BackupDir。
func (c *Config) EnsureDirs() error {
	for _, d := range []string{c.DataDir, c.EvidenceDir, c.TempDir, c.BackupDir, c.NarrationDir, c.VoiceDir} {
		if d == "" {
			continue
		}
		mode := os.FileMode(0o755)
		if d == c.VoiceDir {
			mode = 0o700
		}
		if err := os.MkdirAll(d, mode); err != nil {
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

// KnowledgeReviewModel chooses the optional review model on the existing POD connection.
func (c *Config) KnowledgeReviewModel() string {
	if c.PodReviewModel != "" {
		return c.PodReviewModel
	}
	return c.PodModel
}

// KnowledgeStageModels resolves optional stage overrides on the POD connection.
// Callers freeze the returned values when admitting a new workflow.
func (c *Config) KnowledgeStageModels() map[string]string {
	fallback := func(v string) string {
		if v != "" {
			return v
		}
		return c.PodModel
	}
	return map[string]string{
		"discover": fallback(c.PodDiscoveryModel), "select": fallback(c.PodSelectionModel),
		"write": fallback(c.PodWriteModel), "revise": fallback(c.PodWriteModel),
		"review": c.KnowledgeReviewModel(), "review_final": c.KnowledgeReviewModel(),
		"weekly_review": fallback(c.PodLearningReviewModel),
	}
}

// ValidateEmbedding never inherits credentials or a route from text/audio providers.
func (c *Config) ValidateEmbedding() error {
	count := 0
	for _, v := range []string{c.EmbeddingBaseURL, c.EmbeddingAPIKey, c.EmbeddingModel} {
		if v != "" {
			count++
		}
	}
	if count == 0 && c.EmbeddingDimensions == 0 {
		return nil
	}
	if count != 3 {
		return fmt.Errorf("LEARNING_EMBEDDING_BASE_URL / API_KEY / MODEL 必须成组配置")
	}
	u, err := url.Parse(c.EmbeddingBaseURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("LEARNING_EMBEDDING_BASE_URL 必须为无认证信息、查询或片段的HTTP(S)地址")
	}
	if strings.ContainsAny(c.EmbeddingAPIKey+c.EmbeddingModel, "\r\n") || len(c.EmbeddingModel) > 200 || c.EmbeddingDimensions < 0 || c.EmbeddingDimensions > 2048 {
		return fmt.Errorf("LEARNING_EMBEDDING 配置无效")
	}
	return nil
}

// QuestionStudyModels resolves the new task's explicit fallback chain only.
func (c *Config) QuestionStudyModels() (generation, review string) {
	generation = c.PodQuestionStudyModel
	if generation == "" {
		generation = c.PodModel
	}
	review = c.PodQuestionStudyReviewModel
	if review == "" {
		review = c.PodReviewModel
	}
	if review == "" {
		review = c.PodModel
	}
	return
}
