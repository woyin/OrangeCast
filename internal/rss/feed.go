package rss

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/breestealth/wisepod/internal/models"
	"github.com/mmcdole/gofeed"
)

var (
	ErrInvalidURL     = errors.New("invalid feed url")
	ErrBlockedAddress = errors.New("feed url resolves to a blocked (private) address")
)

var blockedPrivateNets = []string{
	"10.0.0.0/8",
	"172.16.0.0/12",
	"192.168.0.0/16",
	"127.0.0.0/8",
	"169.254.0.0/16", // 链路本地
	"::1/128",
	"fc00::/7", // IPv6 私有
	"fe80::/10",
}

// safeDialer 自定义 dialer：在连接前校验解析到的 IP 不在私有网段，防 SSRF。
type safeDialer struct {
	inner net.Dialer
}

func (d safeDialer) Dial(network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	ip := net.ParseIP(host)
	if ip == nil {
		// 主机名，先解析
		ips, err := net.LookupIP(host)
		if err != nil || len(ips) == 0 {
			return nil, fmt.Errorf("无法解析主机 %s", host)
		}
		ip = ips[0]
	}
	if isBlockedIP(ip) {
		return nil, ErrBlockedAddress
	}
	return d.inner.Dial(network, net.JoinHostPort(ip.String(), port))
}

func isBlockedIP(ip net.IP) bool {
	for _, cidr := range blockedPrivateNets {
		_, network, err := net.ParseCIDR(cidr)
		if err != nil {
			continue
		}
		if network.Contains(ip) {
			return true
		}
	}
	return ip.IsUnspecified()
}

// ValidateFeedURL 校验 feed URL：必须 http/https，且可解析。
func ValidateFeedURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ErrInvalidURL
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return ErrInvalidURL
	}
	return nil
}

// safeClient 返回带 SSRF 防护、重定向限制、超时的 HTTP 客户端。
func safeClient() *http.Client {
	dialer := safeDialer{inner: net.Dialer{Timeout: 10 * time.Second}}
	return &http.Client{
		Timeout: 30 * time.Second,
		// 自定义 CheckRedirect：限制最多 3 次重定向，每次校验目标。
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return errors.New("超过最大重定向次数")
			}
			return ValidateFeedURL(req.URL.String())
		},
		Transport: &http.Transport{
			Dial:                dialer.Dial,
			MaxIdleConns:        10,
			IdleConnTimeout:     30 * time.Second,
			TLSHandshakeTimeout: 10 * time.Second,
		},
	}
}

const (
	// 播客 feed 常含大量历史单集，大型 feed 可达数 MB。
	// 16MB 足够覆盖主流播客 feed，同时防止恶意超大响应耗尽内存。
	maxFeedSize = 16 << 20 // 16MB
)

// FetchFeed 抓取并解析 RSS feed。含 SSRF 防护 + 大小限制。
// 返回解析后的 podcast 元信息与 episode 列表（episode 的音频 URL 也校验过 http(s)）。
func FetchFeed(feedURL string) (*models.Podcast, []models.Episode, error) {
	if err := ValidateFeedURL(feedURL); err != nil {
		return nil, nil, err
	}
	client := safeClient()
	resp, err := client.Get(feedURL)
	if err != nil {
		return nil, nil, fmt.Errorf("抓取 feed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, nil, fmt.Errorf("feed 返回 HTTP %d", resp.StatusCode)
	}
	body := io.LimitReader(resp.Body, maxFeedSize)

	fp := gofeed.NewParser()
	feed, err := fp.Parse(body)
	if err != nil {
		return nil, nil, fmt.Errorf("解析 feed: %w", err)
	}

	podcast := &models.Podcast{
		FeedURL:     feedURL,
		Title:       feed.Title,
		Description: feed.Description,
	}
	if feed.Image != nil {
		podcast.ImageURL = feed.Image.URL
	}
	var episodes []models.Episode
	for _, item := range feed.Items {
		audioURL := firstEnclosureURL(item)
		if audioURL == "" || !strings.HasPrefix(audioURL, "http") {
			continue // 无音频的条目跳过
		}
		guid := item.GUID
		if guid == "" {
			guid = item.Link
		}
		if guid == "" {
			continue
		}
		ep := models.Episode{
			GUID:       guid,
			Title:      item.Title,
			Description: item.Description,
			AudioURL:   audioURL,
		}
		if item.PublishedParsed != nil {
			t := item.PublishedParsed.UTC().Format(time.RFC3339)
			ep.PublishedAt = &t
		}
		if item.ITunesExt != nil && item.ITunesExt.Duration != "" {
			// duration 解析（秒）留给 worker；此处仅存原始，先简化为 0
		}
		episodes = append(episodes, ep)
	}
	return podcast, episodes, nil
}

func firstEnclosureURL(item *gofeed.Item) string {
	for _, e := range item.Enclosures {
		if strings.HasPrefix(e.Type, "audio") || strings.HasSuffix(strings.ToLower(e.URL), ".mp3") ||
			strings.HasSuffix(strings.ToLower(e.URL), ".m4a") || strings.HasSuffix(strings.ToLower(e.URL), ".wav") {
			return e.URL
		}
	}
	return ""
}
