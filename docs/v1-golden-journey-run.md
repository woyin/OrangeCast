# V1 黄金旅程实录与摩擦清单

状态：待开始（2026-08-14 建立）
实例：本地 `data/cloudwisepod.db`（320 个 Episode 候选、19 个旧管线 Transcript、0 KeyPoint、0 EditorialProfile）
对照：`product-goal.md`「V1 黄金旅程」10 步；冻结范围、DoD、交战规则与排序方法见 `implementation-roadmap.md`「V1 验证冲刺」。

## 旅程参数

- 播客：（待填——Owner 内容熟悉且仍在更新）
- 新处理集数：（待填，目标 3–5 集，批量入队 + IngestionPolicy）
- EditorialProfile 与预算：（待填——单篇/月度上限、角色模型、提案节奏）

## 已知缺口（旅程开始前登记）

1. 19 个旧管线 Source 有 Transcript/KnowledgeCard 但无 KeyPoint，代码无回填命令；本次不回填、不重新处理。它们对 Scout 不可见。
2. 覆盖率债务：`internal/store` 78.4%、`internal/server` 79.4%（低于 95% 门禁）。冲刺开始时已红；经 Owner 批准（方案 B，2026-08-14）以基线地板值登记在 `scripts/cover-gate.sh` 的 KNOWN_GAP_FLOORS，回归仍会失败；旅程后随摩擦修复一并补齐并移除地板。同批次已修复：全部 35 处导出符号注释缺失（lint 转绿）、旅程关键路径测试（rss Filtered 摄取、provider Curator/Writer 两个 Provider 实现，rss 96.8%、provider 96.9%）。
3. Narration 在当前环境不可用（宿主与 Docker 镜像均无 Kokoro，实例内 0 条 Narration，历史从未行使）。2026-08-17 核查与纠正：Owner 指出 Narration 是个人收听（AI DJ），不进公众号，语言跟随 Source；实例 320 集全部为英文播客，英文 Gist 可用 Groq 现存 TTS。实测：`playai-tts` 已下线（model_decommissioned，官方文档过时），现存仅 `canopylabs/orpheus-v1-english`（需 Owner 在 Groq 控制台接受模型条款；单请求 200 字符上限，provider 需按句分段 + ffmpeg 拼接）。若实现，走既有 NarrationProvider 接口（ADR-0019 预留的引擎替换缝）。属次级研究区功能，不阻塞黄金旅程。
2. （旅程中发现的新缺口追加于此）

## 步骤核查表

1. [ ] 创建 EditorialProfile：SourceScope、角色模型、预算、提案节奏
2. [ ] 为播客设置 Filtered/AllNew IngestionPolicy；新 Episode 自动形成 Transcript、KnowledgeCard、KeyPoint
3. [ ] 统一素材 Inbox：按 Episode/Podcast/状态筛选 + 语义搜索浏览 KeyPoint；手工补充一个遗漏观点
4. [ ] Scout 基于多个 Episode 产出不重复、带真实来源的 ArticleProposal
5. [ ] 接受提案 → Curator 生成 ArticleBrief，显示入选/淘汰材料与冲突关系
6. [ ] 确认 Brief → Writer、EvidenceReviewer、StyleEditor、Writer 修订流水线产出可交付 ArticleRevision
7. [ ] Markdown 编辑器局部修改 → 新 ArticleRevision + 增量重审
8. [ ] 当前 Revision 通过硬性证据门禁 → 生成带来源列表的 PublicationPackage
9. [ ] 富文本一键复制，粘贴进微信公众号后台编辑器（不要求群发）
10. [ ] 备份 → 全新实例恢复 Source、KeyPoint、画像、提案、Brief、全部 Revision、审校结果

## 摩擦清单

> 每条摩擦记录四要素：所在步骤、期望、实际、本能反应（绕过方式/放弃冲动）。
> 处置只有两种：记录（默认）或硬阻塞最小修复（注明修复内容与继续点）。

