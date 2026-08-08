-- Migration 0015: 修复现有数据的 Highlight current 指针（ADR-0019 实施时发现）
--
-- 背景：SetCurrentVersion 对 KindHighlight 的列名映射此前是坏的（落到 current_card_version
-- 而非 current_highlight_version），已于 Phase A 修正代码。但用旧 buggy 代码处理的现有
-- episode 从未正确写入 current_highlight_version（恒为 NULL），导致修正后的代码读不到
-- 当前 highlight 版本（DJ 页面 404）。
--
-- 修复：对每个存在 highlight ArtifactVersion 的 source，把 current_highlight_version
-- 设为该 source 下 highlight 的 MAX(version)。current_card_version 不动（它本就指向 card，
-- 即使曾被旧 bug 错写，值也与 card 的 version 一致——因为 card 与 highlight 在各自序列
-- 都从 1 开始，现有数据恰好都是 1）。

-- episodes
UPDATE episodes
SET current_highlight_version = (
    SELECT MAX(version) FROM artifact_versions av
    WHERE av.source_type = 'episode' AND av.source_id = episodes.id AND av.kind = 'highlight'
)
WHERE EXISTS (
    SELECT 1 FROM artifact_versions av
    WHERE av.source_type = 'episode' AND av.source_id = episodes.id AND av.kind = 'highlight'
)
AND current_highlight_version IS NULL;

-- uploads
UPDATE uploads
SET current_highlight_version = (
    SELECT MAX(version) FROM artifact_versions av
    WHERE av.source_type = 'upload' AND av.source_id = uploads.id AND av.kind = 'highlight'
)
WHERE EXISTS (
    SELECT 1 FROM artifact_versions av
    WHERE av.source_type = 'upload' AND av.source_id = uploads.id AND av.kind = 'highlight'
)
AND current_highlight_version IS NULL;
