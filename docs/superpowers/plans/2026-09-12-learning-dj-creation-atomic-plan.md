# CloudWisePod 学习、DJ 精听与文章创作：原子提交实施计划

日期：2026-09-12。产品优先级：用户已确认学习与创作并重。

计划状态：**49/53 首轮任务已完成（2026-09-13 最终更新）**。已完成并入库：A01–A04、B01–B09、K01–K04、G01–G08、D01–D08、C01–C10（48 个功能提交，起于 8ff8845 终于 7f33f90，逐任务记录见 `docs/implementation-log/`）。外部阻塞：K05（真实 Provider 评分）、D03（真实引擎试听）。未开始：C11–C13、U01–U03、V01–V03。门禁：race/vet/build/check 全绿；cover-gate 有已知债务（evalset 90.2/provider 87.9/queue 83.3 低于 95）。44 个增量迁移（0001–0044）全部一致。

## 1. 实施目标与完成范围

主流程：**订阅或导入 → 自动重点 → DJ 精听 → 笔记与个人理解 → 单集精读 / 跨集个人文章 → 修订、导出与历史回溯**。

首轮必须同时交付两种成文结果：

- 单集精读：围绕一个 Source 生成文章，正文区分来源转述、AI 展开、本人已有笔记和有依据的补充事实。
- 个人文章：围绕本人确认的主张，使用多个 Source 的素材，生成、修订、审校和导出文章。

继续使用 Go、SQLite、html/template 和原生 JavaScript。复用现有 Provider、处理队列、知识与创作数据；本轮不做框架迁移、数据库更换或多用户化。跨集每日 DJ、翻译、思维导图和更强语义检索列为独立后续任务，不阻塞首轮。

上层依据：[产品目标](../../product-goal.md)、[当前方向建议](../../research/2026-09-12-personal-podcast-direction.md)。本计划负责落实顺序与提交粒度；涉及旧规则变化由 A01 更新正式文档。旧路线图和迁移差距表只作为历史线索，不据其状态标签推断实现情况。

## 2. 代码基线与本轮证据

规划时 HEAD：`37abe675955a4a429038fec50ba3daac78adfb43`。实际基线是 **HEAD 加当前未提交工作区**，包含 EpisodeDigest 后端、页面、测试与产品文档改动。新建干净 worktree 时不能只检出 HEAD 就假定这些功能存在。

2026-09-12 本轮执行 `go test ./...`，全部有测试的包通过；`internal/models` 无测试文件。这是当前工作区的自动化基线，不是实际 Provider、中文 TTS、手机播放或成文质量的验收。此前 5 包定向测试属于继承证据；本轮没有执行 race、覆盖率、lint 或真实用户旅程。

| 已核实的当前实现 | 本计划处理方式 |
| --- | --- |
| RSS 手动/自动/关键词策略、转录、卡片、重点索引均存在 | 增量增加处理深度、质量与恢复；不重做订阅和转录 |
| 自动摄取在 `doAnalyze` 中跳过 Highlight/Narration | 修改明确的旧规则，并同步修改对应契约测试 |
| Groq 分窗分析后直接拼接摘要和重点 | 增加有来源约束的整集归并 |
| 自动 KeyPoint 初始为 `needs_review`，重建索引删除后重插 | 增加质量评估，并保留已有稳定身份与 Owner 决策 |
| DJ 已有解说和原音交替页面，原音按墙钟计时停止 | 增强现有编排与播放，不新增另一套同义播放器 |
| 精读文列表、批量入口、Markdown 下载存在于未提交改动 | A02–A03 整理为基线；后续修复引用、版本和恢复 |
| Document 精读入队已放行，`digestSourceMaterial` 仍统一读 Transcript | G01 修复实际材料读取路径，不能只测入队成功 |
| 精读文“重试渠道改写”重新入队整篇 digest；来源剔除删除现有正文块 | G06 做独立改写任务；G03 保留旧修订 |
| 新 CreationBrief 确认已存在，Writer 授权仍读取 ArticleBrief | C08 建立持久兼容连接，再接通新的写作入口 |
| IdeationSession/MaterialDiagnosis 表与存储函数存在，创建会话 handler 只保存意图 | C02–C03 增加会话轮次、实际诊断与候选提升 |
| ClaimMap 存储及旧 EvidenceMap 投影存在，当前 ClaimReview 写入旧证据审校结果 | C09/C11 实现主张身份契约及对应审校，不能只改显示名称 |
| 自动发现的门槛和批次存在，请求只使用当前变化素材，历史比较主要看标题/主张字符串 | C05–C06 补历史召回、结果校验和候选处理闭环 |

定位文件：`internal/queue/worker.go`、`internal/provider/groq.go`、`internal/store/keypoints.go`、`internal/server/templates/dj.html`、`internal/queue/digest.go`、`internal/server/digest*.go`、`internal/server/creation_workspace.go`、`internal/store/creation_flow.go`、`internal/server/editorial_writer_workflow.go`、`internal/server/editorial_review_workflow.go`、`internal/server/automatic_discovery.go`。

## 3. 原子提交规则

**一个任务对应一个可审阅的提交候选。** 它可以包含数据库、Go、模板和测试，因为它们共同完成一个行为；不要按“先提交测试失败、再提交实现”或“只提交调用方、稍后补被调用方”拆分。开发过程中可先写失败测试，提交时必须闭合。

每次开发只领取一张任务卡：

1. 读取该卡、依赖任务记录与当前 diff；确认所需基线已经提交或可安全继承。
2. 对照验收行为补必要回归测试，再完成最小实现。纯文字或低风险布局变化用渲染与人工检查，不堆实现镜像测试。
3. 执行卡内验证及本节通用检查。记录真实结果、跳过原因和外部验收缺口。
4. 检查暂存 diff 只包含本任务及必要测试、迁移、文档；共享文件按 hunk 暂存。不要用 `git add .` 收走已有改动。
5. 使用卡内建议信息提交；执行记录填写 SHA、验证命令和剩余事项。依赖未完成时不勾选任务。

一个任务如果实际包含两项能分别发布的行为，执行前拆为 `任务号-a`、`任务号-b`，各自补全依赖、验收与提交信息；不以“原子”为由做超大提交。纯数据/Interface 基础任务允许暂不展示入口，但必须有真实数据读写或契约测试，不能暴露尚未消费的用户选项。

工作区现有改动的整理属于 A02–A03；无关的 `CONTEXT.md` 大段修改不得混入。不要覆盖用户修改，也不依赖未提交的兄弟任务使当前提交通过。若需要验证暂存内容，在独立临时 checkout 中验证确切候选树；完成 A02–A03 前，禁止用只包含 HEAD 的 checkout 得出当前功能结论。

### 通用检查

- 每个 Go 变更提交：格式化本任务 Go 文件，`go test` 涉及包，`go vet` 涉及包，`go build -o <临时目录>/cloudwisepod ./cmd/cloudwisepod`，`git diff --check` 和 `git diff --cached --check`。构建不覆盖用户现有可执行文件。
- 改动共享 Interface、schema、路由装配或任务领取时：扩大到 `go test ./...`；队列、并发或状态 CAS 变更对相关包运行 `-race`。
- 每次准备合并到主分支、完成里程碑或最终发布：执行现有 CI 门禁 `go test ./... -race`、`go vet ./...`、临时路径构建、`make cover-gate`、`make lint`。若每任务直接进入主分支，则每任务均运行这些门禁。
- 当前覆盖率脚本对多数包要求 95%，`store` 与 `server` 分别登记 78.4/79.4 的历史地板；遵循实际脚本，不在本计划中假定全包已经 95%，不为过关降低门槛。
- 改动模型生成契约时运行对应 EvalSet；外部质量验收单独记录模型、提示版本、输入快照及费用，Fake Provider 测试不计作真实质量通过。
- 浏览器行为通过实际浏览器验证并记录；Go HTML 字符串断言不能替代播放、按钮及编辑器交互。初期保留 Go CI，无需为此先迁入整个 Node 工具链。

### 迁移与失败恢复

- 新 schema 使用后续增量迁移。下文 `migrations/<next>_*.sql` 是任务执行时分配的新序号，不能逐字用 `<next>`，也不能修改已经运行过的 0025 或更早迁移。
- 每张涉及数据的卡同时处理旧行回填、读取、备份恢复和 Purge/失效传播；这些不等到最后再补。
- 旧数据缺少版本血缘时标为 legacy/待核实，不把当前版本冒充历史输入。可确定的输入才可回填。
- 每个迁移提交保证旧数据可读；这不自动意味着旧二进制可读取新库。部署前备份，回退优先关闭新入口或前向修复；需要恢复旧库时明确恢复点，避免丢弃迁移后的新数据。
- SQLite 事务覆盖本地状态与结果提交，不覆盖远端模型。模型已返回的结果先缓存再落业务数据；“远端可能完成、本地没有结果”的任务保持结果未知，不能承诺 exactly-once 或无限自动重试。

