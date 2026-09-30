package config

import (
	"bufio"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"
)

var environmentName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// LoadEnvironmentFile loads literal dotenv assignments without executing shell
// code. Existing process variables take precedence; an absent file is allowed.
func LoadEnvironmentFile(path string) error {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		name, value, ok := strings.Cut(line, "=")
		name, value = strings.TrimSpace(name), strings.TrimSpace(value)
		if !ok || !environmentName.MatchString(name) {
			return fmt.Errorf("环境文件第 %d 行格式错误", lineNo)
		}
		if strings.HasPrefix(value, `"`) || strings.HasPrefix(value, "'") {
			quote := value[0]
			end := strings.LastIndexByte(value[1:], quote)
			if end < 0 {
				return fmt.Errorf("环境文件第 %d 行引号未闭合", lineNo)
			}
			end++
			remaining := strings.TrimSpace(value[end+1:])
			if remaining != "" && !strings.HasPrefix(remaining, "#") {
				return fmt.Errorf("环境文件第 %d 行引号后有非法内容", lineNo)
			}
			value = value[1:end]
		} else if i := strings.Index(value, " #"); i >= 0 {
			value = strings.TrimSpace(value[:i])
		}
		if _, exists := os.LookupEnv(name); !exists {
			if err := os.Setenv(name, value); err != nil {
				return err
			}
		}
	}
	return scanner.Err()
}

// ValidatePod validates the complete optional automatic-article connection.
func (c *Config) ValidatePod() error {
	if c.PodBaseURL == "" && c.PodAPIKey == "" && c.PodModel == "" {
		return nil
	}
	if c.PodBaseURL == "" || c.PodAPIKey == "" || c.PodModel == "" {
		return fmt.Errorf("自动知识文章需要同时配置 POD_BASE_URL、POD_API_KEY 和 POD_MODEL")
	}
	u, err := url.Parse(c.PodBaseURL)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("POD_BASE_URL 必须是有效的 HTTP(S) API 基础地址")
	}
	return nil
}

// PodAvailable reports whether the dedicated text connection is configured.
func (c *Config) PodAvailable() bool {
	return c.PodBaseURL != "" && c.PodAPIKey != "" && c.PodModel != ""
}
