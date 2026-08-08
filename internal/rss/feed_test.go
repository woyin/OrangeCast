package rss

import (
	"net"
	"testing"

	"github.com/mmcdole/gofeed"
	"github.com/woyin/orangecast/internal/safehttp"
)

func TestValidateFeedURL(t *testing.T) {
	good := []string{"https://feed.example.com/x.xml", "http://localhost/feed"}
	for _, u := range good {
		if safehttp.ValidateURL(u) != nil {
			t.Errorf("%q 应通过校验", u)
		}
	}
	bad := []string{"ftp://x.com/feed", "file:///etc/passwd", "javascript:alert(1)", "://no-scheme"}
	for _, u := range bad {
		if safehttp.ValidateURL(u) == nil {
			t.Errorf("%q 应被拒绝", u)
		}
	}
}

func TestIsBlockedIP_PrivateAddresses(t *testing.T) {
	blocked := []string{"127.0.0.1", "10.0.0.1", "172.16.5.5", "192.168.1.1", "169.254.1.1", "::1", "fc00::1", "fe80::1", "0.0.0.0"}
	for _, ip := range blocked {
		if !safehttp.IsBlockedIP(net.ParseIP(ip)) {
			t.Errorf("%s 应被 SSRF 防护拦截", ip)
		}
	}
}

func TestIsBlockedIP_PublicAllowed(t *testing.T) {
	public := []string{"8.8.8.8", "1.1.1.1", "203.0.113.5"}
	for _, ip := range public {
		if safehttp.IsBlockedIP(net.ParseIP(ip)) {
			t.Errorf("%s 是公网 IP，不应被拦截", ip)
		}
	}
}

const sampleRSS = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0">
  <channel>
    <title>测试播客</title>
    <description>一个用于测试的播客</description>
    <item>
      <guid>ep-001</guid>
      <title>第一集</title>
      <description>第一集描述</description>
      <enclosure url="https://cdn.example.com/1.mp3" type="audio/mpeg" length="1024"/>
    </item>
    <item>
      <guid>ep-002</guid>
      <title>第二集</title>
      <description>第二集描述</description>
      <enclosure url="https://cdn.example.com/2.m4a" type="audio/mp4" length="2048"/>
    </item>
    <item>
      <guid/>
      <link>https://example.com/no-enclosure</link>
      <title>无音频条目</title>
      <description>没有音频应被跳过</description>
    </item>
  </channel>
</rss>`

// TestParseFeed 验证 parseFeed 把 gofeed.Feed 转换为领域模型：
// 播客元信息映射、含音频的条目保留、无音频条目跳过、GUID 回退 Link。
func TestParseFeed(t *testing.T) {
	fp := gofeed.NewParser()
	feed, err := fp.ParseString(sampleRSS)
	if err != nil {
		t.Fatalf("解析样例 RSS 失败: %v", err)
	}

	podcast, episodes, err := parseFeed(feed, "https://feed.example.com/pod.xml")
	if err != nil {
		t.Fatalf("parseFeed 失败: %v", err)
	}
	if podcast.Title != "测试播客" {
		t.Errorf("Title = %q, want 测试播客", podcast.Title)
	}
	if podcast.Description != "一个用于测试的播客" {
		t.Errorf("Description 不匹配: %q", podcast.Description)
	}
	if podcast.FeedURL != "https://feed.example.com/pod.xml" {
		t.Errorf("FeedURL = %q", podcast.FeedURL)
	}

	// 3 个条目，其中 2 个有音频，1 个无音频被跳过。
	if len(episodes) != 2 {
		t.Fatalf("应保留 2 个 episode，实际 %d", len(episodes))
	}
	if episodes[0].GUID != "ep-001" || episodes[0].Title != "第一集" {
		t.Errorf("episodes[0] 不匹配: %+v", episodes[0])
	}
	if episodes[0].AudioURL != "https://cdn.example.com/1.mp3" {
		t.Errorf("episodes[0].AudioURL = %q", episodes[0].AudioURL)
	}
	if episodes[1].AudioURL != "https://cdn.example.com/2.m4a" {
		t.Errorf("episodes[1].AudioURL = %q", episodes[1].AudioURL)
	}
}

// TestParseFeed_GUIDFallbackToLink 验证 GUID 为空时回退到 item.Link。
func TestParseFeed_GUIDFallbackToLink(t *testing.T) {
	xml := `<?xml version="1.0"?>
<rss version="2.0"><channel>
  <item>
    <link>https://example.com/ep</link>
    <enclosure url="https://cdn.example.com/a.mp3" type="audio/mpeg"/>
  </item>
</channel></rss>`
	fp := gofeed.NewParser()
	feed, err := fp.ParseString(xml)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	_, eps, err := parseFeed(feed, "https://feed.example.com/x")
	if err != nil {
		t.Fatalf("parseFeed 失败: %v", err)
	}
	if len(eps) != 1 || eps[0].GUID != "https://example.com/ep" {
		t.Fatalf("GUID 应回退 Link，实际 %+v", eps)
	}
}

// TestFirstEnclosureURL 验证只接受 audio 类型或常见音频扩展名的 enclosure，
// 以及 item 无 enclosure 或类型不匹配时返回空串。
func TestFirstEnclosureURL(t *testing.T) {
	item := &gofeed.Item{Enclosures: []*gofeed.Enclosure{
		{URL: "https://cdn.example.com/skip.pdf", Type: "application/pdf"},
		{URL: "https://cdn.example.com/pod.wav", Type: "audio/x-wav"},
	}}
	if got := firstEnclosureURL(item); got != "https://cdn.example.com/pod.wav" {
		t.Errorf("应选中 wav 音频，实际 %q", got)
	}

	noAudio := &gofeed.Item{Enclosures: []*gofeed.Enclosure{
		{URL: "https://cdn.example.com/skip.pdf", Type: "application/pdf"},
	}}
	if got := firstEnclosureURL(noAudio); got != "" {
		t.Errorf("无音频应返回空串，实际 %q", got)
	}

	empty := &gofeed.Item{}
	if got := firstEnclosureURL(empty); got != "" {
		t.Errorf("空 item 应返回空串，实际 %q", got)
	}

	// 大写扩展名也应按后缀识别（inside 大小写归一化）。
	upper := &gofeed.Item{Enclosures: []*gofeed.Enclosure{
		{URL: "https://cdn.example.com/pod.MP3", Type: "audio/mpeg"},
	}}
	if got := firstEnclosureURL(upper); got != "https://cdn.example.com/pod.MP3" {
		t.Errorf("大写 .MP3 后缀应识别，实际 %q", got)
	}
}
