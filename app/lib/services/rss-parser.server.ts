export interface ParsedPodcast {
  title: string;
  description: string | null;
  siteUrl: string | null;
  imageUrl: string | null;
  episodes: ParsedEpisode[];
}

export interface ParsedEpisode {
  guid: string;
  title: string;
  description: string | null;
  audioUrl: string;
  durationSeconds: number | null;
  publishedAt: string | null;
}

function decodeXml(value: string): string {
  return value
    .replace(/<!\[CDATA\[([\s\S]*?)\]\]>/g, "$1")
    .replace(/&amp;/g, "&")
    .replace(/&lt;/g, "<")
    .replace(/&gt;/g, ">")
    .replace(/&quot;/g, '"')
    .replace(/&#39;/g, "'")
    .replace(/&apos;/g, "'")
    .replace(/&#(\d+);/g, (_match, code: string) => String.fromCodePoint(Number(code)))
    .replace(/&#x([0-9a-fA-F]+);/g, (_match, code: string) =>
      String.fromCodePoint(Number.parseInt(code, 16)),
    );
}

function stripTags(value: string): string {
  return value.replace(/<[^>]+>/g, "");
}

function textOf(xml: string, tagName: string): string | null {
  const pattern = new RegExp(`<${tagName}(?:\\s[^>]*)?>([\\s\\S]*?)<\\/${tagName}>`, "i");
  const match = xml.match(pattern);
  if (!match) return null;
  const text = stripTags(decodeXml(match[1])).trim();
  return text.length > 0 ? text : null;
}

function blocksOf(xml: string, tagName: string): string[] {
  return Array.from(
    xml.matchAll(new RegExp(`<${tagName}(?:\\s[^>]*)?>[\\s\\S]*?<\\/${tagName}>`, "gi")),
    (match) => match[0],
  );
}

function selfClosingTag(xml: string, tagName: string): string | null {
  const match = xml.match(new RegExp(`<${tagName}(?:\\s[^>]*)?\\/>`, "i"));
  return match?.[0] ?? null;
}

function attrOf(tag: string, attrName: string): string | null {
  const match = tag.match(new RegExp(`\\s${attrName}=["']([^"']*)["']`, "i"));
  return match ? decodeXml(match[1]).trim() || null : null;
}

function channelBlock(xml: string): string {
  const channel = xml.match(/<channel(?:\s[^>]*)?>([\s\S]*?)<\/channel>/i)?.[1];
  if (!channel) throw new Error("RSS feed is missing a channel");
  return channel;
}

function withoutItemBlocks(channel: string): string {
  return channel.replace(/<item(?:\s[^>]*)?>[\s\S]*?<\/item>/gi, "");
}

function parseDate(value: string | null): string | null {
  if (!value) return null;
  const time = Date.parse(value);
  return Number.isNaN(time) ? null : new Date(time).toISOString();
}

function parseDuration(value: string | null): number | null {
  if (!value) return null;
  const trimmed = value.trim();
  if (/^\d+$/.test(trimmed)) return Number(trimmed);

  const parts = trimmed.split(":").map((part) => Number(part));
  if (parts.some((part) => !Number.isFinite(part))) return null;

  if (parts.length === 2) return parts[0] * 60 + parts[1];
  if (parts.length === 3) return parts[0] * 3600 + parts[1] * 60 + parts[2];
  return null;
}

function podcastImageUrl(channel: string): string | null {
  const itunesImage = selfClosingTag(channel, "itunes:image");
  if (itunesImage) return attrOf(itunesImage, "href");

  const imageBlock = channel.match(/<image(?:\s[^>]*)?>([\s\S]*?)<\/image>/i)?.[1];
  return imageBlock ? textOf(imageBlock, "url") : null;
}

function episodeAudioUrl(item: string): string | null {
  for (const match of item.matchAll(/<enclosure(?:\s[^>]*)?\/?>/gi)) {
    const tag = match[0];
    const url = attrOf(tag, "url");
    const type = attrOf(tag, "type");
    if (url && (!type || type.toLowerCase().startsWith("audio/"))) return url;
  }
  return null;
}

export function parsePodcastRss(xml: string): ParsedPodcast {
  const channel = channelBlock(xml);
  const channelMetadata = withoutItemBlocks(channel);
  const title = textOf(channelMetadata, "title");
  if (!title) throw new Error("RSS feed is missing a podcast title");

  const episodes = blocksOf(channel, "item")
    .map((item): ParsedEpisode | null => {
      const audioUrl = episodeAudioUrl(item);
      if (!audioUrl) return null;

      const title = textOf(item, "title");
      const guid = textOf(item, "guid") ?? audioUrl;
      if (!title) throw new Error("RSS item is missing an episode title");

      return {
        guid,
        title,
        description: textOf(item, "description"),
        audioUrl,
        durationSeconds: parseDuration(textOf(item, "itunes:duration")),
        publishedAt: parseDate(textOf(item, "pubDate")),
      };
    })
    .filter((episode): episode is ParsedEpisode => episode !== null);

  if (episodes.length === 0) {
    throw new Error("RSS feed has no playable audio episodes");
  }

  return {
    title,
    description: textOf(channelMetadata, "description"),
    siteUrl: textOf(channelMetadata, "link"),
    imageUrl: podcastImageUrl(channelMetadata),
    episodes,
  };
}
