# CloudWisePod Handoff 手册

更新日期：2026-08-01  
适用对象：接手项目的开发者或 Coding Agent  
当前方向：Go、自托管、单 Owner、Evidence-first

## 先读这三份文档

1. [`product-goal.md`](product-goal.md)：产品要解决什么问题、做什么和不做什么。
2. [`../CONTEXT.md`](../CONTEXT.md)：必须统一使用的领域词汇。
3. [`implementation-roadmap.md`](implementation-roadmap.md)：按依赖排序的实施阶段与退出条件。

随后阅读 [`adr/`](adr/) 中未被取代的决策。旧 Cloudflare 设计和计划已经标记为历史文档，不能作为实现依据。

## 30 秒摘要

CloudWisePod 已完成 Roadmap Phase 0–7（2026-08-01）：单 Owner、自托管、Evidence-first 的播客证据库。Go + SQLite + SSR 单二进制，具备 RSS、上传、Groq 转录、不可变 ArtifactVersion、真实 Citation 的知识卡片、持久 EvidenceAudio、可恢复 SQLite 任务队列、分段级全文搜索、确定性 Markdown 下载、备份/恢复 CLI、CSRF/限流/SSRF 公网安全基线。

自动化验证全部通过（`go test ./...` / `go vet ./...` / `go build ./cmd/cloudwisepod` / `git diff --check`）。发布前仍需：真实 Groq key 跑通约 60 分钟黄金旅程（含处理中重启与跨实例恢复）、EvalSet 人工评分。

## 仓库基线

- 审计时分支：`main`
- 审计时提交：`743ab96`
- 发布镜像：`ghcr.io/woyin/orangecast`
- 产品名称：CloudWisePod
- Go 入口：`cmd/cloudwisepod/main.go`
- 现行 schema：`internal/store/schema.sql`
- HTTP 路由：`internal/server/server.go`
- 处理 worker：`internal/queue/worker.go`

Go module 与发布镜像均对齐到实际仓库 `woyin/orangecast`（Phase 0 已完成）：模块路径 `github.com/woyin/orangecast`、镜像 `ghcr.io/woyin/orangecast`。产品对外名称仍为 CloudWisePod；仓库/镜像保留 `orangecast` 命名，若未来要统一为 `cloudwisepod` 需在 GitHub 重命名仓库（会改变 GHCR 镜像地址）。

## 当前实现与目标差距

| 领域 | 已实现（Phase 0–7） |
|---|---|
| Owner | `ClaimOwner` 原子认领；第二次注册被拒绝；`/register` 已认领时重定向登录 |
| 数据归属 | 内容表/接口不再携带 `user_id`（迁移 0002）；仅凭据保留单例 Owner |
| Provider | 移除全局 `active_provider`（迁移 0003）；Groq 默认零成本；付费按单次任务显式授权（Phase 4 记录于 ArtifactVersion） |
| ProcessingJob | SQLite 驱动 worker：租约 + 心跳 + 启动恢复 + 至少一次；防重复领取测试 |
| 音频 | 每个 Source 持久保存标准化 EvidenceAudio（SHA256 校验）；播放只依赖它 |
| Transcript/Card | 不可变 `artifact_versions`，可查看与回退；Source 显式指向当前版本 |
| Citation | 模型只返回 Segment ID；程序解析时间范围；金句逐字校验；无效项省略/拒绝 |
| Q&A | 只使用模型实际引用的 Segment；无可靠引用 422 拒答；不进入 KnowledgeNote |
| Search | 分段级 FTS，命中返回实际 Segment + 时间；`?t=`/`#seg-` 深链跳转 |
| Markdown | 确定性 Obsidian Markdown（frontmatter + Citation 链接），单 Source 下载 |
| Purge | 两阶段可恢复删除（intent → 文件 → DB 事务），重启可续 |
| Backup | `cloudwisepod backup|restore`：manifest + DB/证据 SHA256 + tar.gz，全新目录 E2E 恢复测试 |
| Web 安全 | CSRF、登录限流、可信代理 + `PUBLIC_URL` Secure Cookie、RSS/音频共享 SSRF 防护客户端 |

## 最容易踩错的地方

### 1. 现有 schema 不是迁移系统

`store.Open` 每次只执行嵌入的 `CREATE IF NOT EXISTS`。修改 `CREATE TABLE` 不会改变已有表，也无法安全移除 `user_id`、增加版本关系或回填数据。

在任何 schema 重构前：

1. 建立有序迁移记录。
2. 准备真实 v0.1 SQLite fixture。
3. 生成一致性备份。
4. 在事务中迁移并校验行数与关联。
5. 对已有多个 User 的数据库停止自动迁移，要求显式选择 Owner。

### 2. 不要复用当前 goroutine 语义冒充持久队列

`Worker.Process` 只启动 goroutine。进程重启后没有扫描 queued/running job；`running` 也没有 lease 或 stale 判定。新增启动扫描之前必须先设计重复领取和崩溃点下的幂等性，否则恢复机制会制造重复 Provider 调用。

