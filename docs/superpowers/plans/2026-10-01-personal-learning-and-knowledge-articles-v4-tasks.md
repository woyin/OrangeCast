# 第四轮开发原子任务清单

日期：2026-10-01。基线：`3024642`，迁移截止0069。状态：**实施中；技术完成22/73，剩余51张；产品待验单列**。

关联：[设计文档](../specs/2026-10-01-personal-learning-and-knowledge-articles-v4-design.md)、[开发计划](2026-10-01-personal-learning-and-knowledge-articles-v4.md)、[第三轮验收](../../acceptance/2026-10-personal-learning-v3.md)。

## 1. 卡片规则

- 一卡交付一个有界行为；包含该行为必要的schema、失败处理、测试和说明。默认0.5–1.5个专注工程日，超过2日再拆。技术验收可为单个稳定Interface，不要求未完成页面提前启用。
- 所有依赖是最低依赖，必须已形成可工作的提交；推荐A→R→S→C→G→U→W→F→E→O→V。该顺序不要求启动并行代理。
- 新文件是拟议落点，可按已有等价Module调整；`<next>`须替换执行时实际序号，从0070起分配，不能改历史迁移。文件中“按实际落点”必须在实施记录落实到真实路径。
- 每个涉及持久数据的任务同时处理来源删除/归档/权限变化、备份恢复与旧行，不留到V组才补；V01进行组合复核。
- 验证中的新Test名称表示拟新增行为测试；测试不存在或“no tests to run”不算通过。Fake、真实接口、人工、手机分别登记；配置缺失不掩盖未实现分支。
- 状态从待实施→实施中→已提交/技术验证完成；外部待验条件另列。完成一张卡填写commit、当前测试与证据，不能只改复选框。
- 计划不等于开发授权扩大到部署/真实库清理/账号发布；使用独立目录，不改用户.env，不将自建笔记写默认库。

## 2. 总览

| 组 | 阶段 | 卡数 | 任务 |
| --- | --- | --- | --- |
| A 共用契约与基线 | M0 | 7 | A01–A07 |
| R 听后整理 | M1 | 6 | R01–R06 |
| S 检索升级 | M2 | 7 | S01–S07 |
| C 问题对话 | M3 | 9 | C01–C09 |
| G 缺口补听 | M4 | 6 | G01–G06 |
| U 理解快照 | M5 | 5 | U01–U05 |
| W 文章用途 | M6 | 6 | W01–W06 |
| F 反馈评测 | M7 | 6 | F01–F06 |
| E 学习成果包 | M8 | 5 | E01–E05 |
| O 离线设备路径 | M9 | 11 | O01–O11 |
| V 综合交付 | M10 | 5 | V01–V05 |
| 合计 | M0–M10 | 73 | 技术完成22/73 |

精确依赖已进行静态检查：身份唯一、依赖存在、无自依赖、无循环。此检查仅验证任务图，不验证生产实现。

## 3. 执行索引