## 4. 总览与依赖

首轮 A/B/K/G/D/C/U/V 共 **53 个任务**；后续 X 共 **4 个任务**。A02–A03 是已有改动整理，其余按卡内状态区分新增、修复或验收。

| 组 | 任务 | 交付结果 |
| --- | --- | --- |
| A | A01–A04 | 产品契约、可提交基线、质量样本 |
| B | B01–B09 | 来源快照、可恢复任务、用量与自动处理 |
| K | K01–K05 | 整集重点质量与个人笔记 |
| G | G01–G08 | 可追溯、可修订、可导出的单集精读 |
| D | D01–D08 | 日常可用的单集 DJ |
| C | C01–C13 | 从个人想法或自动候选到跨集文章 |
| U | U01–U03 | 学习与创作的日常入口 |
| V | V01–V03 | 恢复、真实旅程及发布交付 |
| X | X01–X04 | 可分别启动的后续扩展 |

推荐串行顺序：A → B → K → G → D → C → U → V。各卡列出的依赖为真实最低依赖；例如可在 K04 后先做 C01–C04，让创作入口尽早接受使用验证。这里的独立性用于控制变更范围，不要求启动并行代理。

里程碑：

- **M1 自动素材**：B09、K05 完成；新集自动出现可信重点，选择 DJ 深度后后台任务可完成。
- **M2 单集成文**：G08 完成；从一集重点与笔记到可回溯精读文。
- **M3 日常精听**：D08 完成；真实浏览器完成精听、恢复与记笔记。
- **M4 跨集创作**：C13 完成；从个人判断和多集材料到审校后的文章。
- **M5 个人版首轮交付**：U03、V03 完成；双流程真实验收、备份恢复和使用文档完整。

## 5. A：基线与契约

### A01 — 固化学习与创作并重的产品契约

**性质**：文档。**依赖**：无。**建议提交**：`docs(product): define learning DJ and creation delivery contracts`

**文件**：`docs/product-goal.md`、`docs/implementation-roadmap.md`、新 ADR、当前方向建议；`CONTEXT.md` 只调整相关术语段。

- [ ] 明确学习、单集精读、个人文章的入口与交付；自动化只生成选择开启的产物，现有订阅不静默扩大处理深度。
- [ ] 修订“自动摄取永远不生成 DJ”及“解说只朗读 Highlight Gist”的旧限制，允许本项目需要的明确标注串场；保留来源/AI/个人判断区分。
- [ ] 约定来源快照、单集精读修订、创作兼容连接及迁移路径；将旧计划已实现的任务标为历史，不复制其缺失插件指令。

**验收/验证**：文档之间无冲突；能从正式目标找到本计划，旧版本兼容与自动触发范围明确；链接和 diff 检查。本任务不修改业务代码。

### A02 — 整理现有 EpisodeDigest 后端为可构建基线

**性质**：已有工作区整理，非重新实现。**依赖**：A01。**建议提交**：`feat(digest): establish episode digest backend baseline`

**文件**：现有未提交 `models/digest.go`、`provider/digest*.go`、`store/digests*.go`、0025 迁移、`queue/digest.go`、`server/digest.go` 及它们依赖的 model/provider/worker/routes/template hunk 与对应测试。

- [ ] 精确盘点并保留已有后端、单集 handler 与草稿模板；将类型、schema、Provider、worker dispatch、路由和必要测试放在同一可构建候选树。
- [ ] `/digests`、Markdown、批量 UI 等 A03 独立入口若未纳入此树，相应 handler 分支、路由引用和测试也暂不纳入；不要遗漏 `digest.go` 对未提交下载 handler 的依赖。
- [ ] 将现有问题登记为 G 组任务，不在基线整理中追加新需求。0025 若已在用户数据运行，保持文件内容；后续修复用新迁移。

**验收/验证**：候选提交独立 checkout 可构建并通过 `go test ./...`；数据库创建、迁移及 digest 备份测试通过。若后端候选仍过大，可先拆“数据与纯 Provider”再“worker 与单集入口”，按本计划子卡规则记录，不修改原工作区成果。

### A03 — 整理现有精读文入口、列表和导出基线

**性质**：已有工作区整理。**依赖**：A02。**建议提交**：`feat(digest): expose existing list batch and markdown flows`

**文件**：`server/digest_list.go`、`digest_markdown.go`、相关 routes、`templates/{digests,dashboard,podcast_detail,document_detail,source_detail,progress}.html` 及对应测试。

- [ ] 纳入现有列表、批量、文档入口、下载和进度展示；按实际代码归档旧 `2026-08-25-episode-digest-closure.md` 任务状态。
- [ ] 记录“入队不等于成文成功”“下载尚缺引用跳转”等边界，避免将 UI 已存在当成 G 组验收完成。

**验收/验证**：`go test ./internal/server ./internal/store`；模板路由可达，现有下载输出可读取；候选提交不引用工作区之外的文件。A02/A03 后记录新的共同基线 SHA。

### A04 — 建立可比较的学习与成文样本

**性质**：质量基础。**依赖**：A03。**建议提交**：`test(eval): add learning DJ and writing acceptance fixtures`

**文件**：`internal/evalset/`、`docs/evalset.md`、新 `docs/acceptance/learning-creation-baseline.md`。

- [ ] 扩展已有 EvalSet，覆盖中文长访谈、英文信息密集节目、多主题/重复/广告、个人笔记及跨集矛盾观点；测试库只保存可提交的自建或获准片段，真实节目记录来源和本地快照标识。
- [ ] 为每个样本人工标出核心观点、支持区间、重要限定条件及不可推出的结论；保存基线结果，不只列节目标题。
- [ ] 制定评测表：观点覆盖、引用支持、重复、片段完整、解说帮助、文章可读性与个人判断保留。每项记录模型、提示版本和费用。

**验收/验证**：`go test ./internal/evalset`；fixture 可重复加载、人工参考可追溯。真实评分待实际生成后填写，不预先给通过结论。

## 6. B：共用来源、任务和自动处理

### B01 — 增加按版本读取的来源快照

**性质**：新增基础能力。**依赖**：A03。**建议提交**：`feat(evidence): resolve immutable audio and document snapshots`

**文件**：新增 `store/source_snapshots.go` 与测试；`models/`、`store/{artifacts,documents,evidence}.go`、增量迁移；必要的 `server/source_handlers.go`。

- [ ] 提供一个小 Interface：按 Source 及明确版本读取正文 Segment、原音身份、标题、卡片与引用位置；支持 Episode、Upload、Document。
- [ ] 血缘记录准确的 Transcript/Document 版本及 EvidenceAudio 哈希；保存新产物时引用这些身份。原音文件更新不能覆盖历史引用依赖的内容：保留相应证据版本，或明确标为不可回听。旧产物无法可靠推定时标 legacy。
- [ ] 增加按快照定位引用的读取路径；新版切换不让旧链接静默指向新转录。同 ID 不同版本的 Segment 不视为同一证据。

**验收/验证**：`go test ./internal/store ./internal/server ./internal/backup`；v1 链接在 v2 成为 current 后仍读 v1；文档位置不被当作音频秒数；不存在版本/Purge 后明确失效；迁移与恢复保留快照身份。

### B02 — 持久化任务输入、结果及幂等身份

**性质**：任务契约增强。**依赖**：B01。**建议提交**：`feat(queue): persist task snapshots checkpoints and deduplication`

**文件**：`models/models.go`、`store/jobs.go`、新增任务 checkpoint 文件与迁移、`queue/worker.go`、队列测试。

- [ ] 任务保存输入快照、配置版本、Provider/模型、处理意图和幂等 key；已知输入在入队时冻结，转录输出供后续任务在衔接事务中绑定。
- [ ] 以数据库唯一性或事务 CAS 实现并发入队去重，替代只先查后写；完成结果与产物 ID 可重复读取。
- [ ] 支持步骤 checkpoint、可重试失败、结果未知；普通刷新重用结果，显式“重新生成”产生新的意图 ID。
- [ ] 保留旧任务兼容读取，恢复时避免新任务依赖被清除的旧 current 指针。

**验收/验证**：`go test -race ./internal/store ./internal/queue`；并发请求只有一个有效任务；结果写入后进程中断再执行不新增版本；旧任务可继续。新增任务类型要与消费 handler 同次提交，当前任务不提前暴露未实现的类型。

### B03 — 记录学习和生成的真实用量及模型身份

**性质**：修复。**依赖**：B02。**建议提交**：`fix(usage): record actual models and provider usage for learning tasks`

**文件**：`provider/{types,groq,openai,http}.go`、`queue/worker.go`、`store/{jobs,editorial_costs}.go`、必要增量迁移与测试。

