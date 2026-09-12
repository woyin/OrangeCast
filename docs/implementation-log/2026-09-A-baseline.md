# A 组任务记录：基线与契约

## 任务：A01
状态：已验收（文档任务，本任务验收即文档检查）
依赖基线：37abe67（规划时 HEAD）+ 未提交工作区
实施范围：新增 ADR-0024（处理深度 opt-in、DJ 串场解说修订 ADR-0019、来源快照/精读修订/创作兼容连接契约）；product-goal.md 增加处理深度与自动化边界、学习 DJ 交付与计划链接；CONTEXT.md 调整 IngestionPolicy、新增 ProcessingDepth、Narration 增加受约束串场范围；implementation-roadmap.md 标注历史定位；连同已有未提交的 ADR-0023、方向建议研究与原子计划一并入库。
提交：8ff8845 docs(product): define learning DJ and creation delivery contracts
验证：链接相对路径全部存在（逐项核对）；git diff --check 通过；文档间条款无冲突（处理深度默认 knowledge、存量订阅不静默扩大、串场标注 AI 与 ADR-0019 分级原则一致）。不修改业务代码。
兼容与恢复：不涉及。
剩余事项：无。

## 任务：A02
状态：代码已提交待外部验收
依赖基线：8ff8845（A01）
实施范围：将工作区已有 EpisodeDigest 后端整理为可构建候选——models/digest.go、JobDigest 常量、provider/digest*.go（Writer/Search/Rewriter + 门禁）、ProviderBundle/Selector 接线、store/digests*.go + 0025 迁移、队列 dispatch 与 Purge 删除、editorial_costs 的 digest 免画像记账、server/digest.go 单集 handler（入队/草稿页/落源确认剔除/改写重试）、digest.html 草稿模板及 store/queue/server/backup 测试。A03 归属的列表/批量/Markdown handler 分支、路由与测试未纳入本候选。
提交：4aa0b08 feat(digest): establish episode digest backend baseline
验证：独立 worktree checkout 4aa0b08：go build 成功；go test ./... 全部包通过；go vet ./internal/... 通过；git diff --cached --check 通过。
兼容与恢复：0025 迁移保持文件内容不改动；migrate/backup 测试断言版本 25；备份恢复测试覆盖 digest 相关表。
剩余事项：digestSourceMaterial 统一读 Transcript、Document 素材路径缺失（G01）；重试渠道改写整篇重跑（G06）；来源剔除直接删块（G03）——均为原有已知问题，按计划登记到 G 组，不在基线整理中修复。

## 任务：A03
状态：代码已提交待外部验收
依赖基线：4aa0b08（A02）
实施范围：纳入已有列表页（/digests + digests.html）、批量入口（/api/digest/batch + podcast_detail 批量按钮与全选复选框）、文档详情页精读入口、dashboard 精读卡片、progress 页 episode_digest 人性化标签、digest 页 Markdown 下载与返回列表；归档旧 2026-08-25-episode-digest-closure.md 任务状态（4 任务全部实现，移除缺失的 subagent 插件指令，登记已知边界）；恢复 source_detail.html 中被工作区改动误删的"版本历史/AI DJ"入口（路由仍在，属基线整理而非新功能）。
提交：b680b90 feat(digest): expose existing list batch and markdown flows
验证：gofmt 清洁（digest_list_test.go、routes.go 对齐修复）；go test ./internal/server ./internal/store 通过；go vet ./internal/server 通过；git diff --check 与 --cached --check 通过。
兼容与恢复：不涉及 schema 变更。
剩余事项：浏览器端复制/下载人工验证未执行（无真实运行实例），属 G04/V02 真实验收范围；记录边界"入队不等于成文成功""下载尚缺引用跳转"已写入归档文档。

## 共同基线
A02/A03 后新的共同基线 SHA：b680b90。后续任务（A04、B01 起）以该 SHA 为依赖基线。
