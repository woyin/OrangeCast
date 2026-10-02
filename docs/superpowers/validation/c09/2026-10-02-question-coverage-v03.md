# V03 问答覆盖补强

仅新增独立测试，不修改生产源码或覆盖地板：

- `internal/store/question_study_coverage_v4_test.go`：7 父 / 15 子；命令/config/UUID 校验、远端调用 CAS、冻结 checkpoint CAS、receipt 未知用量与实际模型、两侧冻结估价、唯一性、账本故障、已知/未知显式恢复、错误 checkpoint/绑定、事务失败不留下轮次/付费任务、生成采用与 successor 回滚、材料变化拒绝、关闭数据库显错。
- `internal/server/question_study_coverage_v4_test.go`：2 父；真实 auth/CSRF/router/worker/HTTP 模型协议桩，4 类回答及 Owner 理解渲染、冻结引用、JSON 状态、已知响应 receipt 故障的 HTTP 恢复及命令重放、失败草稿保留、未带 CSRF 变更仍 403。405 方法契约另直接测 handler，不宣称其绕过认证成功。
- `internal/queue/question_study_coverage_v4_test.go`：5 父 / 16 子；合法手工重点真实 CardVersion=0/OwnerConfirmed 的完整提交和生成+检查；正文存储变化/Owner dismiss 后零 dispatch；错误冻结身份/估价/断点；响应保存失败及账本同时失败；无连接、未知远端结果与未知标记持久失败；已知不合格模型/损坏审校响应保 receipt 不采用；审校已知恢复沿用原付费身份，无新调用。

手工重点没有公开正文编辑 Store 方法；content 子场景明确修改 authoritative row 及其 search projection 来模拟已存储正文变化，未以自动 card 或虚构版本 1 替代手工重点，也未宣称人工编辑 UI 通过。

实际命令：

```sh
go test ./internal/store ./internal/server ./internal/queue \
  -run '^TestQuestionStudyCoverageV4' -race -count=1 \
  -coverprofile=/tmp/c09-coverage-added-final.out -json -timeout=5m

go test ./internal/queue -run '^TestQuestionStudyCoverageV4' \
  -race -count=1 -coverprofile=/tmp/c09-queue-added.out -json -timeout=4m
```

首命令通过 store 7 / server 2 / queue 当时 1 父；后命令在全部 queue 用例落稳后通过 queue 5 父 / 16 子。去重实际覆盖新 14 父 / 31 子。没有 skip 或空匹配计入通过。

与协调者提供的 `/tmp/cwp-v4-coverage.out` 按相同文件/区块范围和语句数匹配的布尔 union，仅用于增量分析：

| 文件 | 原命中 | 新测试与原证据 union | 原未命中→剩余 |
|---|---:|---:|---:|
| store/question_study_jobs.go | 140/397，35.3% | 287/397，72.3% | 257→110 |
| server/question_study.go | 61/155，39.4% | 129/155，83.2% | 94→26 |
| queue/question_study.go | 57/71，80.3% | 69/71，97.2% | 14→2 |

union 不等于当前全库完整覆盖门禁；最终须以所有当前测试和当前源码运行原 `make cover-gate` 为准，不修改 provider/queue 95% 与 store/server 原地板。queue 剩余两语句为不可序列化错误返回与生成 JSON 解析错误返回；没有通过改源码、降地板或制造空成功来抹掉遗漏。
