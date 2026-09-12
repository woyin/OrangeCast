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
