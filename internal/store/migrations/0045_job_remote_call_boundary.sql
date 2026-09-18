-- R02-b（B02 / ADR-0024 §5）：任务的持久远端调用边界。
-- processing_jobs.remote_call_started 记录"已到达远端模型调用边界"：
-- 失败收尾据此区分"调用前失败"（released_no_call）与"远端结果未知"
-- （pending_remote 保留待处理）；不使用进程内存状态，也不凭 receipt 反推。
-- 旧行默认 0（未到达调用边界），旧任务兼容。

ALTER TABLE processing_jobs ADD COLUMN remote_call_started INTEGER NOT NULL DEFAULT 0;
