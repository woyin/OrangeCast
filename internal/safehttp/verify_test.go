package safehttp

import (
	"context"
	"net/http"
	"testing"
	"time"
)

// TestRedirectChain_PodtracStyle 验证可跟随 3 跳以上的重定向链（Podtrac 等播客投递链）。
// 需要外网；失败时跳过而非阻塞 CI。
func TestRedirectChain_PodtracStyle(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	url := "https://dts.podtrac.com/redirect.mp3/tracking.swap.fm/track/zA4xtlPBvf2K1K9zesjz/media.transistor.fm/550311e4/c15607da.mp3"
	client := NewClient(5, 0, 20*time.Second)
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("follow redirects: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
}
