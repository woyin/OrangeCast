# CloudWisePod 实施路线

基线：`main` / `v0.1.0` Go 实现  
目标：完成 [`product-goal.md`](product-goal.md) 中的下一版本黄金旅程

## 执行原则

- 每个阶段结束时，Go 应用必须可构建、可启动、可从上一版本数据升级。
- 先建立迁移与恢复能力，再改变 schema 或删除旧路径。
- 先保证证据不丢，再优化 AI 内容质量和界面体验。
- 新能力按领域词汇命名；不要重新引入 User、Tenant、全局 Provider 或可变分析结果。
- 不同时维护 Go 与 TypeScript 两套实现。

## Phase 0：建立单一代码与文档真相

> **状态：已完成（2026-08-01）。** 旧 TypeScript/Cloudflare 实现与 Node/Wrangler 构建路径已删除；Go module 统一为 `github.com/woyin/orangecast`；CI 仅运行 Go 门禁，Docker 发布工作流保留多架构。详见交付报告。下一项工作从 Phase 1 开始。

目标：让仓库只表达 Go、自托管、单 Owner 方向。

- 删除受 Git 跟踪的旧 `app/`、`tests/`、`package*.json`、`tsconfig.json`、`vite.config.ts`、`wrangler.toml` 和 Cloudflare 部署文件。
- 删除或归档仅服务旧实现的构建产物与依赖说明；历史仍由 Git 保存。
- 修正模块、镜像与产品命名差异，例如 `github.com/breestealth/wisepod`、CloudWisePod、OrangeCast 三套名称。
- CI 增加 `go test ./...`、`go vet ./...` 和 Go 二进制构建；发布工作流继续验证 `amd64`、`arm64` 镜像。

退出条件：干净 checkout 不需要 Node/npm/Wrangler；唯一有效的测试、构建和部署路径都是 Go。

## Phase 1：建立可升级的数据库迁移基础 ✅

目标：在修改 v0.1 schema 前，保证已有数据可检查、备份和迁移。

- 用有序、可记录版本的迁移替代仅执行 `CREATE IF NOT EXISTS` 的启动方式。
- 增加 `schema_migrations` 或等价机制，每个迁移在事务中执行并可重复检测。
- 第一次破坏性迁移前生成 SQLite 一致性备份，并验证失败时原库不被部分修改。
- 迁移前检查现有 `users` 数量：0 个保持未认领，1 个升级为 Owner；超过 1 个时停止并要求显式选择，不能猜测或静默合并。
- 为 `v0.1.0 → 下一版本` 编写真实数据库 fixture 的迁移测试。

退出条件：v0.1 fixture 可自动升级且数据计数、关联关系和登录凭据保持一致；迁移失败可安全重试。

## Phase 2：收敛为唯一 Owner 并建立公网安全基线 ✅

目标：让访问模型和内容模型真实表达单 Owner。

- 将首次注册改为实例认领；Owner 存在后关闭注册路由和界面。
- 保留单例 Owner 凭据与 Session，移除所有内容表、查询和接口中的 `user_id` / `UserID`。
- 移除全局 `settings.active_provider`，为单次 ProcessingJob 尝试记录 Provider 授权。
- 为所有改变状态的请求增加 CSRF 防护，为登录增加限流。
- 通过显式公开 URL 与可信代理配置设置 Secure Cookie；不信任任意 `X-Forwarded-*`。
- 让 RSS 与 Episode 音频下载复用同一安全 HTTP Client：解析地址校验、逐跳重定向校验、响应体上限和超时。

退出条件：第二个 Owner 无法创建；跨站写请求失败；恶意重定向不能访问私网；未授权付费调用不能发生。

## Phase 3：统一数据目录、证据音频与持久任务 ✅

目标：重启或外链失效都不会破坏 Source 的证据链。

- 引入单一 `DATA_DIR`，在其下明确划分数据库、EvidenceAudio、临时文件和备份。
- Episode 与 Upload 均生成并持久保存标准化 EvidenceAudio；转录和播放器只使用该文件。
- 原始输入在 EvidenceAudio 成功落盘并校验后才允许删除。
- 将 goroutine-only 调度改为 SQLite 驱动 worker；启动时领取 queued 与过期 running 任务。
- 明确定义 lease、心跳或 stale 判定，采用至少一次执行与幂等写入。
- 为转录成功、分析失败、进程中断和重复领取编写恢复测试。
- Purge 在数据库与文件系统之间采用可恢复的删除流程，避免只删一半。

退出条件：在下载、转码、转录、分析任一阶段强制终止进程，重启后都能自动完成或进入明确 failed；不会产生重复产物。

## Phase 4：不可变产物与 Evidence-first KnowledgeCard ✅

目标：任何 AI 衍生内容都可核验、可比较、可回退。

- Transcript 与 KnowledgeCard 从单 Source 唯一 upsert 改为不可变 ArtifactVersion。
- 记录 Provider、模型、Prompt/Schema 版本、ProcessingJob 尝试和创建时间。
- Source 显式指向当前 Transcript 与 KnowledgeCard 版本。
- 为 Segment 分配稳定标识；分析模型只返回 Segment 标识，不返回自行估算的时间戳。
- 摘要、关键要点、章节和金句全部携带 Citation；金句必须通过逐字匹配校验。
- 校验失败时拒绝保存、重试或省略该项，不能降级为无证据内容。
- 建立 5–10 个代表性中英文样本的 EvalSet，自动检查 schema、引用存在性、时间边界和逐字引用，并记录少量人工有用性评分。

