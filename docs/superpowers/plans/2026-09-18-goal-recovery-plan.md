# 学习、DJ 与创作闭环：审计后的开发计划

日期：2026-09-18。代码基线：`dc0cefb7c07637eff649e6884682ada0d1b6c086`。状态：执行中（R00–R23 已完成实现与自动验证；R24–R26 待执行）。

本计划接替 2026-09-12 原子计划的执行顺序与完成状态；产品范围和 ADR-0022/0023/0024 不变。用户已授权按本计划开发，要求在 Herdr 新 tab 使用 `pi --provider woyin --model z.ai/glm-5.3-flash` 执行。目标仍是个人版 Podwise：自动获取可信重点、DJ 精听和记笔记，并继续生成可追溯的单集精读文或跨集个人文章。

## 1. 当前事实与完成定义

原计划的“50/53 完成”不能作为交付依据。当前有大量数据结构、独立函数、页面和单元测试，但存在生产入口未调用、结果未消费和跨层契约不一致。保留已工作的 RSS、转录、知识卡、旧文章、基础播放和导出，针对实际断点补齐。

2026-09-18 本轮实测：现有全量测试、vet、临时目录构建通过；cover-gate 失败，evalset 94.0%、provider 86.7%、queue 77.3%、store 75.2%。九个临时契约反例复现：预算漏本次估算、重复预占、日限额首任务被拒、笔记输入漂移、精读修订丢快照、claim_writing 未分派、审校状态不兼容、续听 JSON 不兼容、质量通过不更新正式素材。临时反例应转为稳定仓库回归用例，而非依赖临时文件路径。

其他已确认断点：DJPlan 没有生产入队和页面消费者；新 Brief 确认没有调用文章桥接和写作入队；Writer 不保存正文；候选批次没有完成路径；新 Brief 仍是主张和材料 ID 的占位对象。构思、研究缺口、发现历史召回、修订映射及精读多阶段恢复须补查入口到结果的完整链路。

每项只有满足以下条件才可记为“完成”：

1. 从真实 HTTP/调度入口可以到达实现，持久任务由真实 worker 分派；数据结果能被页面或下一阶段消费。
2. 规定的正常、失败、重复、恢复和旧数据兼容行为都有对应证据。
3. 测试验证可观察行为，不以 HTML 子串、函数存在或直接调用孤立 helper 替代整条链路。
4. 实现、回归测试及该任务执行记录共同形成一个原子提交。数据迁移同时覆盖读取、备份和 Purge/失效。
5. “实现与自动验证完成”“浏览器验证完成”“真实内容质量通过”分别记录。真实外部验收无结果时保持待验收，禁止用 Fake Provider 或历史状态标签代替。

## 2. 执行规则

- 顺序领取下面任务；可以将过大的任务拆成 `Rxx-a/b`，先把边界、依赖和验收写清楚，再逐项提交。不得跨多个业务能力做一个大提交。
- 不改已应用迁移；后续新增迁移沿当前最大序号分配。不迁框架、数据库或多用户模型；不实现旧计划 X01–X04。
- 单 Owner 确认主张与写作方案的授权边界不变。模型不得静默补事实、自动发布或从旧 Writer/Review 路径绕过新契约。
- 无需为普通实现选择再次请求确认。保留他人改动；显式暂存本任务文件或 hunk，不使用 `git add .`。不推送、不部署、不发送外部消息。
- 不读取或输出密钥。开发测试使用 Fake Provider、httptest、本地 fixture 和 scratch DATA_DIR；真实付费生成必须遵循既有按次授权，不能因开发授权扩大外部消费。可用本地 TTS 做真实音频验证。
- 本轮使用 `fix/learning-creation-recovery` 开发分支（已存在时核实归属后复用），保持当前工作目录和 Herdr tab，不影响其他项目或已有 pane。不自动合并到主分支。
- 每提交执行涉及包的 `go test`、`go vet`、gofmt、`git diff --check`，并临时路径构建。共享路由、schema、队列契约变更扩大至全量测试；队列/CAS/事务执行对应包 `-race`。里程碑或最终交付执行全量 race、vet、build、cover-gate、lint。不得降低门槛、增加覆盖率豁免、禁用测试来完成任务。
- 执行记录写入 `docs/implementation-log/2026-09-recovery.md`：任务、已交付行为、验证命令及结果、实际限制。对应提交 SHA 在下一次文档更新时补齐，避免为记录自身 SHA 无穷改提交。
- 验证证据适用的代码未变时可继承；发现新失败或修改相关行为才重跑。不要为“更有把握”无限重复验证。

