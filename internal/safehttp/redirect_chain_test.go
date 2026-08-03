package safehttp

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"
)

// TestRedirectChain_FullTrackingChain 验证播客完整追踪链（mgln.ai → op3 → pscrb → podtrac → swap → transistor）可被跟随。
func TestRedirectChain_FullTrackingChain(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
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
