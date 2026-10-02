# V02 同题完整旅程验收

最终同题绑定版本已实际通过：独立 `TestPersonalLearningV4CrossModuleJourney` race 1 个命中测试，13.638s；最终 opt-in `TestPersonalLearningV4BrowserHarness` 1 个命中测试，889.36s，终态 PASS。未修改其他模块业务源码，未 commit。

## 当前正式证据

最终 fixture 于 2026-10-02 14:05:52 启动，独立 SQLite scratch、loopback 18131，正式身份/启动与终态见 [fixture.json](final/fixture.json) 和 [harness.log](final/harness.log)。原音为实际本地 WAV，问答为实际 HTTP 模型协议；文章为本地确定性测试 Provider。它们证明集成与状态合同，不证明真实模型文章质量。

3 个不同来源、20 条明确自建个人笔记、2 个独立问题。q1 `485617f0-d5d0-4821-a7ce-2f5853fc4a2a` 依次串起明确关联的反思、三来源条件分歧与双阶段问答、缺口、20–40s 补听、理解 v1/v2、精确显式理解关系、同文章综合通过 v1 → 解释通过 v3、反馈、ZIP 导出及 offline 同步。q2 保留独立范围；第二用途使用 q1 原文章真实用途修订，未用另一个问题冒充。旧通过 v1 哈希保持不变。

自动化通过真实认证/CSRF Router、Store 和 Worker：generation paid response receipt 写入失败，已知 checkpoint 恢复供应商调用数不增加；旧缺口修订 409 保留判断草稿；旧理解 head 409 恢复表单精确保留 answer/head/parent/request UUID；篡改 export preview hash 拒绝，原合法预览随后成功；同 UUID offline acknowledgement replay 原子复用，导出及 offline 前后不增加模型调用。

## 同一个最终数据库的实际浏览器

Owner 在首页明确选择 q1，再播放原音并将位置设为 25 秒；“听后整理”显示 q1 与原音绝对位置 25.1 秒。真实填写并保存反思后，q1 页面实际显示对应已确认笔记关系。证据 [反思捕获](final/reflection-anchor.json)、[保存后的关系](final/reflection-q1.json) 与 [页面截图](final/reflection-q1.png)。

真实可见 root 播放按钮启动后，使用公开产品 `CWPNavigation.visit` 连续导航 10 次；每次实际 URL 精确命中、audio DOM 对象严格相同、paused=false，时间 49.896897→51.305495；原音笔记、反思及下一问三份草稿保持。未伪造页面或播放响应。见 [逐次观察](final/ten-nav.json)、[截图](final/ten-nav.png)，复跑驱动为 [continuous-navigation-driver.js](continuous-navigation-driver.js)。

同 scope 实际查看问答通过内容、理解两版本、同文章两种用途及旧通过版： [问答页面文字](final/question-study.json)、[理解截图](final/understanding-v1-v2.png)、[综合 v1](final/synthesis-r1.png)、[解释 v3](final/explanation-r3.png)。实际点击文章段落质量入口、填写和提交反馈，再读页面确认 saved=true，见 [结果](final/feedback.json) 与 [截图](final/feedback.png)。

反馈变化后重新预览并确认导出，Worker 到 ready，再从可见 ZIP 链接实际浏览器下载。最终 ZIP SHA256 为 `736c243882c9332e10a090ab0ffe0dad19fcf9224a0a0911b6662b22491789e9`；scope 精确为 q1，11 个 manifest 文件全部 size/SHA256 一致，ZIP CRC 无错误，包含同文章 r1/r3、理解两版及明确关联的反思笔记。见 [ZIP 校验](final/export-zip.json) 与 [ready 截图](final/export-ready.png)。旧 E 代理 reader 的中文阅读、内部链接和阅读器截图属于此前 fixture 的独立格式验收，单独继承，未冒充最终 ZIP 的重新阅读。

同 fixture 真正授权下载原音和 excerpt，设置断网（navigator.onLine=false），播放缓存原音、填写并实际保存个人整理。UI显示“已持久保存，等待明确同步”及原文字；恢复网络后实际点击明确同步，显示1条已保存/0条需核对，再查看服务器只读整理修订1及精确文字。见 [断网观察](final/offline-pending.json)、[断网截图](final/offline-pending.png)、[同步结果](final/offline-sync.json)、[服务器结果](final/offline-synced.json)、[截图](final/offline-synced.png)。真实点击离线补听，实际播放位置20.073976秒、paused=false，见 [补听观察](final/offline-excerpt.json) 与 [截图](final/offline-excerpt.png)。丢失 acknowledgement 的同 UUID replay 由前述真实 HTTP 测试单独证明，未把浏览器空待同步伪称回放。

## 终态与证据边界

