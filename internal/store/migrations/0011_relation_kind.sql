-- Migration 0011: 引入 relation_kind，显式区分 Citation 与 Reference（ADR-0018 R1）
--
-- 背景：Citation 与 Reference 语义不同且不可互换（Citation 可逐字核验，Reference 仅表示参考）。
-- 升级前，所有承载 Segment 关系的位置都是 Citation 语义但没有显式标记，互斥铁律只活在文档里。
-- 本次为每个承载 Segment 关系的列旁增加 relation_kind 列，显式标注语义：
--   'citation'  = 可核验的强关系（CitedDerivative）
--   'reference' = 仅表示参考的弱关系（GeneratedDerivative）
-- 存量数据全部回填 'citation'（现有 keypoint_index/annotations/pins/collections 均为证据型）。
-- 宿主实体类别与 relation_kind 的配对校验在 Go 写入层执行（CitedDerivative→citation，GeneratedDerivative→reference）。
--
-- 注意：relation_kind 与 segment_ids/citations_json 并存，不改变现有列格式，
-- 也不影响 collection_items 与 keypoint_index 之间基于 citations_json = segment_ids 的 JOIN。

ALTER TABLE keypoint_index ADD COLUMN relation_kind TEXT NOT NULL DEFAULT 'citation';
ALTER TABLE annotations      ADD COLUMN relation_kind TEXT NOT NULL DEFAULT 'citation';
ALTER TABLE pins             ADD COLUMN relation_kind TEXT NOT NULL DEFAULT 'citation';
ALTER TABLE collection_items ADD COLUMN relation_kind TEXT NOT NULL DEFAULT 'citation';
