# 第四轮集成验证记录

日期：2026-10-02。状态：执行中，不是最终完成报告。

## 当前验证

- 音频复用：Worker 按数据库 EvidenceAudio.RelPath 读取并验证路径、文件和哈希，保留旧幂等返回路径。`go test ./internal/queue -run 'TestEvidence(Audio_|Reuse)' -race -count=1 -timeout 10m` 通过（18.860s）。新回归验证自定义恢复路径、正文哈希变化及 symlink 逃逸拒绝。
- `make lint`：通过；`go vet ./...`：通过；`go build ./cmd/cloudwisepod`：通过。
- 当前 `internal/server` 下 42 个 JavaScript 文件逐个 `node --check`：通过。
- `git diff --check`：通过。

## 未闭合门禁

全库 coverage profile `/tmp/cwp-v4-coverage-current.out` 对应较早的并行候选树，因音频复用返回 canonical 路径使旧幂等测试失败；该原因现已修复并通过定向 race，但必须重新运行最终全库检查。

该轮测得 queue 94.9%，server 78.8%，尚未满足原 95%／server 79.4% 地板；store 79.1% 满足原 78.4% 地板。其他包达到各自门槛（models 原豁免不变）。这些数字是定位补强的中间证据，不是最终覆盖率交付。

待收口：综合 v2 备份恢复、同问题完整跨模块学习旅程、最终 race/coverage 门禁、稳定低负载检索性能、逐卡证据和提交登记。真实个人笔记、人工质量评分、实体手机结果仍须独立记录，不能由自建语料或桌面浏览器替代。

## 综合恢复证据补充

`go test ./internal/backup -run TestPersonalLearningV4RealRestore -race -count=1` 当前候选树通过（12.056s）。测试真实生成 v2 archive 并 Restore 到独立实例，核对理解版本/current/冻结引用、文章用途计划、质量案例版本、缺口历史、两个区间独立进度、反思与问答采用、暂停控制与付费账本。已知付费响应通过实际 Worker 续跑，外部供应商与原音获取替身均设为 Fatal 且未触发；未知结果保留 NULL 费用。另一独立恢复实例将声明音频改为外部 symlink，Worker 拒绝复用、保留付费断点且不新建转录。证据来自实际测试文件 `internal/backup/personal_learning_v4_restore_test.go`；其范围不替代完整浏览器旅程或最终全库门禁。

## 独立审计暴露的缺口（修复中）

- O04/O09：原脚本实测同 namespace/fileID 的第二次下载网络失败，会覆盖旧 ready Blob，旧 pack 仍标 ready；旧 readable 随后报 material_unavailable。需要临时区下载/身份核对及失败保旧测试，不能由不同文件 ID 用例代替。
- F03.3：无 JavaScript 的质量案例 SSR 表单没有 request_key，提交失败；成功和冲突也缺原生导航/保稿。需要 SSR 请求身份和内容协商分支。
- G/U：统一 Retrieve 返回理解候选，但 ConfirmEvidenceGapCandidate 尚无对应真实关联分支；页面显示可确认却拒绝。需要确切理解身份/权限/范围关联测试。

上述为独立源码与运行审计结果，已发送模块所有者；未因已有定向测试通过而标为完成。

## 后续当前树复验（优先于较早通过）

独立审计组合命令 `go test ./internal/store ./internal/server ./internal/backup -run 'TestEvidenceGap|TestLearningExcerpts|TestUnderstanding|TestPersonalLearningV4RealRestoreRelationships' -race -count=1 -timeout 5m` 的 store/server 通过，但 backup 在观点对比所需两个不同来源的依据验证处失败。较早 V01 通过不能覆盖此失败；模块所有者正在调查来源选材/顺序的不确定性，综合恢复状态暂保留未闭合。

O09.2 还发现 schema 不兼容的离线初始化仅锁定内容，没有旧 outbox 文字复制出口，需补真实不兼容授权与 pending 草稿重开用例。

U HTTP 新增测试当前 race 通过（16.912s），相对上一全库 profile 增加35条 server 覆盖语句；这不替代新全量覆盖门禁。

