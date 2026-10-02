# 四用途与可选写作计划验收证据

日期：2026-10-02。范围：剩余工作计划 W01–W05；跨模块门槛由主验收统一判断。本记录区分实现、自动化回归和实际浏览器观察，不代表真实 AI 文章质量评分。

## 实现与契约

| 项目 | 已实现行为 |
| --- | --- |
| W01 | synthesis / explanation / comparison / practice 四用途及独立规则指纹；新协议 knowledge-article-v5。旧 v1–v4 请求、审校 hash 和已知 checkpoint 保持原协议。 |
| W02 | 新文章及 discover/select/write/review/revise/final 冻结用途；真实序列化消息用于估价。用途变更生成新的工作修订，旧通过修订保留。Understanding 保持 Owner 理解身份及引用参考快照，不能升格为来源 Citation。 |
| W03 | 每种用途的结构和材料纪律在服务端审校；对比要求不同来源。非法结构不会成为 passed 修订；阶段 receipt/checkpoint 保留供恢复，恢复不重复调用已完成的模型阶段。 |
| W04 | 默认全自动；只有明确开启预览才在 selected plan 停留。迁移 0084 持久化不可变计划、父修订、请求 hash 与估价。编辑生成新计划；确认校验 CAS、用途、实时权限、预算和估价，并幂等返回同一任务。purge 包括计划、冻结请求、checkpoint 与嵌套理解参考，不删除数值 usage。 |
| W05 | 设置与生成表单、计划阅读/编辑/保存/确认、显式用途变更、旧修订阅读及反馈入口。未保存修改阻止确认，草稿保留；局部导航保留音频播放器 DOM。 |

核心文件：`internal/provider/writing_modes.go`、`internal/store/knowledge_writing_plans.go`、`internal/store/knowledge_writing_purposes.go`、`internal/queue/knowledge_article.go`、`internal/server/knowledge_articles.go`。测试 fixture 为 `fixtures/writing-purpose-v1.json`，质量判定保持 `not_evaluated`。

## 自动化证据

- provider/queue 用途定向 race：通过（1.577s / 13.448s）；四用途、六阶段、非法用途/同源对比/理解伪引用、cap 与成本溢出、checkpoint 恢复和 receipt 故障回归。
- 写作计划 race：通过（14.886s）；计划 CAS、父修订变更、权限与材料变化、预算不足/未知、JSON/hash 损坏、确认事务失败、幂等、purge 后数值 receipt 保留。
- server Writing/Generation race：通过（14.535s）；补充 Writing 组合 race：通过（15.427s），新增 6 个业务测试并补足旧覆盖文件中 27 个零覆盖语句。覆盖错误表单、无 POD、草稿保留、不入队、不改旧修订及 PurposeSection 防注入。
- Weekly/Daily/LearningReview 兼容回归：store/queue/server 通过（1.414s / 1.258s / 1.119s）。旧 weekly 协议不附加新用途字段；不是将旧 fixture 改成 v5 绕过契约。
- 最终 store Writing / KnowledgeArticle / Revision / Update / Discovery / Candidate / Understanding 定向 race：通过（39.763s），包含最新 purge 与 legacy recall 改动。
- `git diff --check`：通过。

定向覆盖率不作为整包门槛。共享树 provider/queue 一次全包覆盖为 94.5% / 93.5%，尚不能据此声称达到 95%；store/server 全包地板由主验收重新计算。

## 实际浏览器观察

使用 agent-browser 技能；独立临时库、端口 18089、自建笔记和确定性 provider，无用户默认库和真实外部模型调用。

1. 默认未开预览：实际表单提交后自动 discover/select/write/review，得到 ready v1。
2. 明确开启 explanation 预览：停在 awaiting_plan。修改大纲但未保存时确认被阻止且草稿保留；保存生成 plan v2；确认后自动成稿并通过，正文包含 question/concept/example/boundary。
3. 显式改为 practice：生成新工作修订，最终 ready v3，包含 scenario/steps/limits/checks；旧 v1 可继续阅读和下载。
4. 最新构建重新核验历史页：选中 v1 显示其冻结 synthesis，当前工作为 practice v3；v1 仍显示已通过、原正文和 revision=1 下载。独立 harness 正常停止，`TestWritingPurposeBrowserHarness` PASS（235.724s）。

观察过程中修复两处实际问题：计划成功保存/确认曾重定向到无效 revision=0；历史页浅拷贝请求指针导致当前用途污染选中旧版展示。两者均新增 HTTP 回归。前一长交互 harness 在功能路径结束后达到十分钟生命周期上限，其 run 不计为自动化通过。

截图采集：`/tmp/cwp-writing-default-ready.png`、`/tmp/cwp-writing-historical-rules.png`。浏览器确认的是音频 DOM 持续存在，不等于真实音频连续播放或实体移动设备验收。真实 provider 输出质量、人工评分及主流程的跨模块验收仍由对应验收项单独记录。