## 3. 任务总表

所有任务初始待办，旧任务号仅表示追溯关系，不继承其完成标签。

| 任务 | 交付行为 | 对应旧任务 | 依赖 |
| --- | --- | --- | --- |
| R00 | 修订状态与执行记录 | A/V | 无 |
| R01 | 将本次估算纳入预算 | B04 | R00 |
| R02 | 预占、恢复和结算幂等 | B02/B04 | R01 |
| R03 | 正确执行自动日限额 | B04/B08 | R02 |
| R04 | 入队配置快照真正用于执行 | B01/B02 | R02 |
| R05 | 质量判定接入自动素材链路 | K02/K03 | R03/R04 |
| R06 | 精读输入冻结笔记内容与来源 | G01/G07 | R04 |
| R07 | 精读修订保留证据血缘 | G03–G05 | R06 |
| R08 | 精读检索、补织恢复与独立改写 | G02/G06 | R07 |
| R09 | 生产 DJ 清单编排与页面消费 | D02 | R04/R05 |
| R10 | 串场脚本合成和播放 | D03/D04 | R09 |
| R11 | 续听契约、正确定位与并发保存 | D07/D08 | R10 |
| R12 | 选定素材进入可继续的构思会话 | C01/C02 | R05/R06 |
| R13 | 多轮诊断与候选提升 | C03 | R12 |
| R14 | 研究缺口绑定具体依据并传播失效 | C04 | R13 |
| R15 | 新素材种子、历史召回及结果验证 | C05 | R05/R04 |
| R16 | 候选决策原子完成批次 | C06 | R13/R15 |
| R17 | 可审阅、可编辑、可确认的写作方案 | C07 | R14/R16 |
| R18 | 确认方案建立持久文章映射 | C08 | R17 |
| R19 | Writer 入队到正文修订原子落库 | C09/C10 | R18/R04 |
| R20 | 独立主张审校与交付门禁 | C11 | R19 |
| R21 | 手工/AI 修订保留主张身份并失效审校 | C12 | R20 |
| R22 | 发布包、准确历史与双向导航 | C13/U01/U02 | R21/R08/R11 |
| R23 | 两条完整旅程、恢复及旧数据回归 | V01/V02 | R22 |
| R24 | 修复现有覆盖率与静态门禁 | V03 | R23 |
| R25 | 真实浏览器交互与移动布局 | U03/D06 | R24 |
| R26 | 真实内容质量、发布前证据与文档 | K05/V02/V03 | R25 |

## 4. 逐任务开发卡

### R00 — 纠正交付状态，建立可追踪执行记录

文件：本计划、旧计划头部、`docs/implementation-log/2026-09-recovery.md`、相关验收记录的状态说明。

- 保留旧记录为历史，明确其代码版本、实际覆盖范围和本轮替代关系；不删除失败证据。
- 将旧计划完成总数撤回为历史自报状态，不把全部旧任务重新实现；本计划只补断点和未满足契约。
- 建立分支与任务状态，确认工作区没有未知改动。

验收：文档链接有效；状态与本次审计一致。提交：`docs(plan): replace completion claims with recovery tasks`。

### R01 — 预算检查计入本次预占

文件：`internal/store/budget.go` 及其测试。

