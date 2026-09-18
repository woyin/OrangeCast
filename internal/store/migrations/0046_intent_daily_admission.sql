-- R03（B04/B08）：自动日限额按执行资格计数。
-- processing_jobs.intent_admitted_at 记录该任务意图获得执行资格（占日限额名额）的
-- 时刻：入队不占额，worker 领取时原子准入；同任务重试不重复计数（列非空即不再写）。
-- 限额统计对象是"当日已获准的自动处理意图"（按来源去重），后续分析/解说子任务
-- 与同一来源的重试不重复占名额。NULL = 未获准（排队中或曾被额度拒绝）。旧行默认 NULL。

ALTER TABLE processing_jobs ADD COLUMN intent_admitted_at TEXT;
