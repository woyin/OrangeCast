-- R14（C04 / ADR-0024 §6）：研究缺口以具体、版本化、Owner 确认的依据解决。
-- resolution_source_type / resolution_version / resolution_detail 记录依据的
-- 来源类型、当前版本与具体材料位置（Segment/文档段落）；resolution_owner_confirmed
-- 标记 Owner 认定动作（区别于"机器证明事实为真"）；resolution_invalidated 标记
-- 依据被删除/失效后重新阻断（审计保留原依据字段）。

ALTER TABLE research_needs ADD COLUMN resolution_source_type TEXT NOT NULL DEFAULT '';
ALTER TABLE research_needs ADD COLUMN resolution_version INTEGER NOT NULL DEFAULT 0;
ALTER TABLE research_needs ADD COLUMN resolution_detail TEXT NOT NULL DEFAULT '';
ALTER TABLE research_needs ADD COLUMN resolution_owner_confirmed INTEGER NOT NULL DEFAULT 0;
ALTER TABLE research_needs ADD COLUMN resolution_invalidated INTEGER NOT NULL DEFAULT 0;
