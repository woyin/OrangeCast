-- C03：材料诊断关联构思轮次（输出引用），支持轮次级任务恢复与审计。
ALTER TABLE material_diagnoses ADD COLUMN ideation_round_id TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_material_diagnoses_round ON material_diagnoses(ideation_round_id);
