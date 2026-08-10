package safehttp

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"
)

// TestRedirectChain_FullTrackingChain 验证播客完整追踪链（mgln.ai → op3 → pscrb → podtrac → swap → transistor）可被跟随。
func TestRedirectChain_FullTrackingChain(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	// 这是外部真实网络集成测试：离线/网络不稳定时跳过而非失败，
	// 避免 CI 在无外网环境因 TLS 握手超时误报。
	if os.Getenv("CWP_NETWORK_TEST") == "" {
		t.Skip("设置 CWP_NETWORK_TEST=1 启用外部追踪链集成测试")
	}
	url := "https://mgln.ai/e/p155759/op3.dev/e/pscrb.fm/rss/p/dts.podtrac.com/redirect.mp3/tracking.swap.fm/track/zA4xtlPBvf2K1K9zesjz/media.transistor.fm/550311e4/c15607da.mp3"
	client := NewClient(10, 500<<20, 15*time.Minute)
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("完整追踪链下载失败: %v", err)
	}
	defer resp.Body.Close()
	fmt.Printf("final status=%d url=%s\n", resp.StatusCode, resp.Request.URL.String())
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
}