| # | 步骤 | 期望 | 实际 | 本能反应 | 处置 | 杠杆初判（解锁步骤数 × 频率） |
|---|------|------|------|----------|------|------------------------------|
| 1 | 旅程前·环境启动 | `./cloudwisepod` 直接可用，或报错时告知如何加载配置 | `配置错误: SESSION_SECRET 必须设置`，无 .env 提示；需手动 `set -a; source .env` 或包装脚本 | 想让二进制自动读 .env | Makefile 新增 `serve` 目标（工具层，未改产品代码）；auto-load .env 与“报错信息加提示”作为候选改进登记，旅程后统一排序 | 低杠杆：每次冷启动一次，已有 make serve 绕过；但影响 first-run 体验 |
| 2 | 第 1 步·任务模型配置 | 已配 API key 时，模型选择应可从 `/models` 拉取可用列表选择 | 全部角色模型为自由文本输入 + placeholder（如“留空继承分析模型”），需手动拼写模型名；拼错只能等运行时报错。“编辑角色故障切换”（备用 Provider + 备用模型）同样自由文本（Owner 2026-08-17 补充指出） | 想直接选而不是背模型名 | 登记不修（新功能冻结）；本次绕过：主配置全部留空继承分析模型；故障切换留空（单 Provider 无可切换对象，不阻塞） | 中杠杆：每个新画像/每次换模型/每次配故障切换都会碰；实现面小（两 Provider 均有 /models 端点，需覆盖主配置+故障切换两类输入点、过滤非 chat 模型、兼容自定义 baseURL）。★2026-08-17 升级：模型下线事件（见 #3）证明下拉+可用性校验能预防整类事故，杠杆上调 |
| 3 | 第 2 步·单集处理 | 默认配置即可完成分析 | 硬阻塞：Groq 已下线 llama-3.3-70b-versatile（写死的默认分析模型），分析 404 model_not_found；实测该 key 现存 chat 模型仅 openai/gpt-oss-120b、gpt-oss-20b、qwen/qwen3.6-27b、allam-2-7b、compound 系列 | 换模型重试；切到 openai 后又撞 EOF | 纯配置解除：设置页分析模型显式填 openai/gpt-oss-120b（TPM 8K，分窗等待，慢但可用）。代码级修复（更换默认常量+placeholder）旅程后随排序处理 | 高杠杆：默认配置对所有新实例即坏；一行常量修复，但需同步测试与 placeholder |
| 4 | 第 1 步·设置保存 | 保存即持久化，出错应报错 | `_ = UpdateSettings(...)` 静默吞 DB 写失败；内存 Selector 已更新并显示“已保存”，重启后配置丢失。★复现实验（08-17）：同一 UPSERT 在容器内直写成功 → 瞬时写库失败（疑 SQLITE_BUSY），非常态必现；Owner 11:10 再次保存即成功落库。另：OpenAI Provider 原走 /responses，多数兼容服务仅支持 chat/completions → EOF（08-17 已做硬阻塞最小修复：chatCompleteWithMeta 适配器，统一走 /chat/completions + schema 文本下发 + 配置模型全覆盖，全门禁绿） | 困惑配置去哪了 | 保存 bug 本体登记待修（错误上报）；/responses 兼容性已修复 | 高杠杆：涉及信任（静默丢配置）；修复面小（错误上报） |
| 5 | 第 2 步·任务列表 | 时间显示应为本地时区（或可配置） | DB 存 UTC，UI 原样渲染：Owner 本地 UTC+8 看到 11:12:55 实为 19:12:55，误以为“刚才的处理怎么是几小时前” | 需心算 +8 换算 | ★稳定化修订已部署（08-17）：布局层将带 data-utc 的任务/要点/草稿时间按浏览器本地时区渲染，title 保留原 UTC；实例级时区设置仍旅程后决定 | 中杠杆：浏览器本地化先解除当前摩擦；实例级固定时区待排序 |
| 6 | 第 3 步·主题/提案生成 | 系统应主动推送选题方向；每批固定 5 条，提案离开池后自动补货 | 原为纯拉模式：主题手填、KeyPoint 手挂、Scout 手动触发；Owner 进一步明确了自动触发、固定 5 条、删除补 1、全部重新生成和加入选题库的需求 | 盯着建主题的表单不知道该填什么；想要被提示 | ★第一版已实现并部署（08-17）：跨集/单集深读头脑风暴均严格请求 5 条；提案池低于 5 条时，接受/搁置/拒绝后后台自动补货，另有手动“补货至 5 条”入口；普通 GET 不付费调用。全部重新生成/逐条替换仍留作下一轮细化 | 高杠杆：自动供给和固定批次已解锁持续生产；剩余替换策略需真实使用后决定 |
| 7 | 第 3 步·Inbox/KeyPoints 页 | 旧概念不应出现在新流程路口；挂素材应免抄 ID | 双重问题：①/keypoints 页“加入集合”是旧知识管理时代的 Collection（与 Theme 两套数据模型，不喂 Scout），CONTEXT.md 明令避免混用却仍展示在编辑流程必经页，Owner 误用并撞静默失败；②真正入口/themes 原需手填 KeyPoint ID，而 ID 在产品页面不可见→硬阻塞 | 以为是主题功能，试了没反应；面对“KeyPoint ID”字段不知道填什么 | ★最小修复已部署（08-17）：/keypoints 显示 ID、隐藏旧“加入集合”入口并引导到 Theme；/themes 改为下拉选择已授权 KeyPoint，手抄 ID 不再是主路径；旧 Collection API 保留但不进入编辑流程 | 高杠杆：术语陷阱与跨页抄 ID 断点已解除；完整旧知识功能清理旅程后再定 |
| 8 | 第 3 步·素材授权/挂材料 | 授权状态应可见：授权列表应显标题而非 UUID；挂素材时应前置提示素材是否在画像范围内 | ①workbench.html:47 原只渲染 UUID，Owner 看不清授权了哪集；②/themes 原仅提交后校验范围。实例：312 未授权时挂入被拒 | 不知道自己授权过什么；报错后才反应过来 | ★最小修复已部署（08-17）：授权列表显示单集标题+辅助 ID；Theme 选择器只列画像已授权素材；授权不足仍需在 /workbench 补授权 | 中杠杆：生产权限边界现在可见，剩余前置策略提示旅程后优化 |
| 9 | 第 3 步·主题页 | 应能看见主题包含哪些 KeyPoint、来自哪几集、数量够不够 | 原 themes.html 只显示名称/描述/状态，成员、来源分布、跨集数量不可见，导致同集误判和 Scout 拒绝后无法自诊断 | 不知道主题里有什么；被拒后只能靠猜 | ★最小修复已部署（08-17）：主题卡显示成员来源/内容/ID、关系、KeyPoint 总数、不同单集数和“可运行 Scout”状态；选择器同步显示可用素材 | 高杠杆：主题组装核心可见性已补齐，完整卡片交互旅程后继续优化 |
| 10 | 设计决策（已定） | 是否允许“单集深度解读”文章类型 | 原规则 ≥2 单集一刀切禁止单集深读；Owner 明确表示需要单集拆解 | 已决定支持显式 DeepRead：Owner 必须在已确认 Theme 中选择一个 Episode；Scout 只发送该 Episode 的 KeyPoint，候选 kind=deep_read，仍要求有效 Citation；默认跨 Episode 模式不放宽，提案池自动补货只走跨集模式 | 已实现并部署（08-17）：Theme 卡片提供“生成 5 条单集深读”，Workbench 显示 deep_read | 中高杠杆：解决单集内容类型缺口，同时不污染跨集综合底线 |
| 11 | 第 3 步·Scout 运行 | 费用记账不应阻塞主流程；未设价格不写即可 | 硬阻塞：recordEditorialUsage → CalculateEditorialCost 对未登记价格的模型（自定义兼容端点，如 cmc/deepseek/deepseek-v4-flash）直接 ErrNotFound → 整个 Scout 运行中止（“计算模型费用: not found”）。预算门禁语义本已正确（设预算必配价格，无预算放行），但费用记账缺同款宽容 | 配置了合法模型却被记账打断 | ★硬阻塞最小修复（08-17）：CalculateEditorialCost 对 ErrNotFound 记 0 成本返回 nil（审计行照写）；预算仍由 CheckEditorialBudget 单独强制；新增测试 TestEditorialCostUnpricedModelIsZeroCost；全门禁绿已部署 | 中杠杆：影响所有自定义模型用户的编辑链路；修复面小 |
| 12 | 第 4 步·Curator 生成 | 所有编辑角色应一致尊重配置模型 | 硬阻塞：BundleForTask 模型注入循环漏了 bundle.Curator（groq/openai 两分支均漏）→ Curator 永远用默认 openaiAnalysisModel（gpt-4.1-mini），自定义端点收到未知模型 400（“unknown provider for model gpt-4.1-mini”）。Owner 观察到“没使用我的配置”并正确排除 fallback；根因是装配遗漏非配置问题。现有测试只断言 Analysis 未查 Curator，故漏网 | 配置了模型却不生效，看似 fallback 作祟 | ★硬阻塞最小修复（08-17）：两分支补 bundle.Curator = custom；回归钉：两处模型覆盖测试补断言 Curator 注入；全门禁绿已部署 | 高杠杆：类缺陷风险（漏角色）在编辑链路任一角色都可能再现；修复面小但需警惕同类遗漏（如未来加新角色） |
| 13 | 第 4 步·Curator 生成 | AI 生成应产出可解析的结构化结果 | 硬阻塞：OpenAI 路径的 Curator 提示词缺“必须只输出 JSON”后缀（Groq 路径有），deepseek-v4-flash 遂输出 YAML 风格文本 → parseJSONLoose 失败 → Curator 任务失败（claim “任务未完成”）→ Owner 手动建 Brief（material_plan_json=[]）→ 写作环节拦截“Brief 必须选择至少一个 KeyPoint”。实证：同一模型同输入加 JSON 指令后即输出合法 JSON（selectedKeyPointIds 正确）。类缺陷覆盖全部 json_object 模式角色（Scout/Writer/审校） | AI 输出格式不达标，只能手动兜底 | ★硬阻塞最小修复（08-17）：chatCompleteWithMeta 适配层对 format.type=json_object 统一追加“输出必须是 JSON 对象”指令；回归钉 TestOpenAI_ChatCompletion_JsonObjectPrompt；真实端点复现验证通过；全门禁绿已部署 | 高杠杆：一修全角色（scout/curator/writer/evidence/style）；修复面极小（适配层 2 行） |
| 14 | 第 4 步·Workbench 布局 | 数据展示应适配结构：AI 产出可审、表单不露裸 JSON | ①原先接受提案后内联塞 7 字段手动 Brief 表单，入选材料/冲突处理是裸 JSON；②原 AI Brief 只显示论点；③三区块堆叠冗长。Owner 定性“根本无法正常使用，需要 UI 重构” | 无法审阅待确认内容，表单要求手写 JSON | ★信息架构重构已部署（08-17）：工作台按“1 选题池 → 2 Brief 审核 → 3 写作与审校”分栏；顶部显示下一步和状态计数；Brief 显示结构/材料来源+内容+ID/冲突；手动 JSON 仅作折叠兜底；画像、SourceScope、价格表进入设置区；无材料 Brief 隐藏确认/写作按钮 | 高杠杆：主流程已从配置堆叠改为阶段推进；真实旅程后继续按摩擦调整详情密度 |
| 15 | 第 5 步·写作 | 应有明确的默认字数保障 | ①Writer 系统提示词无字数要求，正文长度完全依赖 Brief.target_length；手动 Brief 表单该字段可选且易留空 → 模型自由发挥产出过短正文；②Owner 在零成功修订时误以为“初稿太短小”（实为 13:28 写作失败后页面残留，revisions=0 实证） | 产出短小、无预期长度 | 登记不修（功能冻结；非硬阻塞——AI Curator 生成的 Brief 自带 targetLength 可绕）；候选修复=Writer 提示词补默认字数下限（如 1500 字）或 target_length 必填 | 中杠杆：影响每次写作产出质量；修复面极小（提示词默认值或表单必填） |

## 预算行使记录

- 实际花费（按角色/模型）：
- 超限暂停路径是否被触发或检验：

## 结论与下一版本需求排序

（旅程结束后按两层排序方法填写：第一层步骤权重，第二层杠杆；Phase 16/17/18 维持冻结，单点例外需 Owner 显式批准。）
