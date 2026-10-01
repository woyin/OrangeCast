# KnowledgeCard EvalSet

代表性子样本集（中英文）与人工有用性评分记录。自动校验（schema、Citation 存在性、时间边界、逐字引用）在 `internal/evalset` 的测试中运行；本表记录人工对"卡片是否真正有用"的主观评分。

## 样本

| ID | 语言 | 主题 |
|---|---|---|
| zh-01 | 中文 | 主权财富基金的长期投资逻辑 |
| zh-02 | 中文 | 睡眠与记忆巩固 |
| zh-03 | 中文 | 播客创作的持续之道 |
| en-01 | 英文 | AI 与数据治理 |
| en-02 | 英文 | 复利的力量 |
| en-03 | 英文 | 远程团队的信任建立 |

## 人工有用性评分（1–5，待人工填写）

> 评分标准：1=毫无价值，3=摘要可用但细节欠缺，5=关键内容全部可核验、可回听、值得沉淀。

| ID | 评分 | 备注 |
|---|---|---|
| zh-01 | — | 待人工评测 |
| zh-02 | — | 待人工评测 |
| zh-03 | — | 待人工评测 |
| en-01 | — | 待人工评测 |
| en-02 | — | 待人工评测 |
| en-03 | — | 待人工评测 |

## 运行自动校验

```bash
go test ./internal/evalset/...
```

## ReferenceCheck EvalSet（ADR-0018 R3）

StudyChat 的信任根是 ReferenceCheck（主题锚定校验器）。它的误杀（合法被判不相关）与漏放（非法被判相关）作为 EvalSet 同等公民接受自动评测，定义见 `internal/evalset/reference_check.go`，6 个场景（3 放行 + 3 挡住）：

| ID | 期望 | 场景 |
|---|---|---|
| rc-pass-01 | 放行 | 类比例化：用买房例子说明参考片段中的通胀概念 |
| rc-pass-02 | 放行 | 解释/重述：用别的话重新讲量化宽松定义 |
| rc-pass-03 | 放行 | 结构重组：把线性步骤重组为闭环视角 |
| rc-block-01 | 挡住 | 主题漂移：回答主体是预测与投资建议，仅顺带提原文 |
| rc-block-02 | 挡住 | 脱离内容：评价主播，与参考片段无关 |
| rc-block-03 | 挡住 | 措辞蹭原文：回答主题是蜂鸟，与通胀无概念联系 |

自动评测入口：`evalset.CheckReferenceSamples(checker)`，返回不符项（空 = 全部正确）。校验器模型/prompt 变更时跑此集合；若虚挂率上升，第一道干预是切换校验模型或引入第二判据，而非调 prompt（ADR-0018 R3）。

## 学习与成文夹具（A04，2026-09-12）

`internal/evalset/learning_creation.go` 提供学习-DJ-创作评测的自建夹具，覆盖：中文长访谈（`zh-long-01`）、英文信息密集（`en-dense-01`）、多主题/重复/广告（`zh-multi-01`）、跨集矛盾观点（`zh-conflict-01` vs `zh-conflict-02`，用例 `cross-coffee-01`）、个人笔记身份（`note-li-01/02`）。

- 每个样本在代码内人工标注：核心观点（Claim）、支持区间（SupportSegments）、重要限定条件（Qualifiers）、不可推出的结论（NotImplied）；广告段与跨窗重复组单独标注。
- 库内只保存自建（`Source: "self-made"`）可提交片段。真实节目评测时本地运行，样本记录为 `external:<来源>#<本地快照标识>`，片段不入库。
- 自动校验入口：`evalset.CheckLearningFixtures()`（引用可追溯、结构完备、身份正确）；`evalset.LearningEpisodes()/CrossEpisodeCases()/OwnerNoteCases()` 供 K01/K05/G 组评测复用。
- 真实模型评分待实际生成后填写到 [learning-creation-baseline.md](acceptance/learning-creation-baseline.md)，不预先给通过结论。

## 学习与成文评测表（记录模型、提示版本与费用）

每项真实评测按样本 × 阶段（分析归并 / 高光 / 精读文 / 个人文章 / 解说）记录一行：模型、提示版本、输入/输出用量与费用、评测人。评分维度与门槛见 acceptance 文档。

## 第三轮个人听学评测（P0）

可重复入口：

