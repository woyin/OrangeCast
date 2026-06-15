export function transcriptTextKey(userId: string, sourceType: string, sourceId: string) {
  return `users/${userId}/transcripts/${sourceType}/${sourceId}/text.txt`;
}

export function transcriptSegmentsKey(userId: string, sourceType: string, sourceId: string) {
  return `users/${userId}/transcripts/${sourceType}/${sourceId}/segments.json`;
}

export function analysisJsonKey(userId: string, sourceType: string, sourceId: string) {
  return `users/${userId}/analyses/${sourceType}/${sourceId}/content.json`;
}

export function analysisMarkdownKey(userId: string, sourceType: string, sourceId: string) {
  return `users/${userId}/analyses/${sourceType}/${sourceId}/note.md`;
}