- [x] [A01 固化第四轮行为与评测基线](#a01)
- [x] [A02 实现共用表单提交Interface](#a02)
- [x] [A03 将知识文章表单迁入共用Interface](#a03)
- [x] [A04 统一问题与回顾的冲突输入行为](#a04)
- [x] [A05 交付首页下一步投影](#a05)
- [x] [A06 扩展运行类别与无模型任务契约](#a06)
- [x] [A07 建立本轮性能与调用观测基线](#a07)
- [x] [R01 保存和恢复听后整理草稿](#r01)
- [x] [R02 原子保存整理与问题关系](#r02)
- [x] [R03 交付根播放器的整理编辑器](#r03)
- [x] [R04 将VoiceDraft采用到整理输入](#r04)
- [x] [R05 增加可选自然结束轻提示](#r05)
- [x] [R06 完成整理的故障与恢复验收](#r06)
- [x] [S01 建立统一Retrieve与FTS Adapter](#s01)
- [x] [S02 实现独立embedding连接预检与计价](#s02)
- [x] [S03 交付版本化索引与增量事件](#s03)
- [x] [S04 实现内容向量持久任务](#s04)
- [x] [S05 实现明确触发的查询向量任务与缓存](#s05)
- [x] [S06 实现RRF融合与解释性降级](#s06)
- [ ] [S07 验证检索收益并交付开关](#s07)
- [x] [C01 保存问题对话会话与轮身份](#c01)
- [x] [C02 冻结问题范围与有界消息序列](#c02)
- [x] [C03 实现对话生成与关联检查Provider契约](#c03)
- [ ] [C04 交付生成阶段断点与用量](#c04)
- [ ] [C05 原子提交检查后的可见回答](#c05)
- [ ] [C06 交付问题对话页面与引用回听](#c06)
- [ ] [C07 明确采用对话结果为可编辑草稿](#c07)
- [ ] [C08 迁入旧单来源StudyChat持久执行](#c08)
- [ ] [C09 验收跨集与旧对话的恢复竞争](#c09)
- [ ] [G01 建立可处置的材料缺口投影](#g01)
- [ ] [G02 检索缺口候选并确认范围](#g02)
- [ ] [G03 扩展区间队列身份与迁移](#g03)
- [ ] [G04 播放补听区间并隔离进度](#g04)
- [ ] [G05 交付缺口补听页面与下一步](#g05)
- [ ] [G06 验收补听的失效与恢复](#g06)
- [ ] [U01 建立无伪来源的理解快照模型](#u01)
- [ ] [U02 实现保存与选择当前的CAS命令](#u02)
- [ ] [U03 交付理解编辑器与历史差异](#u03)
- [ ] [U04 接入理解的检索与文章选材](#u04)
- [ ] [U05 验收理解演变与清理恢复](#u05)
- [ ] [W01 定义用途目录与新提示版本](#w01)
- [ ] [W02 冻结用途到所有文章阶段](#w02)
- [ ] [W03 实现用途结构及审校判据](#w03)
- [ ] [W04 持久化可选写作计划与确认](#w04)
- [ ] [W05 交付用途与计划交互](#w05)
- [ ] [W06 验收四用途及旧通过版兼容](#w06)
- [ ] [F01 将段落反馈冻结为版本案例](#f01)
- [ ] [F02 给出材料不足与反馈的事实分类](#f02)
- [ ] [F03 交付段落反馈与案例管理](#f03)
- [ ] [F04 把接纳案例接入冻结评测清单](#f04)
- [ ] [F05 生成质量与估价回归比较报告](#f05)
- [ ] [F06 验收私有质量案例生命周期](#f06)
- [ ] [E01 建立有界一致性导出预览](#e01)
- [ ] [E02 实现确定性Markdown与ZIP清单](#e02)
- [ ] [E03 通过无模型任务生成和鉴权下载](#e03)
- [ ] [E04 交付范围选择和导出状态页面](#e04)
- [ ] [E05 验收成果包范围与恢复兼容](#e05)
- [ ] [O01 定义离线设备授权和撤回清单](#o01)
- [ ] [O02 实现能力探测与离线专用外壳](#o02)
- [ ] [O03 提供已鉴权的原音和内容下载清单](#o03)
- [ ] [O04 持久下载与配额错误恢复](#o04)
- [ ] [O05 支持离线原音Range与真实锚点](#o05)
- [ ] [O06 保存可同步的本地操作队列](#o06)
- [ ] [O07 服务端原子同步与去重](#o07)
- [ ] [O08 离线重开、到期与登出清理](#o08)
- [ ] [O09 安全更新SW与清理下载副本](#o09)
- [ ] [O10 完成真实离线综合故障验收](#o10)
- [ ] [O11 登记实体手机使用与待验状态](#o11)
- [ ] [V01 综合验证0069升级及v2恢复](#v01)
- [ ] [V02 验收完整听学成文旅程](#v02)
- [ ] [V03 执行最终技术门禁](#v03)
- [ ] [V04 完成使用、配置、部署与验收文档](#v04)
- [ ] [V05 核对任务闭合并交付提交](#v05)

## 4. A：共用契约与基线（M0）

<a id="a01"></a>
### A01 — 固化第四轮行为与评测基线

**状态**：已提交/技术验证完成。**依赖**：无。**粒度**：0.5–1.5日。

**文件/所有权**：docs/superpowers/plans/*v4-validation.md；internal/evalset/；internal/server/learning_v3_eval_test.go；CONTEXT.md。

**交付**：建立实际实施记录和3集/20笔记/2问题固定自建语料，补充跨集条件冲突、空材料、无来源个人答案、不同措辞查询；记录源码/语料/配置指纹。

**验收（含失败路径）**：旧固定评测可运行；40条检索查询有真实相关身份，至少10条改写；human和实体手机状态为空/待验证；不写默认库，不发送真实资料。

**验证**：go test ./internal/evalset ./internal/server -run "PersonalLearning|LearningFixtures" -count=1；检查私有报告字段，登记继承证据。

**建议提交**：`docs(learning): a01 固化第四轮行为与评测基线`。实际标题应描述最终行为。

**完成记录**：commit `1c7c7ca`；固定自建语料完整性与既有PersonalLearning评测通过（evalset/server当前运行）；真实接口/人工/设备待验条件见实施记录。

<a id="a02"></a>
### A02 — 实现共用表单提交Interface

**状态**：已提交/技术验证完成。**依赖**：A01。**粒度**：0.5–1.5日。

**文件/所有权**：internal/server/static/form-actions.js（新）；static/view-lifecycle.js；testdata/form-actions-common.cjs（新）；form_actions_test.go。

**交付**：在既有Scope上实现提交、原disabled恢复、错误code、requestKey、401清理、409保留及局部导航；不接管根播放器。

**验收（含失败路径）**：先FormData后禁用；getAttribute读取action；在途不能重复发；网络未知不自动重试；卸载取消等待但不声称后台停止；命名action字段、原disabled和迟到响应通过。

**验证**：实际加载脚本的Node行为检查及go test ./internal/server -run TestCommonFormActions -count=1。

**建议提交**：`feat(learning): a02 实现共用表单提交Interface`。实际标题应描述最终行为。

**完成记录**：commit `bcb68d3`；TestCommonFormActions通过：禁用状态恢复、单请求、401/409、未知结果及卸载隔离；真实接口/人工/设备待验条件见实施记录。

<a id="a03"></a>
### A03 — 将知识文章表单迁入共用Interface

**状态**：已提交/技术验证完成。**依赖**：A02。**粒度**：0.5–1.5日。

**文件/所有权**：static/knowledge-articles.js；static/knowledge-updates.js；templates/knowledge_articles.html；templates/knowledge_article.html；相关Node夹具。

**交付**：迁移一个业务组：文章发现/修订/提案表单共用错误与导航契约，移除被替代重复实现。

**验收（含失败路径）**：生成范围、材料选择及requestKey保留；配置缺失/预算/父稿冲突仍明确；SSR303不变；成功不替换audio；不为迁移新增付费请求。

**验证**：go test ./internal/server -run "KnowledgeForm|KnowledgeGeneration|KnowledgeUpdate" -count=1；Node现有+新回归。

**建议提交**：`feat(learning): a03 将知识文章表单迁入共用Interface`。实际标题应描述最终行为。

**完成记录**：commit `d9f1efd`；KnowledgeForm、KnowledgeGeneration、KnowledgeUpdate当前测试通过；实际Node脚本保留父稿草稿、命名action与局部导航；真实接口/人工/设备待验条件见实施记录。

<a id="a04"></a>
### A04 — 统一问题与回顾的冲突输入行为

**状态**：已提交/技术验证完成。**依赖**：A02。**粒度**：0.5–1.5日。

**文件/所有权**：static/learning-questions.js；static/daily-review.js；static/review.js；testdata/question-forms.cjs；testdata/automation-forms.cjs。

**交付**：迁移问题/回顾表单到相同提交契约，保留各自CAS和草稿身份；录音根外壳仍用自己的持久生命周期。

**验收（含失败路径）**：旧问题修订/旧回答冲突不丢文字；查看依据仍不算回答；轮询不替换表单；没有重复事件监听；旧SSR与命名action回归通过。

**验证**：go test ./internal/server -run "QuestionForm|DailyReview|LearningReview|AutomationForms" -count=1；实际脚本行为。

**建议提交**：`feat(learning): a04 统一问题与回顾的冲突输入行为`。实际标题应描述最终行为。

**完成记录**：commit `3a58e4a`；当前QuestionForm、DailyReview、LearningReview、AutomationForms及共用接口测试通过；实际Node验证查看依据不清解释和CAS草稿身份；真实接口/人工/设备待验条件见实施记录。

<a id="a05"></a>
### A05 — 交付首页下一步投影

**状态**：已提交/技术验证完成。**依赖**：A01。**粒度**：0.5–1.5日。

**文件/所有权**：internal/store/learning_home.go（新）；internal/server/auth_handlers.go的handleDashboard；templates/dashboard.html；templates/layout.html；static/app.css。

**交付**：现有首页增加当前问题选择和最多3项可解释主动作，手机导航调整但保留全部URL。

**验收（含失败路径）**：空库/无笔记/活动回顾/失败文章有明确动作；读页面零AI、零会话创建；当前问题被删/暂停有有效降级；360/375px及键盘焦点可用。

**验证**：go test ./internal/store ./internal/server -run "LearningHome|DashboardNextActions" -count=1；独立浏览器查看导航与主动作。

**建议提交**：`feat(learning): a05 交付首页下一步投影`。实际标题应描述最终行为。

**完成记录**：commit `50b7019`；首页/偏好CAS与去重、问题暂停/删除降级、0069升级/备份恢复当前race通过；实际360/375px无溢出、选择保存与10次持续播放导航通过；真实接口/人工/设备待验条件见实施记录。

<a id="a06"></a>
### A06 — 扩展运行类别与无模型任务契约

**状态**：已提交/技术验证完成。**依赖**：A01。**粒度**：0.5–1.5日。

**文件/所有权**：migrations/<next>_learning_run_lanes.sql；store/run_controls.go；store/jobs.go；queue/worker.go；server/automation.go及模板。

**交付**：增加study/index/export类别和稳定方向映射；同步SQL入队触发器、领取、停止、Worker、面板；新增无模型导出分支入口。

**验收（含失败路径）**：旧lane/任务/修订不变；暂停与领取竞争原子；重启保留控制；导出不解析Provider/预占AI预算；缺key仍可运行无模型任务；未知费用不变成零。

**验证**：go test ./internal/store ./internal/queue ./internal/server -run "RunControl|RunLane|NonModelJob" -race -count=1；真实0069升级。

**建议提交**：`feat(learning): a06 扩展运行类别与无模型任务契约`。实际标题应描述最终行为。

**完成记录**：commit `126717b`；当前RunControl/RunLane race及0068升级通过；NonModelJob核对预算/用量SQL成功且记录0、不解析Provider、已知结果不重复组装；真实接口/人工/设备待验条件见实施记录。

<a id="a07"></a>
### A07 — 建立本轮性能与调用观测基线

**状态**：已提交/技术验证完成。**依赖**：A01。**粒度**：0.5–1.5日。

**文件/所有权**：store/*benchmark_test.go；server/browser_acceptance_test.go；docs/superpowers/plans/*v4-validation.md；忽略目录data/eval/。

**交付**：记录FTS、首页、队列、固定评测的同机基线；报告区分DB延迟、外部接口耗时、总耗时和峰值内存。

**验收（含失败路径）**：1万/5万合成索引按同查询/硬件保存参数；不称自建召回率为实库质量；不重复供应商调用；只登记观察结果。

**验证**：go test ./internal/store -run "^$" -bench KnowledgeWideRecall -benchtime=20x -count=1；固定Fake评测与计时。

**建议提交**：`test(learning): a07 建立本轮性能与调用观测基线`。实际标题应描述最终行为。

**完成记录**：commit `d050836`；同机1万/5万固定索引20次FTS与采样内存基线通过；首页/队列真实浏览器资源观察、固定Fake评测与语料指纹登记；真实接口/人工/设备待验条件见实施记录。


## 5. R：听后整理（M1）

<a id="r01"></a>
### R01 — 保存和恢复听后整理草稿

**状态**：已提交/技术验证完成。**依赖**：A01。**粒度**：0.5–1.5日。

**文件/所有权**：migrations/<next>_listening_reflections.sql；store/listening_reflections.go（新）；store/*reflections_test.go。

**交付**：新增整理捕获、三项回答、修订/状态/到期/已保存笔记身份；开始/读/编辑/取消有界操作。

**验收（含失败路径）**：捕获实际来源/快照/模式/音频；草稿不入选材；重放开始复用；过期/来源删除清理；v2备份恢复文字与状态，迁移零模型。

**验证**：go test ./internal/store -run TestListeningReflectionDraft -count=1；真实0069升级/新表恢复。

**建议提交**：`feat(learning): r01 保存和恢复听后整理草稿`。实际标题应描述最终行为。

**完成记录**：commit `f388fee`；当前验证：TestListeningReflectionDraft race 通过（11.748s）；真实0069前缀升级、独立备份恢复、并发开始、重放、CAS、上限、取消、到期、purge清理；无模型调用。；真实接口/人工/设备待验条件见实施记录。

<a id="r02"></a>
### R02 — 原子保存整理与问题关系

**状态**：已提交/技术验证完成。**依赖**：R01。**粒度**：0.5–1.5日。

**文件/所有权**：store/listening_reflections.go；store/owner_notes.go；store/learning_questions.go；server/listening_reflections.go（新）；routes.go。

**交付**：将非空答案确定性组成一个OwnerReflection，同事务写笔记、问题关系和saved状态，HTTP共享命令。

**验收（含失败路径）**：至少一栏非空；同UUID同hash唯一笔记；异hash/旧修订409；问题变化/来源失效/事务故障不留下半条笔记；tx内不调用DB连接方法；返回原保存结果可核对。

**验证**：go test ./internal/store ./internal/server -run TestListeningReflectionSave -race -count=1；故障注入和CSRF/输入上限。

**建议提交**：`feat(learning): r02 原子保存整理与问题关系`。实际标题应描述最终行为。

**完成记录**：commit `53b2dfe`；当前验证：TestListeningReflection store/server race 通过（12.077s/6.274s）；提交故障注入证明笔记、关系、修订、回执全部回滚；已保存重放唯一、异hash/旧修订409、删除笔记不重建、问题/来源失效、HTTP鉴权/CSRF/64KiB/只读GET。；真实接口/人工/设备待验条件见实施记录。

<a id="r03"></a>
### R03 — 交付根播放器的整理编辑器

**状态**：已提交/技术验证完成。**依赖**：R02, A02。**粒度**：0.5–1.5日。

**文件/所有权**：templates/layout.html；templates/listening_reflection.html（新）；static/listening-reflections.js（新）；static/listening-session.js。

**交付**：从当前播放启动三栏整理，草稿本地恢复与服务器同步，问题选择冻结修订；局部导航继续播放。

**验收（含失败路径）**：播放A浏览B仍保存A；刷新恢复原capture；失败保留文字；普通随听笔记仍独立；无位置时不伪造0秒；360/375px和标签/焦点可用。

**验证**：实际Node控制器测试；浏览器A/B来源、重载、保存与连续切页。

**建议提交**：`feat(learning): r03 交付根播放器的整理编辑器`。实际标题应描述最终行为。

**完成记录**：commit `b4b2d3c`；当前验证：实际Node控制器与HTTP测试通过（0.745s）；独立真实浏览器验证A播放/B浏览、刷新捕获与文字恢复、正式保存归属A、唯一笔记和本地清理、360/375px无横向溢出；截图已查看，harness 129.954s；物理手机待验。；真实接口/人工/设备待验条件见实施记录。

<a id="r04"></a>
### R04 — 将VoiceDraft采用到整理输入

**状态**：已提交/技术验证完成。**依赖**：R03。**粒度**：0.5–1.5日。

**文件/所有权**：static/voice-notes.js；static/listening-reflections.js；store/voice_notes.go；server/voice_notes.go；相关voice测试。

**交付**：复用录音/转写，将明确采用的建议放入指定整理栏，保留VoiceDraft来源与关联整理身份，避免第二条自动保存路径。

**验收（含失败路径）**：ASR迟到不覆盖Owner三栏；采用才改文字；保存整理只生成一个笔记；留草稿/刷新/切页不转写；权限拒绝/取消和本地断网恢复不丢锚点。

**验证**：go test ./internal/store ./internal/server -run "VoiceReflection|VoiceHTTP" -count=1；Node与浏览器权限/迟到场景。

**建议提交**：`feat(learning): r04 将VoiceDraft采用到整理输入`。实际标题应描述最终行为。

**完成记录**：commit `d6ae413`；当前验证：VoiceReflection/VoiceHTTP/VoiceRecovered/ListeningReflection store/server race 通过；Node验证录音使用整理A的捕获、明确采用所选栏、其他Owner栏保持、迟到建议不改栏、录音不支持时保留原捕获；故障采用回滚、重放、独立Voice保存拦截、统一笔记与删除音频；浏览器故障综合证据归R06。；真实接口/人工/设备待验条件见实施记录。

<a id="r05"></a>
### R05 — 增加可选自然结束轻提示

**状态**：已提交/技术验证完成。**依赖**：R03, A05。**粒度**：0.5–1.5日。

**文件/所有权**：store设置增量迁移；static/playback-controller.js；static/listening-reflections.js；dashboard主动作。

**交付**：提示偏好默认关闭，自然整集结束可轻提示整理；提示点击才开始会话。

**验收（含失败路径）**：循环/DJ换项/睡眠/片段结束/错误不提示；不抢焦点或中断连播；同次结束最多一次；关闭提示不创建记录或模型任务。

**验证**：播放器事件Node测试；浏览器整集自然结束与循环/睡眠对照。

**建议提交**：`feat(learning): r05 增加可选自然结束轻提示`。实际标题应描述最终行为。

**完成记录**：commit `26e7513`；当前验证：ListeningReflection/DashboardNextActions/LearningHome race 通过（server 6.411s/store 17.080s）；SharedPlaybackController/RootEditor/Logout 通过；真实audio ended关闭/启用对照、无记录/无焦点变化、真实循环与控制睡眠期限对照，截图已查看；复用0070已安装默认关闭偏好，无额外模型迁移。；真实接口/人工/设备待验条件见实施记录。

<a id="r06"></a>
### R06 — 完成整理的故障与恢复验收

**状态**：已提交/技术验证完成。**依赖**：R04, R05。**粒度**：0.5–1.5日。

**文件/所有权**：store/reflections_test.go；server/reflections_test.go；browser_acceptance_test.go；本轮validation。

**交付**：固定整理端到端与故障夹具，验证源删除、双窗口、断网、保存后响应丢失、备份及logout。

**验收（含失败路径）**：笔记/关系/索引结果唯一；明确错误及可恢复草稿；私人状态退出清理；自建语料与真实Owner分开；截图实际查看。

**验证**：相关store/server/queue race；独立浏览器真实offline开关；更新当前证据。

**建议提交**：`test(learning): r06 完成整理的故障与恢复验收`。实际标题应描述最终行为。

**完成记录**：commit `bf521e9`；当前相关store/server/queue race通过；真实v2备份/恢复/重放/purge、Worker到期清理、实际浏览器offline开关与双窗口CAS/本地副本恢复，跨窗口退出旧播放缺口修复后重测/login且spec=null/正文副本空/本地清理；截图已查看。物理手机与真实Owner质量独立待验。；真实接口/人工/设备待验条件见实施记录。


## 6. S：检索升级（M2）

<a id="s01"></a>
### S01 — 建立统一Retrieve与FTS Adapter

**状态**：已提交/技术验证完成。**依赖**：A01。**粒度**：0.5–1.5日。

**文件/所有权**：store/knowledge_search.go；store/knowledge_recall.go；store/semantic_search.go；provider检索投影类型。

**交付**：以小Interface统一候选身份、用途/范围/权限、容量及降级说明，保持当前FTS实现作为默认Adapter。

**验收（含失败路径）**：原关键词/分页/来源策略/问题过滤不退化；普通本地搜索与外发选材目的不同；模型未配置时只有FTS且明确；调用者不直接扫描旧字符向量。

**验证**：go test ./internal/store -run "KnowledgeSearch|KnowledgeRecall" -count=1；原查询结果对照。

**建议提交**：`feat(learning): s01 建立统一Retrieve与FTS Adapter`。实际标题应描述最终行为。

**完成记录**：commit `a5028dd`；当前KnowledgeSearch/KnowledgeRecall/UnifiedKnowledgeSearch store测试通过（1.967s）；检索目的显式区分local/external、旧查询分页/范围/版本DeepEqual对照、本地private可见与外发不可见、模糊目的拒绝；普通读取total_changes不变，不扫描旧字符向量。语义请求尚未就绪明确返回FTS降级；S06将接入向量适配。；真实接口/人工/设备待验条件见实施记录。

<a id="s02"></a>
### S02 — 实现独立embedding连接预检与计价

**状态**：已提交/技术验证完成。**依赖**：A01。**粒度**：0.5–1.5日。

**文件/所有权**：config/config.go及测试；provider/embedding.go（新）；provider/selector.go；model价格配置；.env.example。

**交付**：三项配置成组、维度检查、脱敏连接身份；明确预检才调用，登记真实计费单位/估价，不改用户.env。

**验收（含失败路径）**：缺字段/无穷或错误向量/维度变化/HTTP错误不接受；明确配置不复用POD端点；有预算无价格阻断；密钥不进入页面/任务/报告。

**验证**：go test ./internal/config ./internal/provider -run "Embedding|Config" -count=1；协议stub和坏响应。

**建议提交**：`feat(learning): s02 实现独立embedding连接预检与计价`。实际标题应描述最终行为。

**完成记录**：commit `724b319`；配置/协议stub测试通过；输入价格冻结及预算阻断race通过；真实接口与预检UI按S07待验；真实接口/人工/设备待验条件见实施记录。

<a id="s03"></a>
### S03 — 交付版本化索引与增量事件

**状态**：已提交/技术验证完成。**依赖**：S01。**粒度**：0.5–1.5日。

**文件/所有权**：migrations/<next>_knowledge_embeddings.sql；store/knowledge_embeddings.go（新）；现有索引/来源策略/purge落点。

**交付**：对象版本/正文hash/配置/维度唯一，增量队列和重建状态；来源/权限/当前版本改变立即使旧向量不合格。

**验收（含失败路径）**：只有明确合格材料；私有草稿/建议/未通过对话不入索引；purge同步清理；模型切换不混维度；备份恢复缺缓存时FTS可用且不自动外发重建。

**验证**：go test ./internal/store -run TestKnowledgeEmbeddingIndex -race -count=1；真实迁移与权限更新。

**建议提交**：`feat(learning): s03 交付版本化索引与增量事件`。实际标题应描述最终行为。

**完成记录**：commit `04a683f`；版本/权限/purge/分窗/维度race测试通过；真实0069升级与缓存缺失备份恢复FTS通过；合成向量仅验证事务；真实接口/人工/设备待验条件见实施记录。

<a id="s04"></a>
### S04 — 实现内容向量持久任务

**状态**：已提交/技术验证完成。**依赖**：S02, S03, A06。**粒度**：0.5–1.5日。

**文件/所有权**：queue/knowledge_embeddings.go（新）；store索引准入/预算；provider/embedding.go；automation投影。

**交付**：每批最多16项/单项8KiB的完整窗口，冻结输入/价格，响应断点与receipt保存，控制后再原子采用仍匹配的版本。

**验收（含失败路径）**：增量/重建仅Owner开启；外发复查每个Source；已知响应重试零新调用；旧版本/停止时不应用；unknown明确；实际单位与价格未知分开。

**验证**：go test ./internal/queue ./internal/store -run TestKnowledgeEmbeddingJob -race -count=1；调用计数/记账故障。

**建议提交**：`feat(learning): s04 实现内容向量持久任务`。实际标题应描述最终行为。

**完成记录**：commit `fdf03a6`；真实HTTP stub调用计数及预算/响应/receipt/停止/撤回/purge竞态race通过；已知响应恢复零新调用；预检不开索引；真实接口/人工/设备待验条件见实施记录。

<a id="s05"></a>
### S05 — 实现明确触发的查询向量任务与缓存

**状态**：已提交/技术验证完成。**依赖**：S04。**粒度**：0.5–1.5日。

**文件/所有权**：store/knowledge_embeddings.go；queue索引分支；server/knowledge_search.go；static/search.js（新或实际落点）。

**交付**：规范化query+配置身份缓存，未命中返回FTS及语义准备入口；明确触发后只读轮询，删除epoch保护缓存。

**验收（含失败路径）**：普通GET零embedding调用；重复query任务幂等；没有授权向量不外发query；配置更换/失效缓存不误用；价格/预算/未知响应有明确恢复。

**验证**：go test ./internal/store ./internal/queue ./internal/server -run TestKnowledgeQueryEmbedding -count=1；HTTP GET计数。

**建议提交**：`feat(learning): s05 实现明确触发的查询向量任务与缓存`。实际标题应描述最终行为。

**完成记录**：commit `716d3fa`；并发8次只入队1项；cache/epoch/过期/撤回race通过；HTTP GET零调用且只读；实际控制器未知ACK/卸载Node通过；真实接口/人工/设备待验条件见实施记录。

<a id="s06"></a>
### S06 — 实现RRF融合与解释性降级

**状态**：已提交/技术验证完成。**依赖**：S01, S05。**粒度**：0.5–1.5日。

**文件/所有权**：store/knowledge_retrieval.go（新或合并现有文件）；store检索测试；server/search模板。

**交付**：FTS与真实向量Adapter按RRF合并，保留lexical/semantic/both及覆盖范围；事务外计算有界矩阵，权限在输出前再复核。

**验收（含失败路径）**：重复身份去重；相似度不当置信度；无索引/超容量/维度不符/撤回降级FTS；冷/热缓存一致；不全库载入正文。

**验证**：go test ./internal/store -run TestKnowledgeHybridRetrieve -race -count=1；固定40查询排名与降级。

**建议提交**：`feat(learning): s06 实现RRF融合与解释性降级`。实际标题应描述最终行为。

**完成记录**：commit `b533bdf`；当前定向race通过：store26.730s/server6.334s；RRF去重、筛选、权限撤回、维度破损、50001窗口容量降级、冷热点一致及40条冻结查询；合成向量仅验证算法和生命周期，不作为真实模型质量收益证据；真实接口/人工/设备待验条件见实施记录。

<a id="s07"></a>
### S07 — 验证检索收益并交付开关

**状态**：待实施。**依赖**：S06, A07。**粒度**：0.5–1.5日。

**文件/所有权**：store/*benchmark_test.go；evalset检索语料；server/settings与search；docs/evalset.md；validation。

**交付**：显示索引范围/费用/容量/重建状态，默认关闭；固定质量和目标机性能报告，真实连接有显式入口。

**验收（含失败路径）**：Recall@10不低于FTS，改写子集目标+10个百分点；1万/5万p95目标150/500ms；未达标保持关闭并记录原因；真实接口未配置不伪造完成质量。

**验证**：固定40查询、同机冷热benchmark/内存；HTTP配置/权限；可选真实模型一次有界评测。

**建议提交**：`test(learning): s07 验证检索收益并交付开关`。实际标题应描述最终行为。

**完成记录**：commit待填；当前验证/证据待填；真实接口/人工/设备待验条件单列。


## 7. C：问题对话（M3）

<a id="c01"></a>
### C01 — 保存问题对话会话与轮身份

**状态**：已提交/技术验证完成。**依赖**：A01, A06。**粒度**：0.5–1.5日。

**文件/所有权**：migrations/<next>_question_study.sql；store/question_study.go（新）；models新增任务常量；purge及问题删除。

**交付**：会话修订、轮序号、UUID/hash、domain state、冻结输入及job关系；读历史不发模型请求。

**验收（含失败路径）**：同轮提交幂等/双窗口CAS；普通Source删除清除相关冻结正文；问题删除清理会话但费用仍在；旧StudySession可读不迁造成新事实。

**验证**：go test ./internal/store -run TestQuestionStudySession -race -count=1；真实0069迁移/恢复。

**建议提交**：`feat(learning): c01 保存问题对话会话与轮身份`。实际标题应描述最终行为。

**完成记录**：commit `41e2592`；当前TestQuestionStudySession race通过10.667s：8并发同UUID一轮、修改负载409、双窗口CAS/单未完成轮/12轮容量、未检查回答不入历史、来源及问题删除清理冻结正文而保留31单位账本；真实0069升级保留旧StudySession且不造新事实、一致性备份恢复零任务；真实接口/人工/设备待验条件见实施记录。

<a id="c02"></a>
### C02 — 冻结问题范围与有界消息序列

**状态**：已提交/技术验证完成。**依赖**：C01, S01。**粒度**：0.5–1.5日。

**文件/所有权**：store/question_study.go；provider/question_study.go（新）；现有FrozenLearningQuestion/证据窗口接口。

**交付**：只取confirmed关系，校验当前来源授权；冻结问题/材料/历史，8来源20项40KiB材料和最近6轮16KiB历史；精确序列估价。

**验收（含失败路径）**：零材料不外发；建议不能授权；排除local_only与失效依据；容量遗漏明确；修改问题/材料后新输入不同；资料中指令不成为系统指令。

**验证**：go test ./internal/store ./internal/provider -run TestQuestionStudyScope -count=1；坏引用/长材料/注入夹具。

**建议提交**：`feat(learning): c02 冻结问题范围与有界消息序列`。实际标题应描述最终行为。

**完成记录**：commit `3216573`；当前TestQuestionStudyScope race通过；confirmed-only、local_only/清理依据/坏片段排除、历史权限撤回、8来源20项40KiB/最近6轮16KiB/完整长表达容量拒绝、输入指纹随笔记变化；最终消息逐字序列估价且资料指令只在user数据，不外发组织关系元数据；尚无付费调用入口；真实接口/人工/设备待验条件见实施记录。

<a id="c03"></a>
### C03 — 实现对话生成与关联检查Provider契约

**状态**：已提交/技术验证完成。**依赖**：C02。**粒度**：0.5–1.5日。

**文件/所有权**：provider/question_study.go；provider/question_study_review.go（新）；config/config.go；.env.example；provider测试。

**交付**：新版本结构化结果，来源/Owner理解/AI解释分开；独立检查请求与预算上限，配置回退链明确。

**验收（含失败路径）**：模型只能引用真实key/版本/片段；共识/分歧保留限定条件；无答案是正常结果；坏JSON/超限/不存在引用不显示；同模型不称独立模型。

**验证**：go test ./internal/provider ./internal/config -run TestQuestionStudyProvider -count=1；结构/单位/配置协议测试。

**建议提交**：`feat(learning): c03 实现对话生成与关联检查Provider契约`。实际标题应描述最终行为。

**完成记录**：commit `8981024`；当前TestQuestionStudyProvider race通过：provider1.396s/config1.460s；实际HTTP协议stub验证生成4096/检查2048输出上限和估价同消息、分开调用/同模型不宣称独立、模型回退链；坏JSON/凭空key版本片段/Owner冒充来源/无条件假共识/不完整检查拒绝；未知结果单次调用，坏响应仍保留数字用量；队列与可见采用按C04/C05继续；真实接口/人工/设备待验条件见实施记录。

<a id="c04"></a>
### C04 — 交付生成阶段断点与用量

**状态**：待实施。**依赖**：C03, A06。**粒度**：0.5–1.5日。

**文件/所有权**：queue/question_study.go（新）；queue/knowledge_article.go中共用执行事实；store准入/预算。

**交付**：明确提交接纳生成任务，复用外发/断点/receiptInterface；响应已知与未知分别处理，下一阶段有独立身份。

**验收（含失败路径）**：关闭页面后台继续；response先保存；记账失败恢复不重发；停止/暂停/材料变化不采用迟到结果；unknown不自动重试；估价按实际消息。

**验证**：go test ./internal/queue ./internal/store -run TestQuestionStudyGeneration -race -count=1；故障注入与调用计数。

**建议提交**：`feat(learning): c04 交付生成阶段断点与用量`。实际标题应描述最终行为。

**完成记录**：commit待填；当前验证/证据待填；真实接口/人工/设备待验条件单列。

<a id="c05"></a>
### C05 — 原子提交检查后的可见回答

**状态**：待实施。**依赖**：C04。**粒度**：0.5–1.5日。

**文件/所有权**：queue/question_study_review.go（新）；store/question_study.go；automation投影；provider验证。

**交付**：检查阶段独立断点/receipt，结果通过后同事务写可见回答与轮状态；保留不足/被抑制结果私有事实。

**验收（含失败路径）**：未检查/不相关不进历史/检索；复查scope与RunControl；生成后崩溃只恢复检查；检查已知恢复不付费重发；预算不足停在明确阶段。

**验证**：go test ./internal/queue ./internal/store -run TestQuestionStudyCommit -race -count=1；两阶段付费/停止竞争。

**建议提交**：`feat(learning): c05 原子提交检查后的可见回答`。实际标题应描述最终行为。

**完成记录**：commit待填；当前验证/证据待填；真实接口/人工/设备待验条件单列。

<a id="c06"></a>
### C06 — 交付问题对话页面与引用回听

**状态**：待实施。**依赖**：C05, A02。**粒度**：0.5–1.5日。

**文件/所有权**：server/question_study.go（新）；routes.go；templates/question_study.html（新）；static/question-study.js（新）；learning_question模板。

**交付**：提问方式、范围确认、任务轮询、逐项引用、错误恢复和停止；仅已接受结果进入可见会话。

**验收（含失败路径）**：切页不断播放；轮询不清空未提交下一问；401清理、409保留；引用定位真实；无JS查看历史/提交可用；任务面板可解释模型/费用。

**验证**：HTTP auth/CSRF/上限测试；实际Node脚本；浏览器跨集对话和连续导航。

**建议提交**：`feat(learning): c06 交付问题对话页面与引用回听`。实际标题应描述最终行为。

**完成记录**：commit待填；当前验证/证据待填；真实接口/人工/设备待验条件单列。

<a id="c07"></a>
### C07 — 明确采用对话结果为可编辑草稿

**状态**：待实施。**依赖**：C06。**粒度**：0.5–1.5日。

**文件/所有权**：server/question_study.go；static/question-study.js；现有笔记编辑器的草稿导入接口。

**交付**：提供复制/采用所选解释及其来源清单；单源笔记要求Owner明确选择合法Source，跨来源正文供后续理解编辑器复用。

**验收（含失败路径）**：点击采用不自动保存/确认事实；保留AI来历；不构造跨Source Citation或假锚点；Owner编辑后才保存；同一结果重复打开不重复笔记。

**验证**：go test ./internal/server -run TestQuestionStudyAdoptDraft -count=1；Node草稿身份及不保存检查。

**建议提交**：`feat(learning): c07 明确采用对话结果为可编辑草稿`。实际标题应描述最终行为。

**完成记录**：commit待填；当前验证/证据待填；真实接口/人工/设备待验条件单列。

<a id="c08"></a>
### C08 — 迁入旧单来源StudyChat持久执行

**状态**：待实施。**依赖**：C05, A04。**粒度**：0.5–1.5日。

**文件/所有权**：server/ai_handlers.go；static/player.js中的StudyChat控制器；store/study_sessions.go；queue单源Adapter。

**交付**：保留旧POST地址/历史读取，新增任务响应由前端轮询；采用相同外发/检查/receipt契约但冻结单来源范围。

**验收（含失败路径）**：旧历史不重发或补造断点；来源范围/ReferenceCheck纪律不变；新回合HTTP超时可恢复；禁止旧客户端重复提交产生第二次调用，兼容失败有明确反馈。

**验证**：旧StudyChat HTTP/浏览器回归；新幂等key/两阶段故障测试；source删除/预算检查。

**建议提交**：`feat(learning): c08 迁入旧单来源StudyChat持久执行`。实际标题应描述最终行为。

**完成记录**：commit待填；当前验证/证据待填；真实接口/人工/设备待验条件单列。

<a id="c09"></a>
### C09 — 验收跨集与旧对话的恢复竞争

**状态**：待实施。**依赖**：C07, C08, S07, R06。**粒度**：0.5–1.5日。

**文件/所有权**：queue/question_study_test.go；server/question_study_test.go；browser_acceptance_test.go；validation。

**交付**：固定3来源条件/矛盾/无答案和Scope变化，多窗口、停止、断点、费用、旧会话对照。

**验收（含失败路径）**：已知响应0新调用；未知必须明确新attempt；含本地来源/建议不外发；转写/播放/草稿持续；真实接口及人工质量结果分别记。

**验证**：相关queue/store/server race；独立浏览器+可选有界POD真实样本；所有证据指纹记录。

**建议提交**：`test(learning): c09 验收跨集与旧对话的恢复竞争`。实际标题应描述最终行为。

**完成记录**：commit待填；当前验证/证据待填；真实接口/人工/设备待验条件单列。


## 8. G：缺口补听（M4）

<a id="g01"></a>
### G01 — 建立可处置的材料缺口投影

**状态**：待实施。**依赖**：C05。**粒度**：0.5–1.5日。

**文件/所有权**：migrations/<next>_evidence_gaps.sql；store/evidence_gaps.go（新）；候选/更新Missing投影。

**交付**：复用已有Missing并保存父版本、类型、来历/理由和Owner处置；父内容改变使旧判断过期。

**验收（含失败路径）**：程序/模型/Owner分类明确；不会声称全库无材料；重复同输入不重复缺口；忽略/仍不足/有帮助留记录；来源/父稿清理覆盖。

**验证**：go test ./internal/store -run TestEvidenceGap -count=1；父版本变化与处置CAS。

**建议提交**：`feat(learning): g01 建立可处置的材料缺口投影`。实际标题应描述最终行为。

**完成记录**：commit待填；当前验证/证据待填；真实接口/人工/设备待验条件单列。

<a id="g02"></a>
### G02 — 检索缺口候选并确认范围

**状态**：待实施。**依赖**：G01, S06。**粒度**：0.5–1.5日。

**文件/所有权**：store/evidence_gaps.go；server/evidence_gaps.go（新）；现有QuestionRelation命令。

**交付**：明确本地检索最多20候选，显示真实位置/匹配原因/关联状态，确认关系走现有CAS。

**验收（含失败路径）**：无模型调用；FTS和可选语义都有覆盖说明；候选不静默授权或被确认；local_only可本地读但不能新增外发权；未处理Source不自动处理。

**验证**：store/server检索、权限、关系重复/旧修订测试；GET无AI计数。

**建议提交**：`feat(learning): g02 检索缺口候选并确认范围`。实际标题应描述最终行为。

**完成记录**：commit待填；当前验证/证据待填；真实接口/人工/设备待验条件单列。

<a id="g03"></a>
### G03 — 扩展区间队列身份与迁移

**状态**：待实施。**依赖**：G01。**粒度**：0.5–1.5日。

**文件/所有权**：migrations/<next>_learning_excerpts.sql；store/listening_queue.go；models.ListeningQueueItem；store/learning_excerpts.go（新）。

**交付**：新增真实window/snapshot/audioSHA身份与部分唯一索引，旧整集/DJ条目和ID/顺序保留。

**验收（含失败路径）**：同集两区间并存；同区间重复幂等；范围必须真实Segments且start<end；队列CAS/500项上限；升级保持当前项；purge/换音频明确失效。

**验证**：go test ./internal/store -run "LearningExcerpt|ListeningQueue" -race -count=1；真实0069旧队列升级。

**建议提交**：`feat(learning): g03 扩展区间队列身份与迁移`。实际标题应描述最终行为。

**完成记录**：commit待填；当前验证/证据待填；真实接口/人工/设备待验条件单列。

<a id="g04"></a>
### G04 — 播放补听区间并隔离进度

**状态**：待实施。**依赖**：G03, R03。**粒度**：0.5–1.5日。

**文件/所有权**：static/listening-session.js；static/playback-controller.js；static/listening-queue.js；store/listening_progress.go；queue模板。

**交付**：原音真实区间起止、局部游标及绝对NoteAnchor；区间结束遵守显式连播，整集/DJ进度独立。

**验收（含失败路径）**：刷新暂停同区间；末尾不循环或伪装听完；睡眠先停止；原音替换/文件丢失不播放；手动循环明确优先；笔记位置绝对正确。

**验证**：实际播放器Node测试；浏览器两个区间/整集/DJ切换、睡眠及位置保存。

**建议提交**：`feat(learning): g04 播放补听区间并隔离进度`。实际标题应描述最终行为。

**完成记录**：commit待填；当前验证/证据待填；真实接口/人工/设备待验条件单列。

<a id="g05"></a>
### G05 — 交付缺口补听页面与下一步

**状态**：待实施。**依赖**：G02, G04, A02。**粒度**：0.5–1.5日。

**文件/所有权**：templates/question_gaps.html（新）；static/evidence-gaps.js（新）；server/evidence_gaps.go；dashboard投影。

**交付**：找材料、确认、加入补听、文档阅读、Owner处置连接成路径，首页能解释推荐来历。

**验收（含失败路径）**：建议未确认不进入生成范围；文档不加音频队列；同动作不重复；主视图/草稿保留；无JS原链接/操作可用；不会自动关闭缺口。

**验证**：HTTP/Node检查；浏览器从问题→缺口→真实片段→整理，截图/焦点。

**建议提交**：`feat(learning): g05 交付缺口补听页面与下一步`。实际标题应描述最终行为。

**完成记录**：commit待填；当前验证/证据待填；真实接口/人工/设备待验条件单列。

<a id="g06"></a>
### G06 — 验收补听的失效与恢复

**状态**：待实施。**依赖**：G05, C09。**粒度**：0.5–1.5日。

**文件/所有权**：store/learning_excerpts_test.go；server/evidence_gaps_test.go；browser harness；backup组合测试。

**交付**：覆盖多区间、父稿变化、source删除、hash变化、重复加入、旧队列升级及备份恢复。

**验收（含失败路径）**：旧整集/DJ功能不退化；来源清理不留下可播放旧缓存；已付费账本仍在；读/播放不触发新DJ/转录/AI。

**验证**：相关race、真实迁移/备份；浏览器跨页播放/失效提示；当前记录。

**建议提交**：`test(learning): g06 验收补听的失效与恢复`。实际标题应描述最终行为。

**完成记录**：commit待填；当前验证/证据待填；真实接口/人工/设备待验条件单列。


## 9. U：理解快照（M5）

<a id="u01"></a>
### U01 — 建立无伪来源的理解快照模型

**状态**：待实施。**依赖**：A01。**粒度**：0.5–1.5日。

**文件/所有权**：migrations/<next>_understanding_snapshots.sql；store/understanding_snapshots.go（新）；QuestionRelation及purge。

**交付**：独立不可变正文/多来源Reference/自身model_data_policy和允许Provider清单；理解头修订/当前选择分开。

**验收（含失败路径）**：无Source可保存Owner想法；不放宽OwnerNote纪律；参考版本真实；来源purge清掉参考正文但保留Owner答案；删除问题清理快照；备份可读。

**验证**：go test ./internal/store -run TestUnderstandingSnapshotModel -count=1；迁移/无来源/多源清理。

**建议提交**：`feat(learning): u01 建立无伪来源的理解快照模型`。实际标题应描述最终行为。

**完成记录**：commit待填；当前验证/证据待填；真实接口/人工/设备待验条件单列。

<a id="u02"></a>
### U02 — 实现保存与选择当前的CAS命令

**状态**：待实施。**依赖**：U01。**粒度**：0.5–1.5日。

**文件/所有权**：store/understanding_snapshots.go；server/understanding_snapshots.go（新）；routes.go。

**交付**：保存新快照、明确选择当前、历史分页，UUID/hash幂等和理解头CAS，冻结问题修订。

**验收（含失败路径）**：相同输入唯一；旧理解头/问题变更409保留；不自动判断正确/解决问题；修改产生新版本不重写父稿；tx中所有引用校验同连接。

**验证**：go test ./internal/store ./internal/server -run TestUnderstandingSnapshotSave -race -count=1；故障回滚/鉴权。

**建议提交**：`feat(learning): u02 实现保存与选择当前的CAS命令`。实际标题应描述最终行为。

**完成记录**：commit待填；当前验证/证据待填；真实接口/人工/设备待验条件单列。

<a id="u03"></a>
### U03 — 交付理解编辑器与历史差异

**状态**：待实施。**依赖**：U02, C07, A02。**粒度**：0.5–1.5日。

**文件/所有权**：templates/question_understandings.html（新）；static/understandings.js（新）；learning_question模板；对话草稿接口。

**交付**：当前回答/不确定/下一步编辑，可从笔记/对话引用到草稿，展示历史正文/参考变化。

**验收（含失败路径）**：模型文字需Owner确认；未提交编辑跨页/父变化保留；当前/历史区别明确；旧参考变化/清理提示；不自动新增OwnerNote。

**验证**：HTTP/实际Node；浏览器两窗口答案冲突、历史选择、来源清理与焦点。

**建议提交**：`feat(learning): u03 交付理解编辑器与历史差异`。实际标题应描述最终行为。

**完成记录**：commit待填；当前验证/证据待填；真实接口/人工/设备待验条件单列。

<a id="u04"></a>
### U04 — 接入理解的检索与文章选材

**状态**：待实施。**依赖**：U02, S03, S06, C02。**粒度**：0.5–1.5日。

**文件/所有权**：store/knowledge_search.go；store/knowledge_recall.go；provider material类别；embedding eligibility；FrozenLearningQuestion过滤。

**交付**：当前Owner理解作为独立material kind，确认的问题关系和多来源聚合权限共同控制选材；历史仅显式查询。

**验收（含失败路径）**：自身缺省local_only；每个外发参考来源同时允许；空source_id不绕权；内容仍Owner理解/Reference；旧材料类别与范围兼容；更新驱动已有文章提案。

**验证**：store/provider/queue聚合权限、无Source、版本/撤回测试；新旧prompt输入比较。

**建议提交**：`feat(learning): u04 接入理解的检索与文章选材`。实际标题应描述最终行为。

**完成记录**：commit待填；当前验证/证据待填；真实接口/人工/设备待验条件单列。

<a id="u05"></a>
### U05 — 验收理解演变与清理恢复

**状态**：待实施。**依赖**：U03, U04。**粒度**：0.5–1.5日。

**文件/所有权**：store/understanding_snapshots_test.go；server相关测试；组合备份/harness；validation。

**交付**：验证跨集理解→新笔记/新材料→新快照→当前选择→导出/选材读的完整历史。

**验收（含失败路径）**：旧正文/版本不重写；共享Source purge即时移除参考与向量；Owner正文保留且不可虚构出处；双窗口/恢复唯一当前。

**验证**：相关race、真实迁移/v2恢复和浏览器；真人“理解更清楚”评分单独待验。

**建议提交**：`test(learning): u05 验收理解演变与清理恢复`。实际标题应描述最终行为。

**完成记录**：commit待填；当前验证/证据待填；真实接口/人工/设备待验条件单列。


## 10. W：文章用途（M6）

<a id="w01"></a>
### W01 — 定义用途目录与新提示版本

**状态**：待实施。**依赖**：A01。**粒度**：0.5–1.5日。

**文件/所有权**：provider/writing_modes.go（新）；provider/knowledge_article.go；provider/knowledge_config.go；prompt支持测试。

**交付**：默认综合/知识解释/观点对比/实践指南版本化目录；新请求版本包含用途字段，旧v1–v4按原契约读。

**验收（含失败路径）**：用途不扩大权限；规则/目录fingerprint稳定；旧提示/输出上限不改写；未知用途/版本明确拒绝；无配置时仍有默认目录。

**验证**：go test ./internal/provider -run "WritingMode|KnowledgePromptCompatibility" -count=1；旧请求序列金样。

**建议提交**：`feat(learning): w01 定义用途目录与新提示版本`。实际标题应描述最终行为。

**完成记录**：commit待填；当前验证/证据待填；真实接口/人工/设备待验条件单列。

<a id="w02"></a>
### W02 — 冻结用途到所有文章阶段

**状态**：待实施。**依赖**：W01, A06, U04。**粒度**：0.5–1.5日。

**文件/所有权**：store/knowledge_discovery.go；store/knowledge_articles.go；store/knowledge_revisions.go；store/knowledge_estimates.go；迁移/元数据。

**交付**：发现/候选/成稿/修订/审校共享冻结用途；幂等身份纳入用途版本；显式用途修订保留旧通过版。

**验收（含失败路径）**：用途改变新输入/新估价；相同范围不同用途可识别；不把风格改用途当知识增量；旧任务恢复零付费重复；理解材料按真实类别外发。

**验证**：store/queue用途跨阶段和旧断点恢复race；真实旧文章升级。

**建议提交**：`feat(learning): w02 冻结用途到所有文章阶段`。实际标题应描述最终行为。

**完成记录**：commit待填；当前验证/证据待填；真实接口/人工/设备待验条件单列。

<a id="w03"></a>
### W03 — 实现用途结构及审校判据

**状态**：待实施。**依赖**：W02。**粒度**：0.5–1.5日。

**文件/所有权**：provider/knowledge_article.go；provider/knowledge_selection.go；queue/knowledge_article.go；evalset用途夹具。

**交付**：将用途约束进入选题/写作/审校的实际消息；结构检查支持各模式，默认综合保留当前行为。

**验收（含失败路径）**：解释缺例子不编造；比较保留条件；实践不编造经历/效果；输出超限保留receipt但不推进；模式要求不由CSS标签代替。

**验证**：provider/queue结构与坏结果测试；自建同素材至少两模式对照。

**建议提交**：`feat(learning): w03 实现用途结构及审校判据`。实际标题应描述最终行为。

**完成记录**：commit待填；当前验证/证据待填；真实接口/人工/设备待验条件单列。

<a id="w04"></a>
### W04 — 持久化可选写作计划与确认

**状态**：待实施。**依赖**：W03。**粒度**：0.5–1.5日。

**文件/所有权**：migrations/<next>_knowledge_writing_plans.sql；store/knowledge_writing_plans.go（新）；queue选材→写作准入。

**交付**：选材后可暂停形成计划，Owner编辑产生新修订，确认确切版本再预算准入；默认不暂停。

**验收（含失败路径）**：计划/材料/用途指纹固定；同确认唯一写作任务；旧计划409；预算/控制阻断不丢计划；自动任务只按明确预览偏好等待。

**验证**：store/queue计划确认竞争、故障/预算/重复提交race；旧自动流程回归。

**建议提交**：`feat(learning): w04 持久化可选写作计划与确认`。实际标题应描述最终行为。

**完成记录**：commit待填；当前验证/证据待填；真实接口/人工/设备待验条件单列。

<a id="w05"></a>
### W05 — 交付用途与计划交互

**状态**：待实施。**依赖**：W04, A03。**粒度**：0.5–1.5日。

**文件/所有权**：knowledge_articles/knowledge_article模板；static/knowledge-articles.js；server/knowledge_articles.go。

**交付**：选用途、预览开关、计划编辑/确认、任务阶段/估价说明；已生成文章用途变化走明确修订。

**验收（含失败路径）**：默认生成一路到审校；看计划不付费；保存输入/CAS失败不丢稿；局部导航保留audio；SSR表单与旧偏好可读。

**验证**：HTTP/Node；浏览器默认自动和预览确认两条路径。

**建议提交**：`feat(learning): w05 交付用途与计划交互`。实际标题应描述最终行为。

**完成记录**：commit待填；当前验证/证据待填；真实接口/人工/设备待验条件单列。

<a id="w06"></a>
### W06 — 验收四用途及旧通过版兼容

**状态**：待实施。**依赖**：W05, G06, U05。**粒度**：0.5–1.5日。

**文件/所有权**：provider/queue/server用途评测；本轮固定评测入口；validation。

**交付**：固定四用途结构、引用、归因和计划故障回归，真实POD有界对照及独立审校模型身份登记。

**验收（含失败路径）**：不回写旧审校哈希；改用途后旧通过版仍可读；无材料/未知费用不伪造成功；人工有用性单列，不把Fake/模型通过当质量通过。

**验证**：相关race+固定Fake评测；可选真实POD同样本一次对照；指纹/receipt报告。

**建议提交**：`test(learning): w06 验收四用途及旧通过版兼容`。实际标题应描述最终行为。

**完成记录**：commit待填；当前验证/证据待填；真实接口/人工/设备待验条件单列。


## 11. F：反馈评测（M7）

<a id="f01"></a>
### F01 — 将段落反馈冻结为版本案例

**状态**：待实施。**依赖**：A01, W02。**粒度**：0.5–1.5日。

**文件/所有权**：migrations/<next>_article_feedback_cases.sql；store/article_feedback_cases.go（新）；现有feedback扩展。

**交付**：反馈锚定文章/正文哈希/稳定段落或legacy位置hash，接纳案例与反馈本身分开，修订期望新版本。

**验收（含失败路径）**：不会回写文章或偏好；无法定位/旧版本不偷偷移到新段落；整篇有用无需假错误；重复接纳唯一；清理来源移除冻结正文。

**验证**：go test ./internal/store -run TestArticleFeedbackCase -count=1；legacy/正文变化/清理/恢复。

**建议提交**：`feat(learning): f01 将段落反馈冻结为版本案例`。实际标题应描述最终行为。

**完成记录**：commit待填；当前验证/证据待填；真实接口/人工/设备待验条件单列。

<a id="f02"></a>
### F02 — 给出材料不足与反馈的事实分类

**状态**：待实施。**依赖**：F01, S06, G01。**粒度**：0.5–1.5日。

**文件/所有权**：store/article_feedback_cases.go；store recall/coverage元数据；server质量投影。

**交付**：根据已记录覆盖与阶段结果展示材料薄弱/检索漏召回/容量遗漏等候选分类，Owner明确确认；未知保持未分类。

**验收（含失败路径）**：不声称全库缺材料；不把模型Reason当事实；本轮分类操作不新增诊断付费调用；新证据不静默改旧分类。

**验证**：分类表/缺历史字段/容量上限测试；GET零AI与权限投影检查。

**建议提交**：`feat(learning): f02 给出材料不足与反馈的事实分类`。实际标题应描述最终行为。

**完成记录**：commit待填；当前验证/证据待填；真实接口/人工/设备待验条件单列。

<a id="f03"></a>
### F03 — 交付段落反馈与案例管理

**状态**：待实施。**依赖**：F02, A02, W05。**粒度**：0.5–1.5日。

**文件/所有权**：knowledge_article模板/JS；templates/quality_cases.html（新）；server/article_feedback_cases.go；routes.go。

**交付**：选择段落、说明问题/期望、接纳/修订/退役案例，保留原四种反馈与显式写作偏好入口。

**验收（含失败路径）**：反馈引用确切正文；409保留原输入；案例接纳不调用模型；不会把私有正文或key暴露到公共报告；旧反馈可读。

**验证**：HTTP/实际Node；浏览器旧修订段落反馈与冲突恢复。

**建议提交**：`feat(learning): f03 交付段落反馈与案例管理`。实际标题应描述最终行为。

**完成记录**：commit待填；当前验证/证据待填；真实接口/人工/设备待验条件单列。

<a id="f04"></a>
### F04 — 把接纳案例接入冻结评测清单

**状态**：待实施。**依赖**：F01, A01, W03, C03。**粒度**：0.5–1.5日。

**文件/所有权**：internal/evalset/personal_learning_report.go；server评测入口；docs/evalset.md；私有案例模板。

**交付**：从明确接纳案例生成有界manifest，冻结来源/问题/用途/期望/版本；副本隔离运行并支持缺失human。

**验收（含失败路径）**：未接纳案例不自动外发；权限前置；同样本模型比较起点相同；报告包括原输出/receipt/失败；自动测试只用自建内容。

**验证**：evalset schema/权限/独立副本测试；Fake生成/失败报告完整性。

**建议提交**：`feat(learning): f04 把接纳案例接入冻结评测清单`。实际标题应描述最终行为。

**完成记录**：commit待填；当前验证/证据待填；真实接口/人工/设备待验条件单列。

<a id="f05"></a>
### F05 — 生成质量与估价回归比较报告

**状态**：待实施。**依赖**：F04, S07, C09, W06。**粒度**：0.5–1.5日。

**文件/所有权**：evalset报告/评测CLI或测试入口；docs/evalset.md；validation。

**交付**：比较归因/引用/缺口覆盖/结构/耗时/费用/估价误差；真实模型显式运行，生产提示不随报告自动切换。

**验收（含失败路径）**：比较确切阶段模型/提示/价格/语料；未知费用有数量不为0；human不自动赋分；严重归因错误单独失败；不同用途判据不混算。

**验证**：固定报告金样及真实有界对照入口；人工维度格式校验。

**建议提交**：`test(learning): f05 生成质量与估价回归比较报告`。实际标题应描述最终行为。

**完成记录**：commit待填；当前验证/证据待填；真实接口/人工/设备待验条件单列。

<a id="f06"></a>
### F06 — 验收私有质量案例生命周期

**状态**：待实施。**依赖**：F03, F05。**粒度**：0.5–1.5日。

**文件/所有权**：store/server feedback测试；source purge/backup组合；validation。

**交付**：案例接纳→重跑→退役与源撤回/文章删除/备份恢复；保留已发生用量，清除不应保留的正文。

**验收（含失败路径）**：旧案例不被新材料暗改；反馈仍Owner记录；无权限不能通过缓存外发；公开文档只含聚合指标；质量与技术状态分开。

**验证**：相关race、真实迁移/v2恢复；私有输出字段审查。

**建议提交**：`test(learning): f06 验收私有质量案例生命周期`。实际标题应描述最终行为。

**完成记录**：commit待填；当前验证/证据待填；真实接口/人工/设备待验条件单列。


## 12. E：学习成果包（M8）

<a id="e01"></a>
### E01 — 建立有界一致性导出预览

**状态**：待实施。**依赖**：U04, F01。**粒度**：0.5–1.5日。

**文件/所有权**：store/learning_exports.go（新）；server/learning_exports.go（新）；来源状态序列增量迁移。

**交付**：问题/Theme选择、对象/修订清单、当前/历史及草稿标记；短事务复制快照，给预览hash/source_state_seq。

**验收（含失败路径）**：默认只通过版；500对象/100MiB及100请求身份上限；选定范围完整且缺失明确；private Owner导出不误当模型外发；源撤回使预览过期。

**验证**：go test ./internal/store ./internal/server -run TestLearningExportPreview -count=1；大库/状态竞争。

**建议提交**：`feat(learning): e01 建立有界一致性导出预览`。实际标题应描述最终行为。

**完成记录**：commit待填；当前验证/证据待填；真实接口/人工/设备待验条件单列。

<a id="e02"></a>
### E02 — 实现确定性Markdown与ZIP清单

**状态**：待实施。**依赖**：E01。**粒度**：0.5–1.5日。

**文件/所有权**：internal/markdown/learning_bundle.go（新）；internal/learningbundle/或现有导出Module；manifest类型与测试。

**交付**：stable-id路径、中文正文、相对链接、Source定位、类别/版本；流式ZIP和hash/size清单。

**验收（含失败路径）**：离线可读；路径穿越/重复名/坏链接拒绝；文件顺序稳定；不包含key/Cookie/私人录音/任务原输入；超限明确失败。

**验证**：确定性夹具ZIP解包、所有内部链接/哈希及敏感字段检查；实际Markdown阅读。

**建议提交**：`feat(learning): e02 实现确定性Markdown与ZIP清单`。实际标题应描述最终行为。

**完成记录**：commit待填；当前验证/证据待填；真实接口/人工/设备待验条件单列。

<a id="e03"></a>
### E03 — 通过无模型任务生成和鉴权下载

**状态**：待实施。**依赖**：E02, A06。**粒度**：0.5–1.5日。

**文件/所有权**：queue/learning_exports.go（新）；store导出状态/文件；server下载；临时清理Worker。

**交付**：后台任务生成ZIP，私有0600文件、24小时过期；创建UUID幂等；下载复查来源序列和可读取状态。

**验收（含失败路径）**：无AI配置仍可生成；读/下载零模型/预算；关闭页面仍完成；撤回/purge竞争使文件失效；清理重启可恢复，不误删v2备份/录音。

**验证**：queue/server文件故障、auth/CSRF、下载前撤回、过期/重启测试。

**建议提交**：`feat(learning): e03 通过无模型任务生成和鉴权下载`。实际标题应描述最终行为。

**完成记录**：commit待填；当前验证/证据待填；真实接口/人工/设备待验条件单列。

<a id="e04"></a>
### E04 — 交付范围选择和导出状态页面

**状态**：待实施。**依赖**：E03, A02。**粒度**：0.5–1.5日。

**文件/所有权**：templates/learning_exports.html（新）；static/learning-exports.js（新）；问题/Theme入口；routes.go。

**交付**：预览数量/范围、历史/草稿选择、生成状态/下载/错误；明确成果包与实例备份不同。

**验收（含失败路径）**：旧预览不能直接确认新内容；轮询不改选择；文件缺失给重建动作；SSR可用；生成不暂停audio。

**验证**：HTTP/Node；浏览器问题导出、ZIP阅读及撤回失效。

**建议提交**：`feat(learning): e04 交付范围选择和导出状态页面`。实际标题应描述最终行为。

**完成记录**：commit待填；当前验证/证据待填；真实接口/人工/设备待验条件单列。

<a id="e05"></a>
### E05 — 验收成果包范围与恢复兼容

**状态**：待实施。**依赖**：E04, U05, F06。**粒度**：0.5–1.5日。

**文件/所有权**：learningbundle/store/server测试；backup恢复组合；validation。

**交付**：问题/Theme、多源理解、旧文章/历史/草稿、大范围和清理竞争完整对照。

**验收（含失败路径）**：每项来源/版本/链接正确；备份不含临时ZIP；恢复后可明确重建且零模型；原Markdown下载和v1/v2备份不退化。

**验证**：相关测试/race、解包内容核对与独立浏览器实际阅读。

**建议提交**：`test(learning): e05 验收成果包范围与恢复兼容`。实际标题应描述最终行为。

**完成记录**：commit待填；当前验证/证据待填；真实接口/人工/设备待验条件单列。


## 13. O：离线设备路径（M9）

<a id="o01"></a>
### O01 — 定义离线设备授权和撤回清单

**状态**：待实施。**依赖**：A01, R02, G03, U01, E01。**粒度**：0.5–1.5日。

**文件/所有权**：migrations/<next>_offline_learning.sql；store/offline_learning.go（新）；server/offline_learning.go；ADR草稿。

**交付**：冻结设备/session namespace、pack对象/hash/epoch、默认24小时有效期；同意显式下载，来源删除/登录撤回产生检查身份。

**验收（含失败路径）**：默认关闭；不写/发送凭据到清单；0条Source也可空提示；实时离线撤回有限明确记录；scope/源变化不能静默复用；副本恢复无浏览器音频。

**验证**：store/server授权/epoch/时钟/旧清单测试；ADR记录隐私与可用性取舍。

**建议提交**：`feat(learning): o01 定义离线设备授权和撤回清单`。实际标题应描述最终行为。

**完成记录**：commit待填；当前验证/证据待填；真实接口/人工/设备待验条件单列。

<a id="o02"></a>
### O02 — 实现能力探测与离线专用外壳

**状态**：待实施。**依赖**：O01, A02。**粒度**：0.5–1.5日。

**文件/所有权**：static/sw.js（实际根路径服务）；static/offline-shell.js；templates/offline.html（新）；manifest/static注册路由。

**交付**：只缓存版本化静态资源和离线专用投影；HTTPS/能力检测，安装/禁用/升级状态明确。

**验收（含失败路径）**：不cache登录/设置/任务/CSRF/私人录音及动态SSR；不支持时原站可用；注册scope正确；安装失败不标ready；不用后台同步作为依赖。

**验证**：HTTP缓存头与注册范围；实际浏览器HTTPS/localhost、禁用SW、安装失败与原SSR。

**建议提交**：`feat(learning): o02 实现能力探测与离线专用外壳`。实际标题应描述最终行为。

**完成记录**：commit待填；当前验证/证据待填；真实接口/人工/设备待验条件单列。

<a id="o03"></a>
### O03 — 提供已鉴权的原音和内容下载清单

**状态**：待实施。**依赖**：O01, O02。**粒度**：0.5–1.5日。

**文件/所有权**：server/offline_learning.go；store清单/availability；Source evidence音频下载复用。

**交付**：首版仅本地EvidenceAudio原音和选定转录/笔记/通过版；size/SHA/确切定位，失效源不可新下载。

**验收（含失败路径）**：远端URL/DJ Narration/私有录音不进入包；10来源/500MiB上限；认证/源删除/hash变化复查；预览GET不启动模型或转码。

**验证**：server鉴权/范围/字节hash/撤回竞争测试；独立数据目录音频夹具。

**建议提交**：`feat(learning): o03 提供已鉴权的原音和内容下载清单`。实际标题应描述最终行为。

**完成记录**：commit待填；当前验证/证据待填；真实接口/人工/设备待验条件单列。

<a id="o04"></a>
### O04 — 持久下载与配额错误恢复

**状态**：待实施。**依赖**：O03。**粒度**：0.5–1.5日。

**文件/所有权**：static/offline-packs.js（新）；IndexedDB/Cache版本定义；templates/offline.html。

**交付**：完整文件验证size/hash后ready，临时/失败状态分开；总量和浏览器估计配额的50%检查；明确清理选择。

**验收（含失败路径）**：断网/取消/QuotaExceeded/坏hash/缓存驱逐不标可用；不自动清未同步草稿或保留包；重载可核对已完成文件；不保证永久存储。

**验证**：实际脚本存储故障测试；浏览器下载中断、容量模拟与缓存文件删后提示。

**建议提交**：`feat(learning): o04 持久下载与配额错误恢复`。实际标题应描述最终行为。

**完成记录**：commit待填；当前验证/证据待填；真实接口/人工/设备待验条件单列。

<a id="o05"></a>
### O05 — 支持离线原音Range与真实锚点

**状态**：待实施。**依赖**：O04, G04。**粒度**：0.5–1.5日。

**文件/所有权**：static/sw.js；static/offline-shell.js；static/listening-session.js；playback-controller与Range帮助函数。

**交付**：仅完整验证文件提供200/单Range206/416，离线播放用原audioSHA/snapshot锚点；整集/补听独立。

**验收（含失败路径）**：seek/刷新/区间起止正确；坏Range不返回错字节；Blob URL不当来源身份；音频被驱逐即不可用；不做DJ/远端下载兜底。

**验证**：Range字节级/EOF测试；真实offline浏览器原音seek/片段播放与笔记捕获。

**建议提交**：`feat(learning): o05 支持离线原音Range与真实锚点`。实际标题应描述最终行为。

**完成记录**：commit待填；当前验证/证据待填；真实接口/人工/设备待验条件单列。

<a id="o06"></a>
### O06 — 保存可同步的本地操作队列

**状态**：待实施。**依赖**：O02, R02。**粒度**：0.5–1.5日。

**文件/所有权**：static/offline-operations.js（新）；现有笔记/整理/播放本地草稿接口；offline UI。

**交付**：仅文字整理/笔记草稿/原音进度可入outbox，UUID/hash/schema/namespace/expectedRevision/锚点完整，显示待同步。

**验收（含失败路径）**：保存失败可复制文字；401/logout隔离；不入队生成/ASR/控制/预算变更；草稿≤既有上限；操作ack前不清理。

**验证**：实际Node/IndexedDB故障、同operation编辑版本与reload测试；离线UI。

**建议提交**：`feat(learning): o06 保存可同步的本地操作队列`。实际标题应描述最终行为。

**完成记录**：commit待填；当前验证/证据待填；真实接口/人工/设备待验条件单列。

<a id="o07"></a>
### O07 — 服务端原子同步与去重

**状态**：待实施。**依赖**：O01, O06, R02。**粒度**：0.5–1.5日。

**文件/所有权**：store/offline_operations.go（新）；server/offline_learning.go；owner_notes/进度/整理事务Interface；迁移。

**交付**：前台明确同步，认证/CSRF/session/epoch后按UUID/hash执行合法写入，每条返回ack/conflict/unavailable。

**验收（含失败路径）**：响应丢失重复同步不重复笔记；同UUID异hash409；旧修订/删除源保留客户端文字；同步不转写/发模型；事务故障无半保存；不更换来源。

**验证**：store/server同步竞争/race、认证/撤回/响应丢失和rollback；调用计数0。

**建议提交**：`feat(learning): o07 服务端原子同步与去重`。实际标题应描述最终行为。

**完成记录**：commit待填；当前验证/证据待填；真实接口/人工/设备待验条件单列。

<a id="o08"></a>
### O08 — 离线重开、到期与登出清理

**状态**：待实施。**依赖**：O04, O06, O07。**粒度**：0.5–1.5日。

**文件/所有权**：static/offline-shell.js；static/private-state.js；static/sw.js；store撤回/session核对；templates/offline.html。

**交付**：有效期内既有namespace离线重开；过期锁定；回网先验授权/撤回，再显示/同步；显式logout清理所有设备私有副本。

**验收（含失败路径）**：不能cacheHTML伪装登录；新登录namespace不继承旧内容；跨标签立即停止媒体/录音；服务器撤回离线不能实时感知须明确；回网撤回先清缓存后拒旧操作。

**验证**：时钟/广播/过期Node；浏览器离线关闭重开、回网撤回、同/换登录与缓存清理。

**建议提交**：`feat(learning): o08 离线重开、到期与登出清理`。实际标题应描述最终行为。

**完成记录**：commit待填；当前验证/证据待填；真实接口/人工/设备待验条件单列。

<a id="o09"></a>
### O09 — 安全更新SW与清理下载副本

**状态**：待实施。**依赖**：O02, O04。**粒度**：0.5–1.5日。

**文件/所有权**：static/sw.js；static/offline-packs.js；static/offline-shell.js；离线settings UI。

**交付**：新缓存完整再切换，播放/录音/同步在途不强行更新；旧缓存/临时文件的明确清理与版本不兼容阻断。

**验收（含失败路径）**：更新不刷新在途录音/播放；坏新缓存不替换旧可用包；schema不兼容保留草稿可核对；清理下载不删待同步操作。

**验证**：SW生命周期/存储版本脚本测试；浏览器两个客户端、更新失败、录音在途与清理。

**建议提交**：`feat(learning): o09 安全更新SW与清理下载副本`。实际标题应描述最终行为。

**完成记录**：commit待填；当前验证/证据待填；真实接口/人工/设备待验条件单列。

<a id="o10"></a>
### O10 — 完成真实离线综合故障验收

**状态**：待实施。**依赖**：O05, O08, O09, E05。**粒度**：0.5–1.5日。

**文件/所有权**：browser_acceptance_test.go；离线Node/HTTP测试；validation及私有浏览器证据。

**交付**：实际offline开关、重开/Range/草稿/回网/ack丢失/CAS/撤回/过期/配额/文件驱逐/SW升级整套路径。

**验收（含失败路径）**：每个状态可解释；零自动付费；去重唯一笔记/合法进度；登出无私有缓存；所有截图查看；桌面与手机结论分开。

**验证**：相关race与独立浏览器；字节hash/DB/IndexedDB/Cache对照，停止测试实例后修复并复验。

**建议提交**：`test(learning): o10 完成真实离线综合故障验收`。实际标题应描述最终行为。

**完成记录**：commit待填；当前验证/证据待填；真实接口/人工/设备待验条件单列。

<a id="o11"></a>
### O11 — 登记实体手机使用与待验状态

**状态**：待实施。**依赖**：O10, R06, C09。**粒度**：0.5–1日，不含等待设备。

**文件/所有权**：docs/acceptance/*v4-mobile.md（新）；validation；当前HTTPS部署检查清单。

**交付**：按实际设备记录系统/浏览器/HTTPS/时间，原音后台15分钟、麦克风两段自然录音、离线重开/同步、源删除/logout。

**验收（含失败路径）**：没有设备则逐项待验证且说明具体缺口；不能用375px/桩录音代填；部署配置改变是独立授权动作；不为了完成记录编造质量。

**验证**：可用实体设备实际旅程；若缺设备只检查记录模板/入口，技术验收与产品验收分开。

**建议提交**：`docs(learning): o11 登记实体手机使用与待验状态`。实际标题应描述最终行为。

**完成记录**：commit待填；当前验证/证据待填；真实接口/人工/设备待验条件单列。


## 14. V：综合交付（M10）

<a id="v01"></a>
### V01 — 综合验证0069升级及v2恢复

**状态**：待实施。**依赖**：R06, S07, C09, G06, U05, W06, F06, E05, O10。**粒度**：0.5–1.5日。

**文件/所有权**：store历史迁移测试；backup/server组合恢复测试；queue恢复夹具；validation。

**交付**：真实0069旧库含旧文章/StudyChat/队列/已知断点/未知调用/预算控制，升级并v2备份到独立目录恢复。

**验收（含失败路径）**：新问题/理解/整理/对话/用途/案例/控制全部一致；私人录音仍保留；临时导出/设备缓存不进包；已知响应0付费重发，旧unknown不伪造成功。

**验证**：真实迁移与完整store.Open，哈希/对象/receipt/控制对照；模板复制不能代替。

**建议提交**：`test(learning): v01 综合验证0069升级及v2恢复`。实际标题应描述最终行为。

**完成记录**：commit待填；当前验证/证据待填；真实接口/人工/设备待验条件单列。

<a id="v02"></a>
### V02 — 验收完整听学成文旅程

**状态**：待实施。**依赖**：V01, O11。**粒度**：0.5–1.5日。

**文件/所有权**：server/harness；evalset/private评测入口；本轮acceptance/validation。

**交付**：播放→整理→问题对话→缺口补听→理解快照→两用途文章→段落反馈→导出→离线同步，同一问题串联。

**验收（含失败路径）**：至少10次导航同audio/正确锚点；故障/冲突保留文字；真实模型/人工/手机分别记录；固定资料同一副本不互相污染；无资料不杜撰。

**验证**：独立浏览器/确定性provider，必要真实接口显式预算检查，五维人工评分模板与严重错误记录。

**建议提交**：`test(learning): v02 验收完整听学成文旅程`。实际标题应描述最终行为。

**完成记录**：commit待填；当前验证/证据待填；真实接口/人工/设备待验条件单列。

<a id="v03"></a>
### V03 — 执行最终技术门禁

**状态**：待实施。**依赖**：V01, V02。**粒度**：0.5–1.5日。

**文件/所有权**：全部生产源码及测试；scripts/cover-gate.sh（门槛不改）；私有日志。

**交付**：最终源码全量race/覆盖率/lint/vet/build/静态JS与diff检查，记录当前版本及缓存适用性。

**验收（含失败路径）**：95%规则及store78.4/server79.4地板不降低、不增豁免；失败修根因后重跑受影响检查；源码变更不能沿用旧通过记录。

**验证**：make test；make cover-gate；make lint；go vet ./...；go build ./cmd/cloudwisepod；所有JS node --check；git diff --check。

**建议提交**：`test(learning): v03 执行最终技术门禁`。实际标题应描述最终行为。

**完成记录**：commit待填；当前验证/证据待填；真实接口/人工/设备待验条件单列。

<a id="v04"></a>
### V04 — 完成使用、配置、部署与验收文档

**状态**：待实施。**依赖**：V03。**粒度**：0.5–1.5日。

**文件/所有权**：docs/personal-learning-v4.md（新）；README.md；.env.example；docs/production-deployment.md；evalset.md；v4-validation/acceptance；CONTEXT.md。

**交付**：说明七功能实际操作、语义/对话连接、费用、HTTPS/离线期限、草稿/清理、导出vs备份和恢复；设计术语按实际实施更新。

**验收（含失败路径）**：路由/字段/默认值与实际一致；真实Owner库未部署升级就不能写已部署；技术/模型/人工/设备状态分开；本地及外部链接可用，无密钥/真实正文。

**验证**：文档链接/术语/实际路由对照；git diff --check；不为纯文档重跑全量测试。

**建议提交**：`docs(learning): v04 完成使用、配置、部署与验收文档`。实际标题应描述最终行为。

**完成记录**：commit待填；当前验证/证据待填；真实接口/人工/设备待验条件单列。

<a id="v05"></a>
### V05 — 核对任务闭合并交付提交

**状态**：待实施。**依赖**：V04。**粒度**：0.5日。

**文件/所有权**：本任务清单；v4计划/validation/acceptance；Git提交。

**交付**：每卡登记实际commit/证据或明确未完成项，完成最后一致提交及工作树核对；产品待验项单列。

**验收（含失败路径）**：不能仅据勾选推断通过；全部技术任务实际完成才声明工程完成；人工/设备未验不隐藏；无未解释源码变更，不自动部署/发布。

**验证**：核对任务依赖/提交/证据，git status与最终HEAD，报告实际门禁和未验证项。

**建议提交**：`docs(learning): v05 核对任务闭合并交付提交`。实际标题应描述最终行为。

**完成记录**：commit待填；当前验证/证据待填；真实接口/人工/设备待验条件单列。

## 15. 独立产品验收清单

- [ ] 真实3集节目/20条个人笔记/2问题及一次更新；Owner尚无笔记，等待日常积累，不以自建资料代填。
- [ ] 真实embedding接口及改写召回收益；未配置时保留待验，FTS降级可独立交付。
- [ ] 真实问题对话与用途模式输出、确切阶段模型/估价偏差及独立审校对照。
- [ ] Owner五维平均≥4且无严重事实/归因错误；模型审校与Fake不代替人工评分。
- [ ] iOS Safari/Android Chrome实体手机HTTPS、自然录音、后台15分钟、断网重开/同步、源删除与退出。

以上与技术任务状态分别记录。V05只有核对了全部技术产出才可宣布工程交付完成；缺设备或人工资料时保留对应待验状态，不要求反复确认用户已经明确没有的资料。
