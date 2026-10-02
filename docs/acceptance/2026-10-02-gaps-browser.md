# G01–G06 本地浏览器验收（2026-10-02）

使用 agent-browser 隔离会话 cwp-gaps、生产 Router、动态 localhost 端口和临时 SQLite 数据库。夹具为自建中文转录、文档和实际可解码的 120 秒 WAV；worker 未启动，未读取真实个人笔记或调用外部模型。这是功能验收，不是个人学习质量评分，也不是实体手机验收。

## 实际浏览器结果

- 问题 → Owner 缺口 → 本地候选 → 明确确认 → 真实原音摘录队列完成。确认使问题范围 v1→v2，旧缺口显示已过期；保留查找词，可按当前本地索引只读再查，不沿旧 CAS 新增关系。
- 第一区间 20–45 秒实际播放，根 Audio duration=120，摘录模式；37.270921 秒捕获听后整理。切首页保持同一个 Audio 对象，真实播放时间继续前进，私人整理文字与绝对锚点不变，焦点落 MAIN。自然到 45 秒暂停，没有整集完成反思或自动关闭缺口。
- 两标签真实修改范围 v2→v3 后，旧表单实际收到 409：输入文字、expected_revision=2、根播放器及整理草稿保留。发现冲突焦点 BODY 后修复错误反馈聚焦；新编译夹具再以过期 CAS 提交验证 errorCode=conflict、焦点 gap-feedback、文字“最终409焦点验收草稿”保留。
- 第二区间 60–90 秒实际播放并独立保存至 80.436919 秒；切整集原音从 0 秒播放并独立保存至 0.469378 秒。两段摘录进度及原始整理锚点互不覆盖。
- 正常问题整理页、学习成果导出页保持同一根 Audio 与整理草稿；新编译夹具质量案例页软导航成功、Audio 对象保持、焦点 MAIN。
- 删除临时 WAV 后，旧缓存队列播放也被拒绝“没有可播放的音频”；新队列两项 unavailable，原因“原音文件缺失”，播放按钮禁用。
- 通过实际来源页彻底删除夹具来源：来源删除、缺口正文清除；processing_jobs/artifact_versions/evidence_gaps 变为 0。删除操作为正常全导航，不能作为普通学习页根 Audio 持续性证据。

## 零新增任务

读取、检索、确认、摘录入队、播放、切页、整理自动同步和缺失拒播之前与之后，processing_jobs=1、artifact_versions=1、dj_plans=0、usage_records=0；1 条转写任务和 artifact 是预先自建夹具。整个过程中没有新增任务或费用记录。来源清除后来删除了夹具任务和 artifact；usage_records 和 dj_plans 仍为 0。

## 自动测试

本次最终修改后：`go test ./internal/store ./internal/server -run 'TestEvidenceGap|TestRender_AllPages|TestAppNavigation' -race -count=1`，Store 12.895s、Server 11.824s 通过。

此前同轮摘录代理验证：摘录/队列/进度 race 22.354s；真实 v2 备份恢复 race 12.676s；已有三组 v2/排队恢复/语音测试 35.096s。备份包含真实 WAV SHA、两个摘录、整集与 DJ 四类进度、缺口历史及既有 37 分费用记录。此处备份证据来自同轮代理，浏览器链路由本代理实际执行。

## 截图

截图目录：[2026-10-02-gaps-browser](2026-10-02-gaps-browser/)。02 为确认后候选；03 为实际摘录播放；04 为跨页整理草稿；05 为真实双标签冲突保留；06 为双摘录；07 为原音缺失拒播；08 为最终冲突反馈焦点。01 是修复前初步截图，不作为最终通过证据。

## 实用限制

自建静音 WAV 验证了媒体解码、范围和身份，未评价内容可听性。没有真实个人笔记，不能替代人工质量评分；实体手机触摸、后台音频和系统中断仍需实体设备验证。本浏览器链路未实际播放 DJ，DJ/原音/摘录组合与备份由自动测试覆盖。此报告不宣称这些外部验收已完成。


