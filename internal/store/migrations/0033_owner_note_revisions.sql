-- K04（ADR-0024 §5）：个人笔记编辑与乐观并发控制。
-- revision 随每次编辑递增；更新必须携带期望 revision，过期编辑返回冲突。
-- 历史精读/文章在生成时复制笔记文本与版本，不随后续编辑改变。

ALTER TABLE owner_notes ADD COLUMN revision INTEGER NOT NULL DEFAULT 1;