- [ ] 扩展现有返回契约传递实际模型与用量；覆盖转录、分析、归并、高光和 TTS 的实际计量单位，未知用量/价格明确为未知。
- [ ] 去掉保存转录/卡片/高光版本时的固定模型字符串；区分配置模型、实际模型和本地 TTS 零调用费。
- [ ] 以任务 attempt/远端调用身份唯一记账，解析失败但远端已收费时也保存已知 receipt；不把重复写账等同于一次远端调用。

**验收/验证**：`go test ./internal/provider ./internal/queue ./internal/store`；更换模型后产物与账目正确，未知价格不显示免费，重放 receipt 不重复累计；现有 Writer/Digest 用量继续兼容。

### B04 — 让自动处理与文章任务使用一致的预算检查

**性质**：增量费用治理。**依赖**：B03。**建议提交**：`feat(budget): account for learning and creation before provider calls`

**文件**：`store/editorial_costs.go`、新预算共享实现、`queue/worker.go`、`server/editorial_costs.go`、`models/` 与测试。

- [ ] 在现有价格与用量机制上形成单 Owner 月度检查，保留已有画像/单篇限制；覆盖学习、高光、TTS、digest 与文章，避免只按 profile 过滤漏算无画像任务。
- [ ] 调用前检查已知用量和在途预估，限制自动处理的单日数量/音频时长；并发预占与结算在事务内，失败释放按是否发生远端调用区分。
- [ ] 未配置价格的付费自动任务显示配置缺口；人工调用展示未知成本。预算不足保存可见原因，不无限重试。

**验收/验证**：`go test -race ./internal/store ./internal/queue ./internal/server`；无画像 digest 与学习费用进入月度汇总；并发领取不能忽略在途预估。页面明确“预算检查/估算”，不宣称远端费用绝不会超过预估。

### B05 — 保存订阅处理深度与任务配置快照

**性质**：数据能力。**依赖**：B02、A01。**建议提交**：`feat(ingestion): persist processing depth and policy snapshots`

**文件**：`models/models.go`、`store/podcasts.go`、`store/jobs.go`、新迁移与测试。

- [ ] 将“哪些新集处理”的既有 IngestionPolicy 与“处理到哪里”的深度分开：只提取知识、知识加 DJ；未来扩展不要塞进同一布尔字段。
- [ ] 旧自动订阅回填为只提取知识；保持旧手动处理默认行为。用户更改深度只影响新意图，已经排队的任务使用快照。
- [ ] 提供存储读写与合法组合检查；自动历史补处理需单独意图，不因启用而批量重跑所有旧集。

**验收/验证**：`go test ./internal/store ./internal/rss`；旧订阅行为不变、快照不随设置漂移、迁移恢复正确。此提交不展示尚未接通的 DJ 深度设置。

### B06 — 高光独立任务与可恢复生成

**性质**：重整现有执行路径。**依赖**：B02、B04。**建议提交**：`feat(highlights): run generation as a durable versioned task`

**文件**：`models/models.go`、`store/jobs.go`、`queue/worker.go`、新增 `queue/highlight.go`、`provider/selector.go`、相关测试。

- [ ] 将现有 `doHighlight` 挂到独立任务类型，注册路由选择与消费逻辑；使用冻结的 Transcript 版本和实际模型配置。
- [ ] 生成结果、产物版本与后续意图采用 checkpoint/事务衔接；失败不把已经完成的卡片抹成失败。
- [ ] 保留旧手动行为直到 B08 切换，避免两条路径同时对同一意图收费。

**验收/验证**：`go test -race ./internal/queue ./internal/store ./internal/provider`；重试高光不重转录/分析；进程重启可恢复；产物引用冻结版本，失败可独立查询。

### B07 — 解说独立任务与准确缓存身份

**性质**：修复并持久化。**依赖**：B06。**建议提交**：`feat(narration): persist synthesis jobs and content-aware cache keys`

**文件**：`queue/worker.go`、`queue/narration.go`（新）、`store/narrations.go`、`provider/narration.go`、迁移与测试。

- [ ] 解说任务按高光/脚本身份独立运行；缓存 key 至少包含文本指纹、输入版本、Provider、模型、音色及语言，不再只比较 Provider。
- [ ] 原音和合成音频继续分目录；先写临时音频并校验，再原子发布文件与数据库记录，失败清理临时文件。
- [ ] 引擎缺失标为不可用，单段失败可重试；Narration 缺失不阻塞原音。恢复备份后文件缺失可见并可重生成。

**验收/验证**：`go test -race ./internal/queue ./internal/store ./internal/provider`；相同输入复用，文本/音色变化生成新版本；半写文件不可播放；重试不重跑高光。

### B08 — 接通订阅设置与自动学习到 DJ

**性质**：用户可见能力。**依赖**：B05、B07。**建议提交**：`feat(ingestion): prepare DJ automatically for opted-in subscriptions`

**文件**：`rss/refresher.go`、`queue/worker.go`、`store/jobs.go`、`server/podcasts.go`、`templates/podcast_detail.html` 及测试。

- [ ] 设置页暴露处理深度；新集转录 → 分析 → 所选高光/解说任务按结果衔接。
- [ ] 修改旧“Automated 必须无高光”的测试，替换为按深度断言；手动/自动均只走一套新编排。
- [ ] 分析完成与下游 enqueue 使用事务或持久 outbox 衔接，进程在两步间退出仍能补齐；关闭策略后不新增意图。

**验收/验证**：`go test -race ./internal/rss ./internal/queue ./internal/store ./internal/server`；只提取知识不调用 DJ Provider，开启 DJ 后自动产物齐全；重复刷新、进程中断、过滤和旧订阅路径覆盖。

### B09 — 显示分阶段进度与精确重试

**性质**：用户可见恢复。**依赖**：B08。**建议提交**：`feat(progress): show stage results and retry only failed work`

**文件**：`store/jobs.go`、`server/{progress,process}.go`、相关 routes、`templates/{progress,source_detail,podcast_detail}.html` 与测试。

- [ ] 单集显示知识就绪、DJ 准备中/可用/部分缺失；不再用一个 processed 状态代表所有增强已完成。
- [ ] 从失败阶段重试并带回冻结输入；展示预算、引擎不可用、结果未知的不同原因和可采取动作。
- [ ] 正常 GET 和轮询只读，不入队或调用模型；双击重试返回同一任务，结果页能定位产物。

**验收/验证**：`go test ./internal/server ./internal/store` 加浏览器操作；模拟卡片成功、TTS 失败，页面可继续读和听原音，重试只命中 TTS。

## 7. K：重点质量与笔记

### K01 — 增加整集重点归并与语义去重

**性质**：生成质量增强。**依赖**：A04、B03。**建议提交**：`feat(analysis): consolidate episode insights with grounded citations`

**文件**：`provider/{groq,openai,prompts,types}.go`、新增 `provider/analysis_reduce.go` 与测试。

- [ ] 保留已有分窗，新增有输入上限的归并步骤；归并只使用候选及必要原文证据，可分层处理，不再次无上限发送整集。
- [ ] 输出整集主线和少量高价值观点，合并重复观点但保留冲突与限制条件；数量随内容价值决定，不强制凑数。
- [ ] 保持 Citation lineage；归并新增外部事实、引用不存在的候选或丢掉必要证据时失败可见。Provider 两条路径遵守相同契约。

**验收/验证**：`go test ./internal/provider ./internal/evalset`；多窗重复不翻倍，相反观点不合并为共识，末窗关键观点保留，输入过长受限，归并用量可统计。

### K02 — 让自动重点具有可解释的质量判断

**性质**：新增自动质量步骤。**依赖**：K01、B02、B04。**建议提交**：`feat(keypoints): assess groundedness and usefulness before discovery`

**文件**：新增 `provider/keypoint_quality.go`、`queue/keypoint_quality.go`、`store/creation_workspace.go`、质量结果迁移与测试。

- [ ] 程序检查引用/空内容/明显重复，独立判定步骤检查“证据是否支持、观点能否独立表达、是否纯背景”；结果包含原因及输入快照。
- [ ] 通过候选形成 `ready` 的依据；不确定或校验失败保留 `needs_review`，正常阅读仍可用。参考真正引用的原文，不让生成模型自己盖章。
- [ ] 阶段可独立重试并记账；本任务持久化可读取的质量结果，K03 再消费结果更新正式状态。此提交不调用尚未实现的 K03 Interface，也不直接重建整个重点库。

**验收/验证**：`go test ./internal/provider ./internal/queue ./internal/store ./internal/evalset`；错误引用、遗漏限定条件、背景标题被拦；具体且有依据的观点可通过；模型不可用时不伪装通过。

### K03 — 重分析保留重点身份和 Owner 决策

**性质**：数据正确性修复。**依赖**：K02、B01。**建议提交**：`fix(keypoints): reconcile reanalysis without losing owner decisions`