最终3个自有浏览器 session 已关闭，18131 通过显式 stop 文件结束；最后仅统计上述正式 fixture：模型 question=2、article=6；8个实际 usage receipts，各 operation 1 条；任务 succeeded=14、failed=1。failed 是刻意触发记账失败的原尝试，已知恢复另建后继任务并保留失败事实。详细类别与 PASS 退出见 [终态日志](final/harness.log)。

中间轮次新增终态仪表时误用了不存在的 `jobs` 表，导致 logger FAIL；已修成实际 `processing_jobs` 并完整重跑最终 fixture，旧轮次不计 PASS，其 UI 材料仅保留在 `prior-binding/`。另一次导出创建后新增真实反馈，使 migration 0086 的全局 export state sequence 按设计变化：原文件的验证返回冲突、轮询隐藏下载；未将等待中的旧下载计成功，最终使用更新后的明确预览重新生成并下载，未绕过校验。最初工具 click 不自动滚动的观察由 G 查清：可见性不足时 Done 不代表点击目标；正式轮次均先将目标滚动可见，十导航以真实按钮启动。

真实商业模型、真实个人学习资料和人类评分仍没有输入，保持待验证；自建20条笔记与协议 Provider 不能替代这些质量证据。

## V02.2 停止/清理的明确限制

14:05:52 浏览器 fixture 未注入业务 stop/purge，关闭浏览器及显式停止 harness 不算这些故障。随后新增独立 `TestPersonalLearningV4SameFixtureStopAndPurge`，先在其隔离固定副本真实完成全套同题 journey，再在这个相同数据库/同 q1 链注入 Owner stop/purge；1 个命中父测试目标 race PASS，方法8.91s、包10.652s。正式 [race原始日志](final/same-fixture-faults-race.log) 与 [独立测试](../../../../internal/server/personal_learning_v4_faults_test.go) 保存当前结果。它是新的同题工程副本，不冒充14:05:52浏览器DB的后续点击。

当前新增组合验证：真实Owner认证/CSRF `/automation/action` 停止新queued问答，Owner原问题输入、此前反思笔记与理解Answer不丢、Worker不再dispatch；真实 `/api/purge`（含文件清理）删除sourceA，来源派生文章/冻结问答/任务input与checkpoint清除。8个真实usage记录逐列严格等同（不仅行数），模型前后2/6未增加；Owner理解Answer保留而引用body清空/purged，既有offline个人整理保持原文字、SourceAvailable=false；旧ZIP、旧pack授权及文件拒绝，旧pending operation按原UUID返回unavailable/无ack/不生成第二条整理。保留的是Owner理解及可复制个人整理：明确purge的源绑定reflection/turnOwnerInput按privacy合同主动清理，未把保留它们错误设为验收目标。

传入但未同步的operation仍作为调用端原请求字节留存；Go断言不代表浏览器IndexedDB本地草稿的purge后实测。真实浏览器草稿/断网保存由前述正式轮次证明，来源变更后的浏览器可复制文字由下面独立O模块证据继承。late paid response的停止/清理竞态未在新增组合test重复供应商调用，精确继承C09：

- [TestQuestionStudyGenerationLateStopAndPurgeKeepPaidFacts](../../../../internal/queue/question_study_test.go)（190行，`stop`/`purge`两子场景）使用真实 Store/Worker/HTTP 协议。在供应商调用中实际 `ChangeRunControl(...,"stop",...)` 或 `DeleteSourceRows`，迟到回答 blocked、不进历史；实际1调用及31已知 input units receipt 保留。purge 后 frozen/input JSON 清空、checkpoint不可回填。[C09组合执行记录](../c09/2026-10-02-question-recovery.md)的41父/14子 race包含此测试。它是独立 queue 临时库，未在本次 V02 同题浏览器数据库执行。
- [TestQuestionStudyCommitStopAfterPaidReviewSuppressesAnswer](../../../../internal/queue/question_study_review_test.go)（137行）在付费 review 后停止，实际生成/检查2调用与检查receipt保留，history为空。该 stop 使用数据库位故障注入，不能称真实 UI 或 RunControl命令点击。
- [当前 O 桌面验收](../v4/offline/README.md)与[真实 Go/Chromium driver摘录](../v4/offline/real-go-driver-excerpt.log)证明独立18777临时库：真实来源版本变化→Go409→锁定旧来源、保留可复制pending文字；真实明确退出清设备私有数据库。来源更新与设备退出不同于删除来源后的模型费用竞态，且不是最终 V02同题 DB。其Node清理与synthetic26断言也分别标明范围，不冒充同题 browser stop/purge。

47工作计划 V02.2 的同题工程组合stop/purge缺口由新增真实router测试补齐；实际Owner stop/source purge入口、文字和完整numeric账本均已支持。正式浏览器10导航与草稿观察、独立同题工程故障副本、C09迟到付费竞态、O浏览器来源变化/设备清理分别记录适用范围，不能互相冒充。新增目标race通过后测试与业务源码冻结，最终全量技术门禁由主任务另登记。