## G04.3 补验：真实 DJ、睡眠及循环（同日）

另启生产 Router 隔离夹具，预先写入自建 DJ 清单（两项真实原音，20–45 / 60–90 秒，原音为同一实际 120 秒 WAV；不生成解说）。点击“自动播放全集”实际 mode=dj、planVersion=1、媒体开始 20 秒且未暂停（截图09）。切第一区间、第二区间、整集原音、再回 DJ，全程同一根 Audio 对象；实际 SQLite 有四份独立进度：DJ / original / 两份 excerpt，互不覆盖。

实际 UI 设循环 20–21 秒，在真实播放器控制器设置 0.04 分钟（2.4 秒）的加速睡眠定时，未改时间源或伪造媒体事件。自然定时到期后 paused=true、sleepExpired=true，循环设置仍在，位置20.213002秒，停止优先于循环（截图10）。随后关闭睡眠，实际 UI 将循环改成20–45秒、通过真实seek到44.8秒，实际媒体越过边界返回20秒并继续播放；采样26.540233秒、paused=false。明确关闭循环后可切第二摘录，再切整集和DJ。最后暂停第一区间并真实刷新页面，恢复同一 excerpt ID、20.090486秒、paused=true（截图11）。

此补验的任务基线与结束均 processing_jobs=1、artifact_versions=1、dj_plans=1、usage_records=0、excerpts=2。DJ是预先自建持久清单，不是新AI任务。前文“浏览器未实际DJ”的限制由本补验更新；真实设备后台15分钟睡眠仍未验。


## 审计后 G/U 及语义集成修复

缺口候选 kind=understanding 现在按真实理解 snapshot UUID 建立 typed 学习问题关系，Version 为真实不可变版本，SourceType/SourceID 留空。关系确认不更改理解 local_only 策略；外发检索仍拒绝。底层关系 resolver 和新迁移0088同步补齐，迁移完整保留旧关系行、来源清除/导出状态触发器及索引。真实87→88测试验证失败回滚、旧version/state/origin/created_at保留、理解删除与来源删除关系失效、foreign_key_check无异常。

G02页面提供FTS或“已授权语义索引（仅缓存，不调用AI）”及已有配置ID选择。服务端复核当前配置连接路由，复用Retrieve的准入和权限交集；索引关闭、缺准入或缺查询缓存明确回退FTS，不创建查询向量或任务。查找与确认/补听携带同一semantic/configID和实际retrieval_method；确认或补听重查后的method改变拒绝409。HTTP已验证缺配置安全降级、理解候选实际确认、method篡改拒绝；Store另用已持久化合成向量及明确标注的声明报告验证真实rrf候选与method一致确认、任务计数不增加。该合成夹具不能作为语义收益或真实准入质量证明。

最后集成race（含理解/问答回归）：Store63.456s、Server30.429s通过；新增理解关联/升级/缓存语义定向race17.356s通过；go vet Store/Server、node语法与diff检查通过。


## V02 点击无效诊断（实际共享18131夹具）

接手 personalv4 会话只读状态后，实际根toggle trusted点击将结束40秒的摘录重播至20.08338秒、paused=false。队列页存在根笔记和整理草稿，导致“立即播放”在y=1753.625，而视口高度577。相同 agent-browser `click '[data-queue-action="play"]'`返回Done，捕获真实trusted事件target=HTML、播放仍paused。仅改变scrollintoview，按钮进入y491.625，elementFromPoint核对真正按钮；同一点击实际target=play、userActivation=true、excerpt29.365752秒、paused=false（截图12）。此诊断支持工具未滚动到可见按钮，而非监听器/播放器产品缺陷；没有使用API播放替代这次UI验证，也没有改生产源码。真实刷新恢复暂停20.221557秒，用于清除只读事件探针；shared会话交还C09，不关闭其浏览器或夹具。