- 在同一事务内判断已知费用、有效在途预占与本次预估之和；明确恰好等于上限可执行，超过则拒绝。
- 检查负值、无预算配置与未知费用既有语义，不把 unknown 伪装为零成本。

验收：5 分预算拒绝 1000 分预估；允许额度内首次任务；并发预占不能穿透上限。`go test -race ./internal/store`。提交：`fix(budget): include requested reservation in limit checks`。

### R02 — 预占在任务恢复时幂等

文件：`store/budget.go`、`queue/worker.go`、用量结算和恢复测试。

- 同一 job 已有预占时按持久状态复用或明确拒绝不合法转移；不重复 INSERT，不覆盖已结算金额。
- 远端调用前失败、已获得结果、远端结果未知三种路径分别释放、结算或保留待处理，不假定未知调用免费。
- 在预占后、响应缓存后、业务落库后分别模拟重启；一个逻辑调用不重复预占或计费。

验收：重复领取可继续，不出现唯一约束错误；缓存结果恢复不重复调用 Provider；结算和释放重复请求幂等。提交：`fix(queue): resume jobs with idempotent budget reservations`。

### R03 — 自动日限额按执行资格计数

文件：`store/budget.go`、自动任务领取/处理流程及必要迁移。

- 定义限额统计对象和一天的边界，区分自动处理意图与后续分析/解说子任务，避免同一集每一步重复占名额。
- 额度获取与执行资格原子关联，当前未获准任务和排队数量不能提前耗尽额度；重试同一意图不重复计数。
- 暂时因额度不足的任务显示可理解状态，并能在符合现有调度规则时继续，不能把整批永久失败。

验收：限额 1 时首个意图可完成，第 2 个被限制；并发、跨日、同任务重试、手动任务均符合契约。提交：`fix(ingestion): enforce daily limits on admitted processing intents`。

### R04 — 执行使用冻结的配置和来源

文件：`queue/worker.go`、`store/job_execution*.go`、各任务入队与输入快照、Provider 选择。

- 新任务入队冻结 Provider、模型、提示/契约版本、处理深度、明确来源版本；worker 读取持久快照，不重新用当前 settings 替代。
- 保留动态可撤销的来源访问策略检查；冻结输入不是绕过 LocalOnly、失效或 Owner 撤销授权的理由。
- 明确旧任务的兼容路径与 legacy 标记；不伪造它从未保存的历史配置。

验收：排队后修改设置/重分析不会改变该任务输入；策略撤销仍能阻止外发；子任务正确继承意图与必要配置。提交：`fix(jobs): execute persisted provider and source snapshots`。

### R05 — 自动质量判断形成可发现素材

文件：`queue/worker.go`、`queue/keypoint_quality.go`、`store/keypoints.go`、`store/creation_workspace.go`。

- 分析索引完成后幂等入队质量判断；`knowledge` 和 `knowledge_dj` 均获得质量步骤，DJ 不依赖质量全部通过。
- 同一事务消费匹配指纹/版本的判定，更新正式质量状态并写一次 MaterialChange；旧结果不得批准新内容。
- OwnerConfirmed、OwnerExcluded/Dismissed 和人工修改不被自动判定覆盖；失败仍可读且可重试。

验收：从正常分析入口一路执行到 Ready 并可参与发现；重跑不增加变化；错误证据不获准；过期判定与 Owner 决策测试。提交：`fix(keypoints): connect quality jobs to usable material state`。

### R06 — 精读冻结完整笔记输入

文件：`store/digests_enqueue.go`、`queue/digest.go`、笔记快照类型/存储。

- 入队保存选中笔记的确切文本、类型、版本和锚点，明确空选择与旧数据未记录选择的区别。
- 运行时只消费此次输入；编辑、删除或新增笔记不改变已入队任务，失效的引用显式处理。
- Episode、Upload、Document 使用正确的不可变来源读取路径。

