-- B05（ADR-0024 §2）：订阅处理深度。
-- IngestionPolicy 回答"哪些新集处理"；processing_depth 回答"处理到哪里"：
--   knowledge      转录 + 知识卡片 + 关键观点（默认，等价旧自动路径）
--   knowledge_dj   另加高光、解说与 DJ 播放清单准备（Owner 显式开启）
-- 旧订阅回填为 knowledge（列默认值），行为不变；深度只影响之后入队的新意图，
-- 已排队任务使用入队时的 input_snapshot_json 快照（0027）。

ALTER TABLE podcasts ADD COLUMN processing_depth TEXT NOT NULL DEFAULT 'knowledge';
