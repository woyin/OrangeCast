# ADR-0017：KeyPoint 全局索引与 Owner 标注体系

状态：已确认  
日期：2026-08-05

## 背景
Owner 需要一个全局视图，把所有 Source 的关键要点（KeyPoint）放在同一处，按生成时间排列，带出处（Source + 时间段），且可搜索。在此基础上，Owner 希望能对 KeyPoint 对应的证据做标注、收藏、跨 Source 组织——这要求 KeyPoint 从"卡片附属只读产物"升级为"Owner 可操作的实体"。

## 决策

### 1. KeyPoint 物化索引表（只读投影）
新建 `keypoint_index` 表，将每个 Source 当前卡片版本里的 KeyPoints 拆解为独立行，包含 content/description/citations_json/time_start/time_end/source_title/card_version/created_at。FTS5 `keypoint_search` 覆盖全文搜索。

真理来源仍是 `artifact_versions.payload`（不可变 ArtifactVersion）。索引表是投影——worker 生成卡片时（`doAnalyze`）和版本切换时（`SetCurrentVersion`）同步刷新（先删后插，和 `search_index` 模式一致）。

### 2. 标注/收藏锚定在 Citation（Segment 引用），不锚定 KeyPoint 文字
Annotation（标注）、Pin（收藏）、Collection（集合）锚定在 `(source_type, source_id, segment_ids)` 上——即"某 Source 的一组 Segment 引用"。不锚定 KeyPoint 的文字描述。

理由（ADR-0008 Evidence-first）：KeyPoint 是 AI 生成的文字，会随重新分析而变；Segment 是证据，在同一 Transcript 版本内稳定。Owner 真正想标注的是"这段证据有价值"，不是"AI 写的这句话有价值"。重新分析后 KeyPoint 文字变了，但标注锚定的 Segment 不变，标注不丢。

### 3. 不建独立 Citation 实体表
Annotation/Pin/Collection 直接存 Segment 引用（segment_ids JSON + time_start/time_end）。Citation 是逻辑概念（"一组 Segment 的关系"），不是持久化实体。避免实体膨胀。

### 4. Purge 级联删除
Owner Purge Source 时（ADR-0012），级联删除该 Source 的所有 Annotation/Pin/Collection items。证据彻底删除后标注引用的证据没了，保留指向虚空的标注违背可核验契约。Purge 确认 UI 显示标注/收藏数量提醒后果。

### 5. 领域词汇
CONTEXT.md 新增：Annotation（标注）、Pin（收藏）、Collection（集合）。不新增"KeyPoint Index"——它是实现投影，不是领域概念。

## 取舍
- 标注锚定 Segment 而非 KeyPoint 文字：牺牲了"标注和 AI 描述的直接关联"（重新分析后 KeyPoint 文字可能变），换取标注的持久性和证据一致性。
- 不建 Citation 实体表：牺牲了"Citation 作为一等公民"的正规性，换取 schema 简洁。
- Purge 级联删除：牺牲了"保留 Owner 个人文字"，换取证据契约一致性（孤儿标注无法核验）。
## 后果
- KeyPoint 全局视图（/keypoints）按时间排列 + FTS5 搜索 + 分页。
- Owner 可对任意 Citation 加 Annotation（个人标注）、Pin（收藏）、加入 Collection（跨 Source 主题集合）。
- 重新分析不丢标注；重新转录（Transcript 版本变）时 Segment ID 变化，标注自然失效。
- Purge 级联删除标注/收藏/集合成员。