验收：排队后编辑/新增/删除笔记不改变输入；空选择保持为空；历史文章保留所用文本；备份可恢复。提交：`fix(digests): freeze selected note contents at enqueue time`。

### R07 — 精读修订继承快照与确认关系

文件：`store/digests.go`、`server/digest.go`、readiness/引用/来源剔除路径。

- 编辑标题或正文派生新修订，保留父版本、来源快照、仍适用的检索来源确认和事实缺口。
- 只让被修改或剔除的事实失效，不将整篇无条件变为 legacy，也不错误继承不再适用的通过状态。
- 保存后跳转新修订；旧版本仍可查看和回溯，当前指针仅在有效提交后切换。

验收：标题编辑保留引用与就绪状态；来源剔除只改变新版本；确认关系和缺口继承可解释；GET 展示刚保存的版本。提交：`fix(digests): preserve provenance across derived revisions`。

### R08 — 精读阶段恢复与渠道改写

文件：`queue/digest.go`、`queue/digest_rewrite.go`、相关 store/job checkpoint。

- 对 compose、逐项检索落源、补织结果分别保存阶段输入和输出；重试从已知断点继续。
- 远端结果未知不得无限重试；阶段失败不发布半成品，已落源结果不重复创建 Document。
- 渠道改写只消费指定主文修订，失败重试不重做主文或其他渠道，保留事实类型和引用门禁。

验收：每个阶段返回后模拟重启，不重复 Provider 调用或落源；仅重试渠道时主文不变。提交：`fix(digests): resume search and composition from checkpoints`。

### R09 — DJPlan 接入真实生产流程

文件：`queue/dj_plan.go`、高光/解说衔接、`server/source_handlers.go`、`templates/dj.html`、播放器。

- 高光产物就绪后按已冻结处理意图幂等编排；页面读取确切 DJPlan 及其来源快照，不能用“当前高光”冒充清单。
- 目标时长、段落顺序和选择理由实际影响播放项目；无解说也能播放原音。
- 旧来源没有 DJPlan 时明确兼容状态或提供生成动作；GET 不触发模型调用。

验收：从手动/自动 `knowledge_dj` 入口出现可播放清单；`knowledge` 不生成 DJ；更新当前转录不篡改旧计划。提交：`fix(dj): enqueue and render persisted listening plans`。

### R10 — 开场、过渡、收尾真正进入音频队列

文件：`queue/dj_plan.go`、Narration 持久化/合成、音频路由、DJ 前端。

- 消费 ScriptText，为开场/过渡/收尾和 gist 保存明确身份的合成结果；音频绑定计划版本和脚本指纹。
- UI 持续区分 AI 解说与原音；合成失败可跳过到下一原音，局部重试不重新生成所有解说。
- 复用现有 TTS 配置，不让 Narration 进入 EvidenceAudio 或作为引用证据。

验收：合成请求和实际播放项目一致；部分失败仍连播；旧计划音频不被新计划覆盖；本地 TTS 可用时验证真实 wav。提交：`feat(dj): play versioned intro transition and outro narration`。

### R11 — 修复续听 JSON、项目定位和并发保存

文件：`models/dj.go`、`server/listening.go`、`store/listening_progress.go`、`templates/dj.html`/播放器。

- 定义并测试 GET/POST 一致的 JSON 契约；按稳定计划项目身份恢复，不混用“含解说的索引”和“仅原音的索引”。
- 恢复一次加载并播放后续队列，移除重复 play 导致的竞态；偏移量语义明确，恢复倍率，越界位置按规则处理。
- 存储使用原子 CAS/版本策略处理倒序、同时首次保存、多标签请求；清单变化不能静默跳错片段。

验收：真实 API 保存读取、解说与原音交错时恢复第 2 段、刷新后点击继续、暂停/跳段/重复点击、并发保存。提交：`fix(player): restore progress using stable plan item identities`。

### R12 — 素材选择成为构思输入

文件：`server/creation_workspace.go`、material selection store、构思轮次及模板。

