import { describe, expect, it } from "vitest";
import { parsePodcastRss } from "../app/lib/services/rss-parser.server";

const fixture = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0">
  <channel>
    <title>Example Podcast</title>
    <description>Podcast description</description>
    <link>https://example.com</link>
    <item>
      <guid>episode-1</guid>
      <title>Episode One</title>
      <description>Episode description</description>
      <pubDate>Mon, 01 Jan 2024 12:00:00 GMT</pubDate>
      <enclosure url="https://example.com/a.mp3" type="audio/mpeg" />
    </item>
  </channel>
</rss>`;

describe("parsePodcastRss", () => {
  it("returns podcast and playable episode metadata from RSS", () => {
    const podcast = parsePodcastRss(fixture);

    expect(podcast.title).toBe("Example Podcast");
    expect(podcast.siteUrl).toBe("https://example.com");
    expect(podcast.episodes).toHaveLength(1);
    expect(podcast.episodes[0]).toMatchObject({
      guid: "episode-1",
      title: "Episode One",
      audioUrl: "https://example.com/a.mp3",
      publishedAt: "2024-01-01T12:00:00.000Z",
    });
  });
});
