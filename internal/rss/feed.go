package rss

import (
	"fmt"
	"strings"
	"time"

	"github.com/mmcdole/gofeed"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/safehttp"
)

const maxFeedSize = 16 << 20 // 16MB

// feedClient 共享安全 HTTP 客户端（SSRF 防护 + 重定向限制 + 超时），
// 与 Episode 音频下载复用同一客户端（ADR-0013）。
var feedClient = safehttp.NewClient(5, maxFeedSize, 30*time.Second)

// FetchFeed 抓取并解析 RSS feed。含 SSRF 防护 + 大小限制。
// 返回解析后的 podcast 元信息与 episode 列表（episode 的音频 URL 也校验过 http(s)）。
func FetchFeed(feedURL string) (*models.Podcast, []models.Episode, error) {
	if err := safehttp.ValidateURL(feedURL); err != nil {
		return nil, nil, err
	}
	resp, err := feedClient.Get(feedURL)
	if err != nil {
		return nil, nil, fmt.Errorf("抓取 feed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, nil, fmt.Errorf("feed 返回 HTTP %d", resp.StatusCode)
	}
	body := safehttp.LimitBody(resp.Body, maxFeedSize)

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
			GUID:        guid,
			Title:       item.Title,
			Description: item.Description,
			AudioURL:    audioURL,
		}
		if item.PublishedParsed != nil {
			t := item.PublishedParsed.UTC().Format(time.RFC3339)
			ep.PublishedAt = &t
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