- 两集重点和个人笔记选择后进入同一构思意图；选中范围和内容版本持久保存，首次 GET 不付费。
- 轮次保存上一轮身份和约束变化；刷新可继续，重复提交幂等，旧结果不覆盖新一轮。
- 材料资格、OwnerExcluded、stale、Provider 策略失败有明确反馈，不静默改变用户选材。

验收：经真实路由选材到会话；刷新、返回修改、重复提交和过期并发均保留预期输入。提交：`fix(ideation): carry selected materials into persistent sessions`。

### R13 — 多轮诊断和候选提升入口

文件：`queue/ideation*.go`、Provider 诊断契约、creation handler/模板/store。

- 模型读取已冻结前轮与最新约束，并限制输出引用在材料集合内；空材料和矛盾材料形成缺口。
- 展示支持、反驳和建议主张；Owner 选中某条诊断候选后幂等创建关联轮次的提案，尚不承担 OwnerClaim。
- 旧轮次输出可读；过期结果只能记录到其原轮次。

验收：两轮输入确实不同且含前文；伪造引用被拒；按钮能提升实际候选，重复动作一个提案。提交：`feat(ideation): promote grounded multi-turn diagnoses to proposals`。

### R14 — 研究缺口用具体依据解决

文件：`store/creation_flow.go`、ResearchNeed schema/handler、Purge 和 readiness。

- 保存 SourceType、ID、版本、具体材料/位置及 Owner 确认；仅 Source 存在或 processed 不足以 resolved。
- 验证依据属于该来源版本且可用；保持“Owner 认为缺口已解决”和“机器证明事实为真”的区别。
- 删除/失效依据重新阻断待确认 Brief 和写作；已成文标待复核。

验收：错误 Source、跨来源 Segment、旧版本、无具体依据均拒绝；有效依据解除阻断，Purge 后恢复阻断。提交：`fix(research): resolve gaps with versioned owner-confirmed evidence`。

### R15 — 发现使用历史材料且验证新增价值

文件：`server/automatic_discovery.go`、`store/semantic_search.go`/creation history、Scout。

- 当前变化为种子，有限召回历史相关素材和准确作品历史；逐项遵守 Provider 策略，不发送全库。
- 输出材料只能来自发送快照，包含当前窗口新价值并满足跨集要求；空结果可解释，不凑数。
- 同义候选与相同已表达主张去重；高置信 HardDuplicate 与仅疑似相似分开，有新证据的 FollowUp 可保留。

验收：当前窗口+历史输入都可观察；纯历史重组、伪造 ID、同义重复被挡；新论据和 LocalOnly 排除场景。提交：`fix(discovery): ground new proposals in bounded historical context`。

### R16 — 所有候选完成决策后释放批次

文件：`store/proposal_batches.go`、`store/creation_flow.go`、creation handlers/模板。

- 接受、保存以后、拒绝的决策和批次剩余数在同一事务处理；最后一个决策自动完成批次。
- 并发重复动作幂等，拒绝原因/编辑后接受保留来历；保存的候选仍参与去重。
- 下一次符合条件的新变化可触发发现，但不因保存/拒绝立即按旧库存补货。

验收：未处理一条仍背压，最后一条处理后释放；并发两项不会卡死或提前完成；新窗口能实际生成。提交：`fix(proposals): complete batches atomically after owner decisions`。

### R17 — 形成实质性写作方案

文件：Curator adapter、durable brief job、`store/creation_flow.go`、Brief 编辑页。

- 接受主张后幂等入队整理任务；复用 Curator，输出提纲、选用/放弃材料、风格、篇幅、主张类型与未决问题。
- 可编辑并版本化保存主张、提纲、材料和笔记；确认精确版本，修改后不继承旧授权。
- 确认前重检来源资格、研究缺口、Owner 主张和 Provider 策略；所有调用走预算与恢复契约。

