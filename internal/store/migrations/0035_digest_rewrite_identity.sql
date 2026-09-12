-- G06：渠道改写独立任务与输入身份。
-- input_hash = 已通过门禁的块集合指纹；主文修订变化 → hash 变化 → 不沿用旧渠道结果。
ALTER TABLE digest_rewrites ADD COLUMN input_hash TEXT NOT NULL DEFAULT '';
ALTER TABLE digest_rewrites ADD COLUMN digest_version INTEGER NOT NULL DEFAULT 0;