### 3. EvidenceAudio 不是 cache

当前 `fetchAudio` 会转码后删除输出，Episode 播放继续使用 RSS 外链。目标要求标准化音频成为 EvidenceArchive 的永久组成部分。只有持久文件成功落盘、同步并记录后，才能删除原始输入。

### 4. 模型不能生成可信时间戳

当前 AnalysisProvider 接收 `Transcript.PlainText`，Prompt 却要求模型估算章节时间。下一版本必须给 Segment 稳定标识，让模型选择标识，再由程序计算 Citation 时间范围。不要通过“更强 Prompt”继续修补估算时间戳。

### 5. 当前 Q&A 兜底违反证据契约

Groq 路径在模型未返回引用时自动附加第一个检索 chunk。这会把“被检索到”伪装成“支持答案”。目标行为是证据不足即拒答。

### 6. 删除跨数据库与文件系统

现有 `DeleteSourceAndDependents` 没有覆盖搜索索引和持久上传文件；未来还有 EvidenceAudio 与多个 ArtifactVersion。SQLite 事务不能原子提交文件删除，必须设计可恢复的 tombstone/两阶段清理或等价方案。

### 7. 不要继续维护旧 TypeScript 实现

`app/`、`tests/`、Node 配置和 Wrangler 文件已在 Phase 0 从仓库删除（历史可从 Git 取回）。仓库现在只表达 Go 单一路径；CI 只运行 `go test`/`go vet`/`go build` 与多架构 Docker 发布。不要重新引入第二套实现，也不要把 Cloudflare 时代的历史文档（`docs/deployment.md`、`docs/superpowers/*`）当作实现依据——它们仅作存档。

### 8. 保护本地私密配置

`.env`、`.dev.vars` 及被忽略的本地部署配置可能包含密钥或账户标识。不要把它们加入提交、打印到日志或复制进 handoff。使用 `.env.example` 作为配置起点。

## ADR 快速索引

- [`0002`](adr/0002-groq-real-constraints.md)：Groq 实测限制与转码、JSON、TPM 应对。
- [`0003`](adr/0003-single-owner-self-hosted-product-boundary.md)：单 Owner、自托管及首次认领。
- [`0004`](adr/0004-one-way-knowledge-note-boundary.md)：EvidenceArchive 与 PersonalKnowledgeBase 单向 Markdown 边界。
- [`0005`](adr/0005-persist-normalized-evidence-audio.md)：持久 EvidenceAudio。
- [`0006`](adr/0006-sqlite-durable-processing-jobs.md)：SQLite 持久任务与重启恢复。
- [`0007`](adr/0007-remove-tenant-ownership-from-content-model.md)：从内容模型移除 `user_id`。
- [`0008`](adr/0008-evidence-first-knowledge-cards.md)：KnowledgeCard 和 Q&A 的 Citation 契约。
- [`0009`](adr/0009-explicit-per-attempt-paid-provider.md)：付费 Provider 单次授权。
- [`0010`](adr/0010-portable-data-directory-and-verified-backups.md)：统一数据目录与恢复验证。
- [`0011`](adr/0011-immutable-versioned-processing-artifacts.md)：不可变 ArtifactVersion。
- [`0012`](adr/0012-explicit-source-purge.md)：默认永久保留与显式 Purge。
- [`0013`](adr/0013-public-vps-threat-model.md)：公网安全、受信任代理与 TLS 边界。

[`0001`](adr/0001-provider-middleware.md) 已被 ADR-0009 取代，不要实现其中的全局 Provider 选择。

## 建议接手顺序

### 已完成的路线（2026-08-01）

Roadmap Phase 0–7 已全部完成并验证。接手新工作前：

1. 阅读本手册、产品目标、词汇表和 Roadmap。
2. 运行 `git status --short`，保留任何未知的现有改动。
3. 运行 Go 基线验证（`go test ./...`、`go vet ./...`、`go build ./cmd/cloudwisepod`、`git diff --check`）。

### 下一步候选（发布前）

- 填写 `docs/evalset.md` 的人工有用性评分。
- 公网 VPS 部署演练：Caddy/Nginx 反代 + `PUBLIC_URL`/`TRUSTED_PROXIES` 配置核对。

> 已于 2026-08-02 以真实 Groq 完成 Talk Python To Me #556（1:04:55）的黄金旅程：处理中关闭/重启自动恢复、EvidenceAudio/Transcript/KnowledgeCard、分段搜索、Markdown 下载及全新目录恢复播放均已验证。

### 未来功能改动

后续功能迭代从最小可验证改动开始：schema 演进继续走 `internal/store/migrations/` 有序迁移；不要绕过迁移系统直接改 `CREATE TABLE`。

### 推荐提交边界

- 一个提交只完成一个可验证迁移或领域能力。
- schema 迁移、repository 调整和相应测试放在同一提交。
- 不要把旧代码清理与新 schema 迁移混在一个巨大提交中。
- ADR 变更与实现变更应能互相追溯。

## 本地开发

前置：Go 1.25+、ffmpeg、Groq API key。