**文件**：`store/{keypoints,creation_workspace,editorial_relevance}.go`、必要迁移、相关 queue 索引衔接与测试。

- [ ] 替换无条件删除重插：稳定匹配相同来源与语义的自动重点；保留质量确认、排除、收藏、生产状态和人工编辑派生关系。
- [ ] 自动质量通过形成一条幂等 MaterialChange；原样重分析不产生新发现价值。实质变更标新版本，旧引用无法映射时标 stale。
- [ ] 状态、索引、MaterialChange 与失效传播在事务中完成；人工笔记、旧文章引用不可悬空。

**验收/验证**：`go test -race ./internal/store ./internal/queue`；相同输入重跑不增变化，OwnerExcluded 不被自动评分覆盖，人工修订保留，引用失效可见且下游不能继续导出伪有效证据。

### K04 — 从来源位置保存、编辑并导出个人笔记

**性质**：现有笔记衔接完善。**依赖**：B01、A03。**建议提交**：`feat(notes): preserve source anchors and personal reflections across edits`

**文件**：`server/owner_notes.go`、`store/owner_notes.go`、`templates/{source_detail,document_detail,keypoints}.html`、`server/knowledge.go` 或现有 Markdown 导出实现、必要迁移与测试。

- [ ] 从 Segment/重点直接写来源笔记或个人理解；提交并校验 Reference 字段，个人理解不伪装为 Citation 支持的来源主张。
- [ ] 增加笔记编辑与乐观并发控制；历史精读/文章保留当时选用的笔记版本或快照，不随后续编辑改变。
- [ ] 在既有知识导出中保留两类笔记、引用跳转、个人文本；不把普通标注悄悄转换成 OwnerClaim。

**验收/验证**：`go test ./internal/store ./internal/server ./internal/markdown` 加浏览器；音频与文档位置正确、重复保存不制造冲突、过期编辑提示冲突、旧产物引用旧笔记、导出可回溯。

### K05 — 完成重点与选段质量基线对比

**性质**：验收记录。**依赖**：K03、K04、B09。**建议提交**：`test(eval): record episode insight quality acceptance results`

**文件**：`docs/evalset.md`、`docs/acceptance/learning-creation-baseline.md`、必要最小回归样本。

- [ ] 用 A04 相同输入比较旧结果与新结果；人工核对重点覆盖、证据支持、重复与限定条件，记录真实 Provider 结果。
- [ ] 建议首轮门槛：样本中无捏造关键事实或错挂引用，已标注核心观点覆盖至少 80%，重复重点占比不高于 10%，人工有用性每集至少 4/5。分母、人工判据与失败样本一起保存；这些是验收目标，不是现有成绩。
- [ ] 不达标时按原因回到 K01/K02 修复；只重跑受影响样本及必要回归，不反复改阈值求通过。

**验收/验证**：记录可复核输入、模型、结果与评分；自动测试继续通过。外部 Provider/人工评分缺失时状态保持待验收，不阻塞独立代码任务但阻塞最终质量完成声明。

## 8. G：单集学习到精读文

### G01 — 冻结精读素材并修复 Document 读取路径

**性质**：修复。**依赖**：B01、B02、K04。**建议提交**：`fix(digest): resolve frozen source and note inputs for every source type`

**文件**：`queue/digest.go`、`store/digests_enqueue.go`、`provider/digest.go`、必要迁移及 `queue/digest_test.go`、`server/document_digest_test.go`。

- [ ] 用 B01 读取 Episode、Upload、Document；Document 从其正文版本与位置构造素材，不再查 episode 的 Transcript 指针。
- [ ] 任务入队冻结来源、卡片、选用笔记和写作设置；正文较长时按证据完整单元裁分或分阶段处理，不能静默截掉尾部。
- [ ] 笔记读取错误必须可见；Annotation 若作为素材使用，显式记录来源类型与 ID，不将它丢掉或冒充另一类笔记。

**验收/验证**：`go test ./internal/queue ./internal/store ./internal/server`；三类 Source 从入队跑到正文；排队后重分析/改笔记不改变这次输入；文档缺失、损坏快照与过长输入有确定错误或处理路径。

### G02 — 精读生成分步恢复与整份修订原子保存

**性质**：可靠性修复。**依赖**：G01、B04。**建议提交**：`fix(digest): checkpoint generation and persist complete revisions atomically`

**文件**：`queue/digest.go`、`store/digests.go`、checkpoint 扩展与迁移、对应测试。

- [ ] 为 compose、检索落源、补织分别保存输入与结果；采用 B02 的幂等意图，保存成功后重试不重复调用上游模型。
- [ ] 正文、块、检索来源、缺口与生成来历在一个事务中发布为完整修订；未完成修订不被 `GetCurrentEpisodeDigest` 当成最新可用结果。
- [ ] 检索逐项有上限与断点；已落源 Document 可复用，补织失败不让半份结果成为正式 current。结果未知与可恢复失败分开显示。

**验收/验证**：`go test -race ./internal/queue ./internal/store`；注入每个持久化间隙故障，重启后至多一份成功修订；主文成功但渠道失败不重跑主文；预算与调用记录保持一致。

### G03 — 以新修订保存精读文调整和来源剔除

**性质**：修订正确性与编辑。**依赖**：G02。**建议提交**：`feat(digest): save block edits as immutable revisions`

**文件**：`models/digest.go`、`store/digests.go`、`server/digest.go`、`templates/digest.html`、`provider/digest_validate.go`、迁移与测试。

- [ ] 增加父修订、修订原因与来源快照关联；调整标题、块顺序或允许编辑的正文时创建新修订，不原地更新旧正文。
- [ ] 剔除补充来源时派生一份移除相应事实块的新修订，旧版本仍可审计；失效的渠道版本不继承为有效。
- [ ] 编辑仍保留块类型和引用约束：转述保持依据，AI 展开标注身份，笔记块只选择本人已有笔记版本；修改笔记内容回 K04 保存，再显式选用。
- [ ] 表单携带 base revision，过期编辑返回冲突；任意粘贴文本不能被标成已核验引用事实。

**验收/验证**：`go test ./internal/store ./internal/server ./internal/provider` 加浏览器；旧版本内容和引用不变；剔除不影响其他修订；越权类型转换/无依据编辑被拒；重复提交不产生重复修订。

### G04 — 精读页面与 Markdown 保留可点击证据

**性质**：导出衔接修复。**依赖**：G03、B01。**建议提交**：`fix(digest): preserve versioned citations in previews and markdown`

**文件**：`server/{digest,digest_markdown}.go`、`templates/digest.html`、现有 Markdown helper、对应测试。

- [ ] 正文每条引用显示节目/文档名及时间/位置，链接 B01 的确切快照；不只显示 `seg-0001` 字符串。
- [ ] 共享确定性渲染逻辑生成 HTML/Markdown 中的来源与笔记身份；多行 AI callout、空白和转义正确。
- [ ] 增加复制 Markdown 的完成/失败反馈，下载文件与复制内容一致；来源失效显示明确状态，不生成指向当前版本的伪替代链接。

**验收/验证**：`go test ./internal/server ./internal/markdown` 加浏览器复制/下载；音频、文档、笔记和 AI 块导出均保留身份；改 current 后导出旧文仍定位旧依据。

### G05 — 区分精读草稿与可交付版本

**性质**：现有门禁补齐。**依赖**：G03、G04。**建议提交**：`feat(digest): validate delivery readiness against current evidence state`

**文件**：新增 `store/digest_readiness.go`、`server/digest.go`、`digest_markdown.go`、`templates/digest.html`、相关验证测试。

- [ ] 建立只读 readiness Interface，检查引用、来源确认、块类型、笔记快照及来源失效；预览不触发生成或“确认”。
- [ ] 个人草稿允许查看/导出，但显式携带草稿与未处理问题标记；面向交付的版本必须满足现有 ADR-0023 来源确认规则。
- [ ] 在交付导出时重新检查，而不只相信生成时的状态；给出具体问题与操作入口。

**验收/验证**：`go test ./internal/store ./internal/server`；pending 来源、已 Purge 依据、过期修订不能冒充已可交付；全部处理后通过；草稿下载不自动创建发布历史。

### G06 — 单独重试渠道改写

**性质**：修复。**依赖**：G02、G03、B04。**建议提交**：`fix(digest): retry channel rewrites without regenerating the article`

**文件**：`server/digest.go`、`queue/digest.go`、`models/models.go`、`store/digests*.go`、相关迁移与测试。

- [ ] 为渠道改写注册独立任务类型，输入指定 digest revision 与 channel；修复现有重试 handler 入队整篇文章的行为。
- [ ] 改写只读该修订已通过验证的块，不再次检索/补事实；保存输出与输入 hash，主文改变后不沿用旧渠道结果。
- [ ] 同渠道同输入去重；失败保留主文，成功后页面只更新相应渠道产物并计独立费用。

