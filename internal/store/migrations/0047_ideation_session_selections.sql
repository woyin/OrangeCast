-- R12（C02 / ADR-0024 §6）：构思会话绑定持久化素材选择。
-- ideation_sessions.selections_json 保存会话创建时选定的 CreationSelection ID 列表：
-- 轮次材料快照从选择的当前内容与版本冻结（重点+个人笔记），资格变化显式反馈。

ALTER TABLE ideation_sessions ADD COLUMN selections_json TEXT NOT NULL DEFAULT '[]';