## 跨模块关联与验收补强

个人理解候选确认的失败进一步定位到问题关联 resolver 和数据库 kind CHECK；新增迁移 `0088_learning_question_understanding_links.sql` 正在验证历史关联保留及真实升级路径，不能仅修改旧迁移替代已升级实例的升级。

综合恢复比较用例已查明第二文档原来只有 Owner reflection，比较块又假设选材顺序。修复使用真实第二文档 source_note 和明确的两个不同来源证据；不降低观点对比验证。修复后目标 race count=3 已通过39.067s，追加0088真实理解关联恢复的最终版本仍在验证。

独立审计还要求补充 ZIP 实际阅读、无 SW 回退、DJ/睡眠播放及 purge 后理解历史提示的实际浏览器证据；已分配模块所有者。V02 同一问题两用途文章应通过真实修订/成文流程，不使用两个不同问题替代。

## 最新目标验证收口

- V01：导出/恢复所有者实际执行 `go test ./internal/backup -run TestPersonalLearningV4RealRestoreRelationships -race -count=3`，最终 session56476 终态 PASS（65.736s）。现行测试核对真实第二来源、逐阶段生产结果校验、0088理解关联及全部旧关联逐字段恢复；这是所有者执行的当前目标证据，根代理最终全库 gate 尚未运行。
- U03.3：新独立浏览器 fixture 实际 purge 后，Owner答案仍可读、来源正文消失、历史警告显示，DOM三项均为true；harness PASS61.58s。清理前后截图已保存至本目录 `understanding-purge/`，不是仅临时路径。
- G04.3：所有者实际播放自建 DJ WAV并在同一 Audio 上切整集/两个摘录/DJ；独立进度、加速睡眠到期优先停止、区间循环与刷新暂停恢复均有浏览器记录，详见 `docs/acceptance/2026-10-02-gaps-browser.md`。不代表实体手机后台睡眠验收。
- V02新红：理解 SSR 旧head409返回页没有Owner草稿，U正在修复，不能仅凭状态码409判通过。

## 真实配置与新增预检结果

2026-10-02 只读检查 `.env` 字段存在性，未打印或修改值：POD base URL/key/model 已配置，独立 embedding 的 base URL/key/model/dimensions 均未配置。真实文本入口不能据此标“缺配置”；正在独立自建资料进行显式有界真实验收。质量 CLI 预检发现已配置 monthly budget 时尚无预算准入，正在修复；默认未配置预算可保留 unknown，不伪造价格。

大规模语义基准暴露文档更新性能缺口：按 doc_key 删除向量缺少首列索引，EXPLAIN 显示全表扫描。新增0089索引及真实更新/撤权回归将修复此原因；冻结旧二进制四案例仍完整记录，不把它和新源码结果混为一份准入报告。

E04.3/E05.3实际导出、持续播放及中文阅读证据已持久保存到 `export-browser/`，没有将临时ZIP或用户数据纳入源码。V02最终同题浏览器旅程和全库门禁仍待闭合。

## 全库门禁进展

`make cover-gate` 当前全库通过，日志持久保存 `gates/cover-gate.log`。原95%规则、store78.4/server79.4地板及models既有豁免均未改变；当前queue95.0%、server79.8%、store79.2%、CLI96.5%、evalset97.5%。`make lint` 通过，日志为 `gates/lint.log`。

首轮全库 `make test` 已终态失败：Store完整race通过550.087s，但Server原生质量表单测试依赖列表顺序，混用了caseID/version/requestUUID。修复后同一测试普通count20及racecount10通过，生产排序/CAS未放松；新的完整 `make test` 已启动，旧失败不当最终PASS。

真实CLI写作调用返回1428/983 units，响应模型与冻结模型不一致，严格拒绝；review没有发出，费用NULL。相同私有运行目录重放为缓存失败、零新HTTP和零重复usage。实际预算/dispatch锁/账本恢复缺口已修复并有定向race，真实结果不是人工质量通过。