**验收/验证**：`go test -race ./internal/queue ./internal/store ./internal/server`；用调用计数证明重试只有 rewrite，compose/search 均为零；不同修订不串结果。

### G07 — 将单集页的学习成果带入精读创作

**性质**：产品衔接。**依赖**：G04、G05、G06、K03。**建议提交**：`feat(digest): connect episode learning notes and article generation`

**文件**：`server/source_handlers.go`、`server/digest.go`、`templates/{source_detail,document_detail,digest,digests}.html`、必要视图查询与测试。

- [ ] 单集页并列读重点、听 DJ、写笔记、生成/继续精读；显示最近修订和正在执行的任务。
- [ ] 用户可选已有笔记与生成设置，提交时冻结输入；生成完成能直接进入产物，失败回到可处理状态。
- [ ] 精读页提供返回该来源及输入笔记的入口；参数来自持久身份，不靠全局“最近一篇”猜结果。

**验收/验证**：`go test ./internal/server` 加一次完整浏览器旅程；连续两集同时生成不会跳错文；刷新不再次调用模型，批量失败逐集可见。

### G08 — 将确切精读修订纳入创作历史

**性质**：历史闭环。**依赖**：G05、G07。**建议提交**：`feat(history): track digest revisions without treating exports as publication`

**文件**：`store/creation_history.go`、`models/digest.go`、`server/digest.go`、`templates/digest.html`、迁移与测试。

- [ ] 让精读文关联确切 digest revision、正文和来源；个人作品无专门画像时复用默认画像，不创建额外品牌副本。
- [ ] 用户明确记录发布后形成 published 历史；下载、复制和预览不自动当成发布。保存为创作历史的未发布文保留 unpublished 身份。
- [ ] 同修订重复操作幂等，后续修订与既有历史有明确关系；为 C05 的重复提醒提供可用正文。

**验收/验证**：`go test ./internal/store ./internal/server ./internal/backup`；重复点击不新增历史，导出不发布，来源删除保留“依据已删除”的记录而不私存原文，备份恢复关联仍正确。

## 9. D：单集 DJ 日常可用

### D01 — 程序化约束高光连续性与重叠

**性质**：修复。**依赖**：A04、B06。**建议提交**：`fix(highlights): enforce contiguous non-overlapping evidence spans`

**文件**：`provider/highlight.go`、`citation.go`、提示词与高光测试。

- [ ] 校验 Segment 顺序、真实连续区间、start/end、区间重叠与重复；多点引用不能直接取 min/max 把中间大片无关音频带入。
- [ ] 对合法的相邻段规范化，非法跨越或无法解释的区间显式拒绝；起止仍来自程序，不让模型编造时间。
- [ ] 长节目选段输入分阶段受限，先形成候选再全局择优；避免一口气发送全稿。结构正确与“听起来完整”分开验收。

**验收/验证**：`go test ./internal/provider ./internal/evalset`；乱序、缺段、重叠、零时长、跨广告空洞有明确结果；正常连续区间保留，尾段不遗漏。

### D02 — 保存有目标时长的 DJ 播放清单

**性质**：编排能力。**依赖**：D01、B01、B02。**建议提交**：`feat(dj): persist versioned listening plans with duration targets`

**文件**：新增 `models/dj.go`、`store/dj_plans.go`、`queue/dj_plan.go`、迁移与测试；复用 Highlight。

- [ ] 提供 DJ 编排 Module 的小 Interface：来源快照、可用高光和目标时长 → 有序清单；项包括原音区间或解说引用，保存输入版本与选择理由。
- [ ] 首版保持节目原顺序，按价值与上下文完整性控制总时长；将串场估计时长计入，内容不足允许短于目标。
- [ ] 持久化清单与完整输入，复用同一意图结果；新高光版本生成新清单，正在收听的旧清单不被替换。

**验收/验证**：`go test ./internal/store ./internal/queue`；相同输入排序稳定、总长合理且无重叠，旧清单保持原内容；备份恢复后清单和原音身份不变。

### D03 — 验证并完善真实中文 TTS Adapter

**性质**：集成完善。**依赖**：B07、A01。**建议提交**：`fix(tts): support a verified configurable Chinese narration runtime`

**文件**：`provider/narration.go`、`config/config.go`、`cmd/cloudwisepod/serve.go`、`.env.example`、部署文档与测试。

- [ ] 执行前读取所选 Kokoro 发行版的官方安装、参数、模型与中文音色说明；核实现有 `--text/--voice/--output` 假设。能适配则修复现有 Adapter，不凭名称假定 CLI 兼容。
- [ ] 保存引擎/模型/音色/语言配置；传入 context、超时与输出校验，错误信息去除敏感配置。实际不支持中文时记录阻断，选择有官方中文支持的 Adapter 后再接入。
- [ ] 提供预检和短句试听入口，中文专名、数字、停顿、中英混合均使用样本验证；缺失引擎不影响原音。

**验收/验证**：`go test ./internal/provider ./internal/config ./cmd/cloudwisepod`；模拟测试覆盖参数和失败；真实引擎必须生成可播放音频并记录版本、语言与试听结果。仅模拟命令通过不足以勾选本任务。

### D04 — 为 DJ 增加开场、串场和收尾

**性质**：听播编排完善。**依赖**：D02、D03。**建议提交**：`feat(dj): narrate introductions transitions and episode recaps`

**文件**：`queue/dj_plan.go`、`provider/narration.go`、新增 `provider/dj_script.go`、必要清单字段与测试。

- [ ] 开场说明本集与精听范围，过渡基于相邻高光 Gist，结尾提示回听/记笔记；首版优先受约束文本模板与现有 Gist，避免额外无必要模型链。
- [ ] 所有语音都标为 AI 解说，引用输入来源，不把个人判断伪装成主播原话；不机械朗读全部文字重点。
- [ ] 通过 B07 生成各段音轨，真实合成时长反馈 D02；部分解说失败时保留清单并直接播下一原音。

**验收/验证**：`go test ./internal/provider ./internal/queue ./internal/store` 加真实试听；开/收尾完整，转场不重复讲完整原音，AI/原音可辨，缺一段解说不会阻塞后续。

### D05 — 将播放逻辑收口为可取消的媒体状态机

**性质**：播放器正确性。**依赖**：D02。**建议提交**：`fix(player): drive DJ playback by media time and explicit state`

**文件**：新增 `server/static/dj-player.js`、`templates/dj.html`、浏览器测试夹具（不打包为生产路由）、相关 server 测试。

- [ ] 提取现有内联控制逻辑，提供 load/play/pause/next/stop Interface；以媒体 currentTime、ended、error、seeking 等事件控制原音区间，不以墙钟定时器判断播完。
- [ ] 两类音轨互斥；播放 session token 或等价机制取消旧队列，停止/手动试听后未完成 Promise 不得推动旧队列继续。
- [ ] 明确 loading/playing/paused/buffering/ended/error 状态；处理拒绝播放、seek 尚未完成、网络中断和手动切换。

**验收/验证**：`go test ./internal/server` 加真实浏览器状态用例；缓冲不消耗片段时间，暂停不跳下一段，停止后无幽灵续播，快速切换不双声叠播。浏览器夹具可模拟媒体事件验证控制逻辑，真实音频另测。

### D06 — 提供完整 DJ 控件与原节目切换

**性质**：用户体验。**依赖**：D04、D05。**建议提交**：`feat(player): add DJ transport controls and full-episode handoff`

**文件**：`templates/dj.html`、`static/dj-player.js`、`static/app.css`、`source_handlers.go`。

- [ ] 展示当前片段、总进度、播放/暂停、上一段/下一段、速度、原音/AI 身份和缺失解说提示；支持键盘与移动触摸。
- [ ] “继续听原节目”定位当前原音的实际位置；回到 DJ 时能辨认所在片段，不重复从头。
- [ ] 自动播放只由用户播放动作启动；不把浏览器阻止播放当成整段已听完。

**验收/验证**：SSR 渲染检查及桌面/手机浏览器手动用例；倍速区间结束正确、键盘焦点可用、无横向溢出，手动操作后状态和按钮一致。

### D07 — 保存听播进度并接入系统媒体控件

**性质**：个人连续使用。**依赖**：D06、B01。**建议提交**：`feat(player): persist listening progress and media session actions`

**文件**：新增 `store/listening_progress.go`、`server/listening.go`、`models/dj.go`、迁移；`static/{player,dj-player}.js`、routes 与测试。

- [ ] 保存 Source、清单版本、片段身份、片段内位置、速度和更新时间；完整播放与 DJ 进度语义分开，节流写入且在暂停/离开时补存。
- [ ] 重新打开显示“继续听”，恢复后等用户播放；清单变化只在能可靠映射时迁移位置，否则保留旧会话或显式提示。
- [ ] 按浏览器支持接入 Media Session 的播放、暂停与跳段；多标签进度使用版本/CAS 或更新序号解决旧请求覆盖新状态。