退出条件：任意 KnowledgeCard 项都能解析到实际 Segment；旧版本可查看和恢复；模型或 Prompt 变更有可比较评测结果。

## Phase 5：完成检索与 KnowledgeNote 沉淀闭环 ✅

目标：达到五分钟判断价值并一键沉淀的核心体验。

- FTS 索引只覆盖 EvidenceArchive，不索引 Candidate。
- 搜索结果返回实际命中的 Transcript Segment，而不是仅展示不可定位的摘要片段。
- 点击搜索结果、Citation、章节或金句都跳到 EvidenceAudio 的确定时间点。
- 实现确定性 Markdown renderer：frontmatter、摘要、要点、章节、金句、标签和 Citation 链接。
- 实现单 Source 浏览器下载；不直接写 Vault，不做批量 zip、插件或双向同步。
- 增加 Markdown golden test、特殊字符/frontmatter 测试和 Citation 链接集成测试。

退出条件：Owner 能从处理完成页在五分钟内判断内容价值，并下载一份所有关键内容都可回听核验的 Markdown。

## Phase 6：备份、恢复与生产部署 ✅

目标：EvidenceArchive 可以迁移到全新实例并安全暴露在公网。

- 提供备份命令，使用 SQLite backup API 或等价一致性快照，而不是运行中直接复制数据库文件。
- 备份包包含 manifest、数据库、EvidenceAudio 和必要版本信息；不包含 API key 或 Session secret。
- 提供恢复命令，校验版本、文件哈希和目标目录为空或已明确授权覆盖。
- 增加全新临时目录中的备份→恢复端到端测试。
- 更新 Dockerfile/Compose 为单一持久 volume，并提供 Caddy/Nginx 反向代理示例。
- 发布前执行下一版本黄金旅程，包括处理中重启与跨实例恢复。

退出条件：备份包能在另一台干净机器恢复并播放同一 Citation；生产 Cookie 与代理配置通过安全测试。

## Phase 7：收紧次要 Q&A 能力 ✅

目标：保留有用探索能力，但不削弱 Evidence-first 契约。

- Q&A 仅检索当前 Source 的 Segment。
- 只有模型实际引用的 Segment 才能成为 Citation；删除“无引用时附第一段”的兜底。
- 证据不足、解析失败或引用非法时明确拒答。
- Q&A 结果不自动进入 KnowledgeNote，也不阻塞主版本发布。

退出条件：无法构造“有答案但引用与答案无关”的成功响应。

## 每阶段通用验证

```bash
go test ./...
go vet ./...
go build ./cmd/cloudwisepod
git diff --check
```

涉及 schema、文件生命周期、任务恢复或备份的阶段必须额外使用真实临时目录和真实 SQLite 文件运行集成测试，不能只依赖 mock。

## 已完成阶段说明（2026-08-01）

Phase 0–7 已按序完成并通过每阶段验证（`go test ./...`、`go vet ./...`、`go build ./cmd/cloudwisepod`、`git diff --check`）。

- **Phase 0**：删除 Cloudflare/TypeScript 实现与 Node/Wrangler 路径；Go module 统一为 `github.com/woyin/orangecast`；CI 只验证 Go。
- **Phase 1**：`internal/store/migrate.go` 有序迁移（`schema_migrations`、事务内应用、失败可重试）；v0.1 fixture 升级测试；`ConsistencyBackup`（VACUUM INTO）。
- **Phase 2**：`ClaimOwner` 原子认领；内容表/接口移除 `user_id`；移除全局 `active_provider`；CSRF double-submit；登录限流；`TRUSTED_PROXIES` + `PUBLIC_URL` Secure Cookie；`internal/safehttp` 共享 SSRF 防护客户端。
- **Phase 3**：统一 `DATA_DIR`（DB/evidence/tmp/backups）；EvidenceAudio 持久化（转码 + SHA256 + 幂等复用）；SQLite 驱动 worker（租约 + 心跳 + 启动恢复 + 至少一次）；两阶段可恢复 Purge。
- **Phase 4**：`artifact_versions` 不可变版本（transcript/knowledge_card + provider/model/prompt_version/job）；稳定 Segment ID；模型只返回 Segment ID；程序解析时间范围；金句逐字校验（无效项省略/拒绝）；`internal/evalset` 6 样本自动校验；`/sources/{type}/{id}/versions` 版本历史页（查看 + 一键回退）。
- **Phase 5**：分段级 FTS（返回实际命中 Segment + 时间）；`?t=`/`#seg-` 深链；确定性 Markdown renderer（golden 测试）；单 Source 下载端点。
- **Phase 6**：`cloudwisepod backup|restore` CLI（manifest + DB/证据 SHA256 + tar.gz、不含密钥、目标空或 `--force`）；全新目录恢复 E2E 测试；单一持久卷 Docker/Compose + Caddy/Nginx 示例。
- **Phase 7**：Q&A 只使用模型实际引用的 Segment（删除首片段兜底）；无可靠引用明确拒答（422）；结果不进入 KnowledgeNote。

### 发布前仍需人工验证

- **黄金旅程已于 2026-08-02 通过**：真实 Groq 处理 Talk Python To Me #556（1:04:55），包含处理中关闭/重启恢复、分段搜索、Markdown 下载及备份到全新目录恢复播放。
- EvalSet 人工有用性评分（`docs/evalset.md` 表格待填写）。