## 最终候选复验（2026-10-02）

前一轮完整 `make test` 终态通过：Server177.238s、Store532.146s。随后补充旧库升级的历史文章/队列断言，并修复实际Theme下载404/409导航丢失根播放器的问题，因此重新执行最终全库检查，前一轮不是最后候选的最终证据。

现行 `make cover-gate`、`make lint`、`go vet ./...`、`go build ./cmd/cloudwisepod`、全部静态JS的 `node --check` 及 `git diff --check` 均终态通过；coverage/lint原始日志更新到 `gates/`。门槛及原豁免不变。最终 `make test` session4567仍运行，不能提前填PASS。候选775个源码/夹具文件指纹为 `gates/current-source-sha256.txt`（清单SHA256 813132429ea8e95b2249b04e8c6cd116ea530f4b5a70deef5028901fed8a2bef），最后提交前还须核对无相关变化。

V01旧0069升级夹具现实际包含旧文章/修订/审校输入、旧队列current/autoplay/position/日期、旧QA、known/unknown任务及账本/控制；任务真实绑定文章。升级和独立备份恢复均逐字段保持，目标race9.410s通过。此历史opaque checkpoint不冒称真实付费恢复，后者由正式v2 Worker guard组合测试证明。

E最后独立Theme浏览器131.05s PASS：双真实来源标题选择、预览/确认/ZIP下载，5个manifest文件hash核对；实际删除ZIP404及归档来源409均保留同URL、未保存中文草稿/选项及持续播放（10.11→85.93秒，同Audio）。0问答/0文章模型调用；相关race11.876s通过。正式报告、hash JSON及已查看截图在 `export-theme-browser/`，未保存ZIP或私有DB到源码。

最终候选全量 `make test` session4567 已终态 EXIT0：Server173.930s、Store520.228s，其余包通过（适用Go缓存的包明确显示cached），原始日志为 `gates/race.log`。此候选的所有技术门禁均已通过。141审计随后明确V02.2尚缺同旅程副本的业务stop/purge组合实证：关闭harness不代表停止业务；正在新增必要独立组合测试，完成后重跑受影响及全量缓存门禁，再冻结最终输入。故此处仍不宣称47卡交付。

O正式资料已落 `offline/README.md`：24Node完整日志，26原生Chromium工具终态观察JSON、真实Go154.44s工具终态观察及磁盘driver日志、DB/CDP脱敏摘要、已查看截图和SHA清单。原未保存完整stdout不补造。V04当前四文档32本地链接/anchors零错误及源码契约终校，完整可重现脚本/输出在 `documentation-check.md`。

## 最后冻结输入与门禁终态

V02.2补测 `TestPersonalLearningV4SameFixtureStopAndPurge` 实际目标race10.652s通过；先完成同题全旅程，再使用真实Owner stop/purge路由。8条usage逐列不变，模型2/6不增，Owner理解/离线整理保留而源绑定privacy正文与派生内容清除，旧ZIP/离线材料和pending同步拒用。正式报告和原race日志在 `../v02/`，不是把独立故障副本冒充最后浏览器DB。

最终冻结776个源码/夹具文件清单SHA256 **e65235091bf2adcacef751daa654b3def3d0a7e1343d2dee31c4d1d4b92aff32**；重核零变更。对应最终 `make test` session46720终态EXIT0，原日志 `gates/race.log`；`make cover-gate` session64085 EXIT0；`make lint` session5011 EXIT0；`go vet ./...`、`go build ./cmd/cloudwisepod`、所有静态JS `node --check`、`git diff --check` EXIT0。Go缓存输出按原日志保留，未称全部重新计算：Store源/测试未变，上一轮520.228s的相同输入race通过，本轮复用缓存；Server包含最后新增测试并重新执行。原95%规则、store78.4/server79.4地板及models豁免不变。

V04终校与141条审计都已核对持久证据。尚待一致提交及主清单登记；真实笔记/人评/手机待验，语义cold失败保持关闭，真实文本write模型合同失败而未发review。未部署。