**验收/验证**：`go test -race ./internal/store ./internal/server` 加真实浏览器；关页重开、双标签、倒序请求、来源删除均正确。手机后台/锁屏行为单独记录平台结果，不把接口存在当成后台播放保证。

### D08 — 精听时直接收藏片段并记录理解

**性质**：学习衔接。**依赖**：D07、K04。**建议提交**：`feat(dj): capture cited moments and personal notes while listening`

**文件**：`templates/dj.html`、`static/dj-player.js`、`server/{owner_notes,knowledge}.go`、必要测试。

- [ ] 提供“收藏这段”“记下我的理解”，默认锚定正在播放的原音区间；播放 AI 解说时锚定其参考原音，明确 Reference 身份。
- [ ] 复用既有 Pin、Annotation、OwnerNote，不创建独立 DJ 笔记体系；保存完成不重置播放器。
- [ ] 保存的内容出现在单集学习页并可由 G07/C01 选用，支持回到准确播放位置。

**验收/验证**：`go test ./internal/server ./internal/store` 加精听→记笔记→进入精读流程；无暂停丢失、无错挂，重复收藏幂等，个人理解没有变成原文证据。

## 10. C：跨集个人文章

### C01 — 从重点与个人笔记建立创作素材选择

**性质**：产品衔接。**依赖**：K03、K04。**建议提交**：`feat(creation): start writing from selected evidence and owner notes`

**文件**：`server/{creation_workspace,material_candidates,editorial}.go`、`store/{editorial_relevance,creation_workspace}.go`、`templates/{keypoints,source_detail,workbench}.html`、必要选择快照与测试。

- [ ] 复用 EnsureDefaultEditorialProfile，首次创作不要求先填完整品牌；已有画像选择保留，自动发现仍需用户开启。
- [ ] 从一个或多个来源选择 KeyPoint 与个人笔记，保存素材选择快照和显式范围；OwnerReflection 以个人材料身份进入，不能转成自动 KeyPoint 冒充来源。
- [ ] 尊重质量、stale、OwnerExcluded 和 Source 的 Provider 策略；被排除或不兼容材料明确解释，不静默过滤到改变用户意图。

**验收/验证**：`go test ./internal/store ./internal/server` 加浏览器；两集素材与个人笔记进入同一创作意图；首次进入不产生付费调用；返回修改选择后保留用户输入。

### C02 — 为定向构思保存轮次与材料诊断快照

**性质**：补齐现有持久模型。**依赖**：C01、B02。**建议提交**：`feat(ideation): persist conversation turns and material snapshots`

**文件**：`models/creation_workspace.go`、`store/creation_flow.go`、新增会话轮次迁移、`server/creation_workspace.go` 与测试。

- [ ] 在已有 IdeationSession/MaterialDiagnosis 上增加轮次身份、用户输入、约束变更、材料快照、任务状态和输出引用；不再只是保存初次 intent。
- [ ] 每轮冻结输入与前一轮身份，允许用户补充问题、范围、立场，重新诊断产生新轮次；并发提交使用 CAS 和幂等 key。
- [ ] 提供会话详情与历史读取，刷新后可继续；此任务持久化真实轮次但不假装已有 AI 诊断。

**验收/验证**：`go test -race ./internal/store ./internal/server`；刷新不丢输入，重复提交一轮只一份，旧结果不覆盖新范围，备份可恢复完整轮次。

### C03 — 实际运行有依据的构思诊断

**性质**：新增生成行为。**依赖**：C02、B04、A04。**建议提交**：`feat(ideation): generate grounded material diagnoses and proposed claims`

**文件**：新增 `provider/ideation.go` 与 Groq/OpenAI Adapter 实现、`queue/ideation.go`、`store/creation_flow.go`、`server/creation_workspace.go`、会话模板与测试。

- [ ] 为当前问题检索限定范围内的材料，输出支持、反驳、补充、缺口和少量可选主张，每项关联具体材料身份；引用必须来自冻结快照。
- [ ] 多轮使用已保存的前文和最新约束，用户修改方向后新结果明确替代哪次建议；超出材料的事实作为缺口，不直接写成结论。
- [ ] 通过 B02 持久任务运行并记账；用户选中方向后创建带会话/轮次关联的 CreationProposal，尚不等于 OwnerClaim。

**验收/验证**：`go test ./internal/provider ./internal/queue ./internal/store ./internal/server`；矛盾材料被识别，空材料产生缺口，虚构引用被拒；旧轮次引用可回看，重复提升不会创建重复提案。

### C04 — 核实研究缺口的解决依据

**性质**：状态正确性修复。**依赖**：C01、B01。**建议提交**：`fix(research): require valid source evidence before resolving a gap`

**文件**：`store/creation_flow.go`、`server/creation_workspace.go`、`models/creation_workspace.go`、迁移与测试。

- [ ] 替换当前“非空 sourceID 即可 resolved”的逻辑；保存 SourceType、SourceID、版本和用于解决问题的具体材料/位置。
- [ ] 验证来源存在、已处理、可用且与所选依据对应；由 Owner 确认这些材料解决问题，程序的存在性校验不宣称证明事实为真。
- [ ] 来源删除或失效后重新阻断相关 Brief/待生成作品；已生成作品标为需要复核。首轮用手动导入解决创作缺口；精读文 SourceSearch 保持既有用途，不新增通用联网研究平台。

**验收/验证**：`go test ./internal/store ./internal/server`；虚构 ID、未处理 Source、无关联依据不能消除 blocking；有效依据可解决，Purge 后再次阻断；调整旧测试中 `new-source` 字符串占位方式。

### C05 — 自动发现使用新素材种子与历史材料

**性质**：完善已存在的发现算法。**依赖**：K03、B04、A04。**建议提交**：`feat(discovery): validate new-material proposals with historical context`

**文件**：`server/automatic_discovery.go`、`store/{discovery_schedule,creation_history,semantic_search}.go`、`provider/scout.go`、对应测试。

- [ ] 保留已有 6 项变化/2 个 Episode、防抖、每日上限和批次背压；新素材为种子，有限召回历史 Relevant 材料和创作历史，所有发送材料逐项检查 Provider 策略。
- [ ] 严格校验 Scout 输出的材料 ID 属于发送快照，每个候选含当前窗口新价值，并达到跨集要求；结果不足允许少于目标，记录原因。
- [ ] 同义标题和相同主张不占多个名额；全文历史可用于高置信重复判定，只有标题/摘要时仅提示疑似重复。先复用已有检索，明确字符向量的召回限制。

**验收/验证**：`go test ./internal/server ./internal/store ./internal/provider`；纯历史重组、伪造素材 ID、同义重复被挡；有新论据的 FollowUp 保留并解释；样本证明 OwnerExcluded 与 LocalOnly 不会通过历史召回绕过。

### C06 — 完成候选批次的接受、保存和拒绝闭环

**性质**：用户工作流。**依赖**：C03、C05。**建议提交**：`feat(proposals): resolve discovery batches through explicit owner decisions`

**文件**：`store/{proposal_batches,creation_flow}.go`、`server/creation_workspace.go`、`templates/workbench.html` 或新候选模板、迁移与测试。

- [ ] 增加保存以后、带原因拒绝、编辑后接受、转入构思会话；展示主张、受众、素材、新旧来源数与缺口。
- [ ] 原子更新候选决策和批次状态；未处理候选存在时保持背压，全部有决策后批次可完成。保存的方向继续参与去重。
- [ ] 接受主张才记录 OwnerClaim；重复动作幂等，编辑不是靠覆盖旧 AI 建议来抹掉来历。

**验收/验证**：`go test -race ./internal/store ./internal/server`；未处理批次阻止后续自动生成，处理完成释放下一窗口；保存/拒绝不会立刻按旧库存规则补货。

### C07 — 生成可编辑且真正可写作的 CreationBrief

**性质**：完善当前最小 Brief。**依赖**：C04、C06、B04。**建议提交**：`feat(brief): assemble editable claim and material plans from accepted proposals`

**文件**：`store/creation_flow.go`、`server/{creation_workspace,curator_workflow}.go`、`provider/curator.go`、新 Brief 模板与必要任务实现、迁移及测试。

- [ ] 替换当前只把 OwnerClaim 和材料 ID 填进空 Brief 的路径；复用 Curator 生成选用/放弃材料、文章结构、篇幅、风格、预计主张类型和待补问题。
- [ ] 用户可以调整主张、提纲、材料和个人笔记；保存为有版本的草案，确认时冻结确切版本与全部输入。
- [ ] 接受方向后的自动整理通过持久任务执行；确认时重新检查材料质量、stale、blocking ResearchNeed 和 Provider 策略。确认后的修改产生新草案，不继承旧写作授权。