验收：从接受提案到可编辑方案的完整路由；刷新不丢内容、并发编辑冲突、过期版本/缺口阻止确认。提交：`feat(briefs): assemble and confirm editable versioned writing plans`。

### R18 — 确认新方案后幂等连接旧文章实体

文件：`store/creation_article_links.go`、确认 handler、旧 Writer 授权检查。

- 确认新 Brief 的实际路径调用持久桥接，绑定精确版本；重复确认只一组关系。
- 未确认/过期草稿无法桥接获得写作授权，新文章不能借旧入口绕过 ClaimReview；保留旧文章兼容。

验收：真实确认 HTTP 后查询持久映射；刷新重试不重复；备份恢复、旧文章读取/导出行为保留。提交：`fix(creation): link confirmed briefs through the production entry point`。

### R19 — 持久 Writer 保存完整文章修订

文件：`queue/claim_writer.go`、worker dispatch、creation routes、article/ClaimMap store。

- 确认方案后按既有授权自动启动或明确生成动作入队；关闭页面不取消。将 JobClaimWriting 加入预算、配置和分派契约。
- 验证主张身份、素材成员和正文映射；正文、真实 revision ID、ClaimMap、输入快照和结果引用事务落库。
- Provider 响应先缓存再业务提交；重启复用缓存，不能仅存长度，也不能把 draft ID 当 revision ID。

验收：从新入口确认到非空正文页面；无确认零调用、重复点击一个任务、结果返回后重启只有一个修订；旧 Writer 继续工作。提交：`fix(writer): persist claim-aware article revisions through durable jobs`。

### R20 — 新审校真正控制交付

文件：`provider/claim_reviewer.go`、`store/claim_review.go`、queue/review handler、publication readiness。

- 统一 Provider、存储、页面状态枚举，独立语义审校消费确切正文修订/Brief/来源快照。
- 新文章要求本修订的 ClaimReview 和 StyleReview；旧 EvidenceReview 不能替代，旧文章仍用兼容规则。
- 审校入队和重试有真实入口、预算与恢复；失败、缺失、过期均禁止交付。

验收：合法通过可保存，归因混淆/未授权主张/过期审校/缺少风格结果不能导出；仅旧审校通过也不放行新文章。提交：`fix(review): enforce claim and style review for exact article revisions`。

### R21 — 修订保留有效映射并强制重新审校

文件：手工/AI revision handler、ClaimMap inheritance、revision writer job。

- 实际保存路径调用映射继承/校验；只继承仍可唯一定位且身份未变的片段。
- 新观点、重复句歧义、删除/挪段明确处理；新修订不继承旧通过状态，旧修订仍可回看。
- AI 修订通过持久任务使用原授权范围，手工并发编辑提供冲突结果。

验收：从页面保存或 AI 修订到新版本，修改后导出阻断，重新审校可恢复；重复句/并发/缓存恢复用例。提交：`fix(revisions): preserve valid claim lineage and invalidate stale reviews`。

### R22 — 发布包、历史与学习创作往返

文件：publication handlers/readiness、creation history、dashboard/source/workbench/article 模板。

- 按新旧契约导出 Markdown/富文本/纯文本，复查来源有效性和准确审校版本；导出不自动登记 published。
- 创作历史绑定确切 revision，明确 Owner 登记发布动作；精读与文章历史参与发现去重。
- 学习来源→素材/笔记→方案/文章、文章→实际来源/笔记可导航；首页显示任务失败、继续听和继续写的真实下一步。

验收：两集引用不串、旧修订跳转准确、无效来源明确提示；导出后未发布，主动登记后历史幂等且可恢复。提交：`fix(workspace): connect reviewed exports history and source navigation`。

### R23 — 固化完整旅程与恢复测试

状态：实现与自动验证完成；真实浏览器与真实内容质量分别留在 R25、R26 验收。

文件：server/queue/store/backup 集成测试、有限 fixtures。