```bash
cp .env.example .env
# 填写 SESSION_SECRET 与 GROQ_API_KEY；不要提交 .env
set -a
source .env
set +a
go run ./cmd/cloudwisepod
```

首次访问 `http://localhost:8080/register` 认领唯一 Owner；认领后注册永久关闭，可安全用于公网初始化。

## 基线验证

每次开始和结束工作至少运行：

```bash
go test ./...
go vet ./...
go build ./cmd/cloudwisepod
git diff --check
```

根据改动增加以下证据：

- schema：从真实 v0.1 fixture 升级并校验数据。
- ProcessingJob：在每个外部副作用前后强制中断并重启。
- EvidenceAudio/Purge：使用真实临时目录检查文件生命周期。
- Citation：验证 Segment 存在、范围合法、Quote 逐字匹配。
- Markdown：golden file 与链接跳转集成测试。
- Backup：在全新目录恢复并运行读取/播放验证。
- Security：CSRF、登录限流、代理头和 SSRF 重定向测试。

## 测试约定

仓库积累了 620+ 测试函数，覆盖各包 88–100%（详见 README）。新增/修改代码应沿用以下约定，保证测试一致且易维护：

### 1. 纯函数直接单测

无副作用的函数（`ValidateCard`、`tokenizeKp`、`jaccard`、`formatSeconds`、`parseCommand`、`guessAudioExt` 等）直接构造输入断言输出，不依赖任何外部状态。

### 2. 外部依赖用 fake / 注入点隔离

- Provider：在 `internal/queue` 与 `internal/server` 各自定义 `fakeTranscriber`/`fakeAnalyzer`/`fakeHighlight`/`fakeQA` 等，通过 `w.bundleFor` 或 `srv.bundleFor` 注入，避免真实网络调用。
- HTTP：用 `httptest.NewServer` + `WithBaseURL(srv.URL)` 把 Groq/OpenAI 的请求重定向到本地测试服务器，可精确控制返回体与状态码。
- ffmpeg/ffprobe：`seedEvidence` 用 `exec.LookPath("ffmpeg")` 检测，不可用时 `t.Skip`，保证 CI 无 ffmpeg 也能跑。

### 3. DB 错误分支用「删表法」

handler/store 内部的 `return err` 分支用 `DROP TABLE xxx` 制造查询/写入失败（见 `TestStudyChat_CreateSessionDBError`、`TestCollection_ListDBError`、`TestProcess_EnqueueFails`）。注意：

- handler 测试要先 `claimOwnerAndLogin` 获得合法 cookie（认证与 CSRF 中间件在前）。
- 选择性删表时要确保前置步骤能通过（例如先 `CreateStudySession` 再删 `study_sessions`，使 `ListStudyMessages` 读 `study_messages` 成功而 `AppendStudyMessage` 的 `UPDATE study_sessions` 失败）。

### 4. 文件系统边界

- 一致性快照、备份/恢复用 `t.TempDir()` 隔离，绝不写工作目录。
- 文件不可读/目录被文件占用等失败用 `os.WriteFile(blocker, ...)` 占位制造。

### 5. 并发与时间

- worker 心跳与 `Run` 循环用 `context.WithCancel` 控制生命周期，`w.poll` 缩短到 10ms 加速测试。
- `sleepFn`、`heartbeatEvery` 等可注入的时间旋钮用短值覆盖。

### 覆盖率门槛

各包当前基线（`go test -cover ./...`）：

| 包 | 覆盖率 |
|---|---|
| auth | 100% |
| config | 100% |
| evalset | 100% |
| filehash | 100% |
| markdown | 100% |
| rss | 100% |
| cmd | 98% |
| safehttp | 97% |
| server | 96% |
| provider | 93% |
| queue | 91% |
| store | 88% |
| backup | 88% |

新增公开函数应附带对应测试；修复 bug 应先加一个能复现该 bug 的测试（TDD）。

## 下一版本 Definition of Done

下一版本只有在 [`product-goal.md`](product-goal.md#下一版本黄金旅程) 的完整旅程通过后才算完成。特别注意：

- “页面能显示卡片”不等于 Citation 正确。
- “数据库里有 job”不等于重启可恢复。
- “文件复制成功”不等于备份可恢复。
- “只有一个人在用”不等于公网安全可以省略。
- Q&A、更多模型或更多导出格式不能替代核心旅程。

## 交接前检查表

- [ ] 当前工作树状态已记录，未覆盖他人改动。
- [ ] 本次改动对应 Roadmap 中一个明确阶段。
- [ ] 领域命名与 `CONTEXT.md` 一致。
- [ ] 未重新引入多用户、全局 Provider 或可变 Artifact。
- [ ] 数据迁移与回滚/恢复风险已说明。
- [ ] 所需测试与真实场景证据已运行。
- [ ] README、ADR、Roadmap 或本手册已随行为变化更新。
- [ ] 未提交密钥、本地数据库、EvidenceAudio 或个人 Cloudflare 配置。
- [ ] 下一位接手者可以从一个明确、可运行的提交继续。