**验收/验证**：`go test ./internal/store ./internal/server ./internal/provider ./internal/queue`；接受一次只整理一次，Brief 有实质可审阅内容；素材变化/新增缺口阻止确认，用户调整不丢失，确认绑定精确版本。

### C08 — 将新创作契约连接到现有文章存储

**性质**：兼容桥接。**依赖**：C07。**建议提交**：`feat(creation): link confirmed creation briefs to legacy article records`

**文件**：新增 `store/creation_article_links.go` 与迁移、`server/editorial_writer_workflow.go`、`store/{creation_flow,editorial}.go`、相应测试。

- [ ] 为 CreationProposal/CreationBrief 与 ArticleProposal/ArticleBrief 建立持久一对一映射，保存新工作流契约版本；旧 Article 记录仍可继续。
- [ ] 对明确确认的新 Brief 幂等建立需要的兼容 Article 记录，不要求 Owner 再创建并确认一套旧对象，也不把未确认方向迁成已确认主张。
- [ ] 在旧入口与导出判断中识别新契约：C09–C11 未具备时不允许新类型文章借旧 Writer/Review 路径获得错误的“就绪”状态。

**验收/验证**：`go test -race ./internal/store ./internal/server ./internal/backup`；重复桥接一个 Brief 只一组映射，旧文章可读可导出，新映射能恢复且不靠标题猜关系。此提交建立可验证连接，写作按钮在 C10 才接通。

### C09 — Writer 接受并输出有身份的主张

**性质**：生成契约升级。**依赖**：C08、A04。**建议提交**：`feat(writer): generate articles from typed owner and source claims`

**文件**：`provider/{types,writer,citation}.go`、`models/creation_workspace.go`、`store/claim_review.go`、新 claim 验证文件及测试。

- [ ] 新版请求包含确认的 OwnerClaim、SourceClaim、允许的 SynthesisClaim、已单独核验事实及引用、个人笔记快照；保留旧 Writer v1 的兼容契约。
- [ ] 输出正文与 ClaimMap；SourceClaim 归因到具体来源，OwnerClaim 必须对应已确认主张，综合明确为综合；禁止 Brief 外新主张和未给出的事实。
- [ ] 程序检查正文片段存在、材料成员身份、对应 Brief/版本及必需字段；独立语义审校留给 C11。新修订在通过 C11 前始终不可交付。

**验收/验证**：`go test ./internal/provider ./internal/store ./internal/evalset`；两种 Provider 的新契约都通过；未授权个人主张、伪造事实、正文不存在的映射被挡；旧 Writer 输出兼容不变。

### C10 — 从确认的 CreationBrief 启动可恢复 Writer

**性质**：接通成文。**依赖**：C09、B02、B04。**建议提交**：`feat(creation): run confirmed article writing through durable jobs`

**文件**：新增 `queue/article_writer.go`、必要的 `internal/creation/` 工作流 Module；`server/{creation_workspace,editorial_writer_workflow}.go`、routes、进度模板和测试。

- [ ] 用户确认写作方案后从新入口生成文章，读取 C08 映射和 C09 契约；HTTP 请求只保存意图，关闭页面不取消后台写作。
- [ ] 将需要共用的写作编排收口为无 HTTP 依赖的 Module，接受持久输入与 Provider；worker 不反向依赖 server。只迁出本任务必要行为。
- [ ] 复用已有领取/结果缓存思想，接入 B02；正文、ClaimMap、revision 与用量关联原子保存，重启后恢复，未知远端结果不盲目重写。

**验收/验证**：`go test -race ./internal/queue ./internal/server ./internal/store`；未确认 Brief 无调用，确认后有正文；断开请求后任务继续；并发点击、结果缓存后崩溃不会重复产出；旧写作入口继续工作。

### C11 — 依据主张身份执行独立审校

**性质**：审校契约升级。**依赖**：C10。**建议提交**：`feat(review): validate claim attribution ownership and evidence before delivery`

**文件**：新增 `provider/claim_reviewer.go` 与 Adapter 实现；`server/editorial_review_workflow.go`、相关持久任务、`store/claim_review.go`、publication readiness 实现与测试。

- [ ] 新契约审校实际检查来源归因、Owner 授权、综合边界、事实依据、Brief 外表达与未解决缺口；不直接复制 EvidenceReviewer 状态到 ClaimReview。
- [ ] 检查以确切修订和其 Brief/材料快照为输入；记录独立模型、提示版本、问题位置和用量，使用 B02 任务恢复。
- [ ] 保留 StyleReview 的独立职责；新文章交付要求对应修订的 ClaimReview 与 StyleReview 满足契约，旧文章使用旧兼容规则。

**验收/验证**：`go test ./internal/provider ./internal/store ./internal/server ./internal/queue`；把来源观点写成客观事实、把 AI 推论归给本人、审校过期修订、缺少风格结果均不能交付；合法个人判断有自身身份，不强迫伪造 KeyPoint。

### C12 — 修订文章时正确保留与失效主张映射

**性质**：修订闭环。**依赖**：C11。**建议提交**：`fix(revisions): preserve claim lineage and invalidate reviews after edits`

**文件**：`server/{editorial,editorial_revision_writer_workflow,revision_diff}.go`、`store/{editorial,claim_review}.go`、`templates/article_draft.html`、对应测试。

- [ ] 复用当前 ArticleRevision，不重写旧版本；手工编辑和 AI 修订都产生新 revision，AI 修订仍读取确认范围并走持久任务。
- [ ] 只有仍然存在且身份/证据关系未变化的正文片段可继承 ClaimMap；修改处重新校验，跨段搬移或重复句要能唯一定位。
- [ ] 新修订不继承旧审校通过；显示修改差异、待审问题和对应来源，保存冲突明确提示。

**验收/验证**：`go test ./internal/server ./internal/store ./internal/provider` 加浏览器修订；删改、挪段、复制同句、引入新观点和并发编辑都有明确结果；旧文及旧审校仍可回看。

### C13 — 发布包与创作历史闭环

**性质**：成文交付。**依赖**：C12、G08。**建议提交**：`feat(publication): export reviewed work and track exact revision history`

**文件**：`server/publication.go`、`store/{creation_history,editorial}.go`、`templates/{article_draft,publication_package}.html`、迁移与测试。

- [ ] 新版文章沿用 Markdown、富文本与纯文本发布包，按主张类型生成适当归因；导出时复查材料和审校有效性。
- [ ] 未发布作品及用户明确登记的已发布作品都关联具体 revision，写回 CreationHistory；导出不会触发外部发布或自动标 published。
- [ ] C05 可召回这些历史，已表达的同一主张与有新价值的后续文章分开判断；来源删除后保留历史身份，引用明确失效。

**验收/验证**：`go test ./internal/server ./internal/store ./internal/backup`；旧/新文章都能按各自契约导出；改稿必须重新审校；历史幂等且可恢复；用实际目标编辑器粘贴一次富文本，记录格式和归因结果。

## 11. U：学习与创作的日常界面

### U01 — 首页并列继续学习与继续创作

**性质**：信息组织。**依赖**：D07、G07、C10。**建议提交**：`feat(home): surface next learning and writing actions together`

**文件**：`server/{handlers,attention}.go`、`store/attention_queue.go`、`templates/{dashboard,attention,layout}.html`、`static/app.css`。

- [ ] 复用 AttentionQueue，首页呈现新重点、继续听、待处理问题，以及精读文、候选、Brief、写作/审校中的文章。
- [ ] 面向使用者使用“我的主张、写作方案、待补资料”等清晰文字；Provider、schema 和内部类型名移到需要排查时的详情。
- [ ] 收敛导航，保留旧 URL 和入口兼容；失败、预算阻断和证据失效不得因首页简化而消失。

**验收/验证**：`go test ./internal/server ./internal/store` 加浏览器；空数据、仅学习、仅创作、两者并行、多个失败状态都能找到下一步；页面 GET 无付费行为。

### U02 — 在学习成果和文章之间双向导航

**性质**：复用与回溯。**依赖**：U01、C13、D08。**建议提交**：`feat(workspace): link notes materials digests and article revisions`

**文件**：`server/{source_handlers,knowledge,editorial}.go`、相关只读 store 查询、单集/重点/精读/文章模板与测试。

- [ ] 单集与重点显示被哪些精读文/文章采用，文章展开具体来源和本人笔记；跳转准确版本并保留当前编辑上下文。
- [ ] 学习可直接转入选择素材，创作缺口可返回相关来源继续学习；不要求复制 ID 到表单。
- [ ] 一次采用关系只保存或投影一次，不为 UI 复制另一套主张/素材真相。

