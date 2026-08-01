-- Migration 0006: 分段级全文搜索（Roadmap Phase 5）
--
-- 现状：search_index 按 Source 粒度索引，搜索结果只返回不可定位的摘要片段。
-- 目标：搜索结果返回实际命中的 Transcript Segment（含时间范围），可跳转 EvidenceAudio。
--
-- 变更：重建 search_index 为分段粒度：
--   - 每个 Transcript Segment 一行（segment_id/start/end + body=段文本）。
--   - 每个 Source 一行 Summary（segment_id=''，body=卡片摘要）供标题级检索。
-- 旧表行迁入为 segment_id='' 的占位行（历史兼容，可由 reindex 覆盖）。

CREATE VIRTUAL TABLE search_index_new USING fts5(
    source_type UNINDEXED,
    source_id UNINDEXED,
    segment_id UNINDEXED,
    start UNINDEXED,
    end UNINDEXED,
    title,
    body,
    tokenize = 'unicode61'
);

INSERT INTO search_index_new (source_type, source_id, segment_id, start, end, title, body)
SELECT source_type, source_id, '', 0, 0, title, body FROM search_index;

DROP TABLE search_index;
ALTER TABLE search_index_new RENAME TO search_index;