```bash
CWP_LEARNING_EVAL_SAVE=1 go test ./internal/server -run '^TestPersonalLearningV3Evaluation$' -count=1 -v
```

默认使用自建3集材料、20条笔记、2个问题和模拟模型，只验证结构、队列、身份与记录流程，不代表文章有用性或真实接口已验收。每个问题/模型从同一份关闭数据库副本启动，历史互不污染，材料身份保持相同。

显式运行真实POD接口：

```bash
CWP_LEARNING_V3_LIVE=1 CWP_LEARNING_EVAL_SAVE=1 go test ./internal/server -run '^TestPersonalLearningV3Evaluation$' -count=1 -v
```

入口读取本地`.env`，调用走既有队列、来源策略与预算。可设置`CWP_LEARNING_V3_MANIFEST`指定私有JSON语料清单；设置`CWP_LEARNING_V3_COMPARE_MODELS`为不同模型名称的逗号列表，可比较同一份冻结语料。可选阶段模型和输出上限冻结在每个任务；比较时应检查这些实际阶段配置，不能将被覆盖的`POD_MODEL`当成写作模型。未设置`POD_REVIEW_MODEL`时审校回退到`POD_MODEL`，不能称作独立审校模型。

清单格式由`internal/evalset/personal_learning_report.go`定义：`version:1`、`kind:real|self-authored`，`episodes`含稳定ID/标题/来源标识/真实Segments/`model_data_policy`，`notes`含ID/episode_id/kind/content/segments，`cases`含ID/question/note_ids/expected/forbidden。来源策略默认`local_only`；只有明确允许外发的材料能通过付费准入。来源笔记的片段必须存在于所属节目；个人反思只能保存参考关系。

输出位于忽略目录`data/eval/personal-learning-v3/run-*`：私有`report.json`与包含精确任务输入、断点结果及用量的`frozen-run.db`。失败也保存已知记录；报告中的`human`默认缺省。人工评分必须有评测人和五个1–5分维度，均分≥4且无严重事实/归因错误才达到人工质量标准。真人评分、模型审校、真实接口和手机体验分别记录。

当前Owner确认尚无真实个人笔记，因此真实笔记质量与人工评分保持待验证。自建笔记不代填真实使用记录；后续积累笔记后可直接更换清单进行评测。

P2的阶段输入估算以实际消息序列为准，仍属于近似；校准只针对确切已测路由，其它模型保留保守办法。报告同时保存前置估算和实际receipt；不把价格未知写成零。新v4任务记录候选覆盖/证据窗口，旧v1–v3沿原契约恢复。阶段返回计费输出超限时保留响应和实际用量，并阻止自动推进，不能假设兼容端点一定遵守请求上限。

高命中投影性能可独立复测（无模型调用）：

```bash
go test ./internal/store -run '^$' -bench '^BenchmarkKnowledgeWideRecall$' -benchtime=20x -count=1
```

该基准为1万/5万合成投影、100%词项命中、最多200项元数据，包含来源外发策略过滤；不评估文章语义质量。实际运行见第三轮验收记录。

### 私人录音真实接口技术检查（P9）

下列入口默认跳过，显式启用后会向既有音频连接发送一段自建中文短音频。当前工具使用macOS `say`与ffmpeg合成样本；它验证真实ASR、草稿编辑、响应断点和用量契约，不代表Owner麦克风或手机验收。

```bash
CWP_VOICE_LIVE=1 \
CWP_VOICE_SETTINGS_DB=/absolute/path/to/data/cloudwisepod.db \
CWP_VOICE_LIVE_REPORT=/private/path/asr-report \
go test ./internal/server -run '^TestVoiceLiveAcceptance$' -count=1 -v -timeout 3m
```

该测试从指定既有数据库以`mode=ro`读取站内转录连接，在独立测试数据库执行，不修改默认库，也不复制密钥进入测试库。已有月预算时读取用量与预占，按剩余额度和确切音频价格准入；旧结构没有预算字段时明确记录该事实，不伪造已设预算。此入口验证站内配置回退；语音专用`.env`配置契约另由config/queue测试覆盖。

私有目录保存自建音频和脱敏报告，`human`保持缺省，实际价格未知继续标记未知。2026-10-01实测Groq `whisper-large-v3`技术通过：5.3289375秒、3个预期用语命中、Owner文字保留、1条receipt及响应断点。真实语音质量仍需不同设备、噪声和自然表达样本；当前结果只适用于这一次样本和连接。