**验收/验证**：`go test ./internal/store ./internal/server` 加往返流程；多集、多修订、多篇文章不串关系；无证据或被 Purge 项显示失效而不是空白链接。

### U03 — 完成手机布局与基本可访问性检查

**性质**：使用质量。**依赖**：U02、D06。**建议提交**：`fix(ui): make learning playback and writing usable on mobile`

**文件**：`static/app.css`、受影响 templates；`docs/acceptance/mobile-workspace.md`（新）。

- [ ] 检查主要视口的导航、播放控件、长文本、引用、笔记和文章表单；修复遮挡、横向溢出和状态信息不可见。
- [ ] 检查键盘焦点、控件标签、加载/错误反馈、颜色之外的身份标记；不中断用户正在输入的文本。
- [ ] 截图/录制可复核场景，记录平台和浏览器；复用现有样式体系，不在此任务重新设计品牌或引入 UI 框架。

**验收/验证**：主要学习→记笔记→精读、构思→文章、DJ 路径在手机与桌面完成；模板渲染测试通过。仅样式修改不新增大量字符串断言。

## 12. V：恢复、真实旅程与交付

### V01 — 验证全部新关系的备份与恢复

**性质**：跨任务恢复验收。**依赖**：U02。**建议提交**：`test(backup): verify full learning DJ and creation recovery`

**文件**：`internal/backup/backup_test.go`、`internal/store/backup_test.go`、`docs/acceptance/recovery-run.md`（新）；仅修复实际发现的最小问题。

- [ ] 构造含旧数据和新版本血缘、笔记、DJ 清单与进度、digest 修订、构思轮次、Brief 映射、ClaimMap 和历史的真实 SQLite 备份。
- [ ] 在全新 DATA_DIR 恢复并启动，逐一验证引用与旧记录可读；Narration 不在核心备份中时展示缺失且可按清单重生成。
- [ ] 验证中断任务恢复、Purge/失效传播与旧迁移升级。各数据卡已做本地恢复测试，本任务验证它们组合后的结果。

**验收/验证**：`go test ./internal/backup ./internal/store` 加新实例手动检查；保存来源/产物关系核对表，不能只比较数据库行数。发现新缺陷应另拆原子修复提交后重跑相关恢复路径。

### V02 — 执行完整的真实学习与成文旅程

**性质**：产品验收。**依赖**：K05、U03、V01。**建议提交**：`docs(acceptance): record real learning DJ and article journeys`

**文件**：新 `docs/acceptance/2026-09-learning-dj-creation-run.md`、EvalSet 结果；必要的最小回归样本。

- [ ] 选至少两集不同来源，包含中文长访谈和英文节目；新增订阅自动处理，检查重点与账单。
- [ ] 完成一轮真实 DJ，包含暂停、跳段、弱网、关页恢复及听播记笔记；中文音色与原音切换须实际试听。
- [ ] 从一集生成精读文，调整、处理来源、导出；从多集素材与个人判断形成 Brief，生成文章、编辑、审校、导出并登记历史。
- [ ] 验证新候选对已写文章的重复提醒、继续学习与创作的首页入口；核对模型调用与实际可得账单/用量。

**验收/验证**：除 K05 的学习门槛外，要求无错挂原音、无混淆个人与来源主张、两条文章路径无手填内部 ID/改数据库步骤；文章人工可用性至少 4/5。费用、时长、设备限制和所有未完成项明确记录。真实外部验收缺失时不能勾选本任务。

### V03 — 完成部署交接与首轮验收收口

**性质**：交付文档与发布准备。**依赖**：V02。**建议提交**：`docs(release): document personal learning and creation operations`

**文件**：`README.md`、`docs/{deployment,implementation-roadmap,handoff}.md`、相关生产部署说明、配置样例、本计划任务状态。

- [ ] 更新安装、处理深度、TTS 预检、预算、故障恢复、导出、备份与回退说明；新能力与实际限制一致。
- [ ] 跑完整现有 CI 门禁，确认没有新增关键问题；未完成的后续 X 任务明确保留，不混入首轮“已完成”。
- [ ] 汇总任务 SHA、迁移序列、验收证据和发布说明；实际部署或发布按后续明确指令执行，本计划不自动部署。

**验收/验证**：完整 CI、真实旅程、恢复证据均可追溯；从干净实例按文档能配置并使用两条闭环，所有已交付任务都有独立提交记录。

## 13. X：后续扩展任务

以下卡互相独立，可以按实际使用反馈选择；不计入首轮完成条件。

### X01 — 显式兴趣与已听反馈

**性质**：个性化基础。**依赖**：V03。**建议提交**：`feat(preferences): rank listening candidates with explicit interests`

**文件**：新个人偏好 model/store/server/template、进度查询与测试。

- [ ] 保存主题兴趣、避开主题、时间偏好以及已听/跳过反馈；用户能查看和修改，兴趣不与创作画像强制共用。
- [ ] 首版用可解释规则排序，不声称训练了个性化模型；反馈只影响推荐，不删除证据或改变来源重点本身。

**验收/验证**：偏好改变可解释地调整排序，已听减少重复推荐；无反馈时保留稳定默认结果；迁移、恢复和用户覆盖测试通过。

### X02 — 跨集每日 DJ

**性质**：新产品能力。**依赖**：X01、D08。**建议提交**：`feat(dj): assemble daily cross-episode listening mixes`

**文件**：扩展 DJ 编排 Module、调度与清单查询；新增每日清单页面和测试。

- [ ] 从多个 Source 的未听高光按兴趣、重复度、信息增量和时间预算选材，生成带节目介绍的跨集串场；复用同一播放与笔记能力。
- [ ] 设每日幂等清单与可见费用，内容不足允许少选；每段能回原集，个人文章仍沿 C 流程生成。

**验收/验证**：跨集不串证据、没有已听内容反复占满、目标时长含串场、同日重复调度不重复生成；用一周真实收听记录评估价值。

### X03 — 按需翻译与双语学习

**性质**：新衍生产物。**依赖**：V03、B01。**建议提交**：`feat(learning): add versioned translations linked to original segments`

**文件**：新 translation Provider/任务/产物与双语模板；沿用快照和费用记录。

- [ ] 原文与译文并列，译文指向原文 Segment，单独版本化；翻译不覆盖转录，金句原文仍可核对。
- [ ] 按需处理并缓存；长节目分段恢复，语言和 Provider 可配置。

**验收/验证**：人工检查专名、数字和限定条件；原文切换使旧译文明确保留旧来源或标 stale；双语导出身份完整。

### X04 — 概念级检索与可选知识视图

**性质**：按实际召回缺口扩展。**依赖**：V03、A04。**建议提交**：`feat(search): improve concept retrieval with evaluated embeddings`

**文件**：`store/semantic_search.go`、新增可替换 embedding Adapter、索引版本与查询测试；知识视图另行子卡。

- [ ] 先用同义表达、中英文跨语言、概念关联查询建立真实检索基线；仅在当前 n-gram 有明确缺口时接入经官方文档核实的 embedding 模型。
- [ ] 保存模型/维度/索引版本，后台重建不影响旧索引；保护 ModelDataPolicy，混合召回仍返回可核对引用。
- [ ] 若增加思维导图，作为 `X04-b` 独立提交：由已有章节/重点和来源关系生成，不将“图里连线”宣称为已证明的知识关系。

**验收/验证**：同一评测集的召回质量改善且延迟/费用可接受；旧索引可用、重建可恢复，引用与来源限制保持正确。未有实证收益时保留现有检索并记录结果，不为实现扩展而扩展。

## 14. 首次开发建议与任务记录

首次开发从 **A01** 开始；随后整理 A02/A03 的已有成果并取得可检出的共同基线。第一项新的行为修复建议是 **B01 来源快照**，因为精读文、DJ 和个人文章的可回溯性都依赖它。

如希望尽快看到单集成文改善，在完成 B01–B04、K04 后可优先执行 G01–G06，再回到 B05–B09/K 组其余任务；这不会重复建设另一套基础能力。首次完整演示仍需满足对应里程碑全部依赖。

每项任务采用以下记录，保存到 `docs/implementation-log/` 中对应任务文件，或本节追加。任务卡自身的复选框与状态一起更新；不提交空的占位记录。

```text
任务：B01
状态：未开始 / 开发中 / 代码已提交待外部验收 / 已验收 / 外部阻塞
依赖基线：SHA + 已完成任务
实施范围：本次实际行为与文件
提交：SHA + commit subject
验证：命令、结果、样本/设备/Provider 版本
兼容与恢复：迁移号、旧数据行为、恢复验证
剩余事项：明确问题；没有则写无
```

如果一项任务的全部验收在开始前已经由当前代码及仍适用的证据支持，记录“既有实现满足”及证据，不制造空提交。若某次任务产生新的 material 依赖或必须分拆，先更新依赖图与子卡，然后继续执行；无需为普通实现细节反复确认。