- 旅程 A：订阅新集→转录→重点质量→DJ 清单/解说→续听/笔记→精读→修订→导出。
- 旅程 B：两集素材+个人理解→构思多轮→接受主张→方案修改确认→Writer→审校→修订再审→导出→历史。
- 从真实 router/调度入口发起，驱动实际 worker；只替换外部 Provider/媒体依赖，不绕过业务入口、预算、队列和数据库。
- 覆盖重启恢复、备份恢复、Purge 失效及旧文章兼容；不能只直接调用新 helper 然后宣称链路完成。

验收：两个确定性旅程与至少一个中途重启、一次恢复后继续通过；当前已修复反例成为永久回归。提交：`test(journeys): cover learning and writing through production boundaries`。

### R24 — 质量门禁回到真实通过

文件：evalset/provider/queue/store 的行为测试，必要最小可测试性修复。

- 先生成覆盖报告，针对未覆盖的实际契约、错误响应、恢复、状态转移补测；不写只重复实现的断言，不为覆盖率删除功能。
- 按现有脚本满足 evalset/provider/queue 95%、store 78.4%、server 79.4% 及其余既有要求；禁止下调门槛、扩大豁免、隐藏失败。
- 可按包拆 `R24-a/b/c/d` 原子提交；任何额外生产修复必须有具体失败案例。

验收：`go test ./... -race`、`go vet ./...`、临时构建、`make cover-gate`、`make lint` 全部通过。提交：`test(<package>): cover recovery and failure contracts`。

### R25 — 浏览器真实操作验收

文件：必要的前端修复及 `docs/acceptance/2026-09-recovery-browser.md`。

- 用 scratch DATA_DIR 和 Fake Provider 支持的真实应用操作桌面、375px 和 360px 两条旅程；记录版本、浏览器、视口、步骤和结果。
- 验证 DJ 媒体时间、暂停/缓冲/倍速/跳段/续听、笔记不中断、方案编辑/确认、审校门禁和导出；不能用静态字符串替代。
- 可用媒体夹具与本地 TTS，清楚区分模拟 Audio 状态机、真实媒体播放、移动视口和手机真机。没有设备不宣称锁屏/后台播放通过。

验收：可复核操作记录；发现问题回对应任务最小修复，再重测受影响场景。提交：`test(browser): verify learning and creation workflows interactively`。

### R26 — 真实质量与最终交付记录

文件：质量与真实旅程验收记录、使用文档、恢复计划状态。

- 执行前核实实际可用环境和既有按次授权；真实中文长访谈与英文信息密集节目必须覆盖多窗归并，记录输入快照、模型/提示版本、用量和限制。
- 评估重点覆盖、引用支持、重复、限定条件、高光完整性及两类文章可读性；Owner 的主观评分单独待确认，不冒充人类验收。
- 无凭证、无预算授权或无手机设备时，仅将对应外部验收明确记为待办，仍完成可执行的开发/自动测试/桌面浏览器任务。外部条件不足不是遗漏代码接线的理由。
- 最终记录实际完成任务/提交/门禁、真实验收缺口、迁移与备份恢复步骤；不自动部署、推送或发布文章。

验收：每项结论可追溯到当前代码适用的证据，遗留清晰且可执行；不能写“全部目标完成”掩盖未完成的真实验收。提交：`docs(acceptance): record verified delivery and remaining external checks`。

## 5. 里程碑

- M1（R01–R05）：自动处理受预算约束且产生可信可用素材。
- M2（R06–R11）：精读可追溯、DJ 新清单可听可续。
- M3（R12–R22）：从选材/个人想法到经过审校的文章与历史闭环。
- M4（R23–R25）：完整入口回归、恢复和浏览器交互通过，自动门禁全绿。
- M5（R26）：真实质量与设备验收有证据；未具备条件的项目保持明确待验收。

不使用功能文件数量或提交数量计算产品完成率。每个里程碑以其用户可观察结果为验收对象。
