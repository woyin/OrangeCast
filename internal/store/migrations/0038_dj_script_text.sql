-- D04：串场解说词随清单项持久化（确定性模板文本，可审计、可重合成）。
ALTER TABLE dj_plan_items ADD COLUMN script_kind TEXT NOT NULL DEFAULT '';
ALTER TABLE dj_plan_items ADD COLUMN script_text TEXT NOT NULL DEFAULT '';
