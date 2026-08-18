# Code Context

## Files Retrieved
1. `internal/store/migrations/0024_learning_creation_workspaces.sql` (lines 1-207) - 0024 新增 15 个工作空间表，并给 `keypoint_index` 增加质量/陈旧字段。
2. `internal/models/creation_workspace.go` (lines 1-91) - 新领域模型及质量状态枚举；多数模型尚无仓储实现。
3. `internal/store/creation_workspace.go` (lines 11-108) - 已实现 `MaterialCandidate` CRUD/决定状态及 `MaterialChange` 幂等写入。
4. `internal/store/proposal_batches.go` (lines 13-101) - 已实现 `ProposalBatch` 创建、查询、积压门控和状态更新。
5. `internal/store/editorial_relevance.go` (lines 14-99) - 已实现默认画像、KeyPoint 相关性/Owner override 与资格判断。
6. `internal/store/keypoints.go` (lines 16-34, 102-110, 243-345) - 现有 KeyPoint 投影、读取和 Scout/UI 可复用的筛选扩展点；未接入 0024 质量字段。
7. `internal/server/routes.go` (lines 42-105) - 全部受保护 HTTP 路由注册点；没有 0024 的 material/quality/batch/relevance 路由。
8. `internal/server/editorial.go` (lines 35-113) and `internal/server/templates/workbench.html` (lines 1-62) - 现有创作工作台仍读取旧 `ArticleProposal`/`ArticleBrief`/`ArticleDraft`。
9. `internal/server/themes.go` (lines 51-150, 250-317) and `internal/server/templates/themes.html` (lines 1-11) - Theme/显式 Scout 的当前素材选择、展示和 POST 扩展点。
10. `internal/server/templates/keypoints.html` (lines 1-53) - KeyPoint inbox 现有质量/状态控件的 UI 扩展点；目前只有 `production_status`。
11. `internal/backup/backup.go` (lines 168-330) and `cmd/cloudwisepod/main.go` (lines 78-146) - DB 一致性快照归档、恢复及 CLI 接入点。
12. `internal/backup/backup_test.go` (lines 32-135) - 端到端备份/恢复 fixture；当前迁移版本断言未更新。

## Key Code

### 当前可调用的新 Store API
- `Create/Get/ListMaterialCandidate`、`SetMaterialCandidateStatus`：`internal/store/creation_workspace.go:21-88`。
- `RecordMaterialChange`：`internal/store/creation_workspace.go:91-108`。
- `Create/GetProposalBatchByIdempotencyKey`、`HasOpenProposalBatch`、`SetProposalBatchStatus`：`internal/store/proposal_batches.go:22-101`。
- `Set/GetEditorialRelevance`、`IsKeyPointEligibleForProfile`：`internal/store/editorial_relevance.go:42-99`。

0024 的其余表（`owner_notes`、`rights_constraints`、`discovery_settings`、`creation_proposals`、`creation_history`、`ideation_sessions`、`material_diagnoses`、`research_needs/plans`、`creation_briefs`、`claim_maps/reviews`）只有 migration/model，当前没有 Store CRUD，也没有 HTTP/UI 调用点。

### HTTP/UI 接线建议（按最小垂直切片）
1. **学习质量闸门（应先做）**：在 `internal/server/routes.go:83-88` 附近增加 material/KeyPoint quality 的页面/API；handler 可仿照 `handleThemeKeyPoint`（`internal/server/themes.go:294-317`）处理 POST+CSRF。来源详情模板（`internal/server/templates/document_detail.html:7-8`、`source_detail.html`）适合显示/创建 source-scoped `MaterialCandidate`；`keypoints.html:17-53` 适合展示 `quality_status` 和批量 Owner 决策。
2. **Theme/Scout 资格接线**：`handleThemes` 在 `themes.go:119-133` 从 `ListKeyPointsFiltered` 构造素材选项，`scoutRequestWithOptions` 在 `themes.go:250-287` 收集 Scout 材料；两处均应调用/SQL 筛选 quality 和 `IsKeyPointEligibleForProfile`，并在 `themes.html:7-10` 显示排除原因、相关性或 Owner override。否则 0024 写入没有行为效果。
3. **发现批次接线**：以 `handleScoutRun`（`themes.go:142-156`）及其 `runScoutWithOptions` 调用链为入口：运行前用 `HasOpenProposalBatch` 做 backpressure，素材快照后 `CreateProposalBatch`，并将完成/失败写入 `SetProposalBatchStatus`。`handleWorkbench`（`editorial.go:47-108`）应加载 batches 和新 `CreationProposal`，而非仅旧 `ArticleProposal`；模板 `workbench.html:3-4` 已明确标注“ProposalBatch 尚未启用”，是替换入口。
4. **新创作对象接线**：为 `CreationProposal → Ideation/Diagnosis → ResearchNeed/Plan → CreationBrief → ClaimMap/ClaimReview` 先补 Store CRUD，再在 `routes.go:51-65` 的现有 `/workbench/*` 族添加 handlers，逐步替换旧 proposal/brief/draft 的 data map 和表单 action。现有 `workbenchBriefView`（`editorial.go:41-45`）和 stage 模板（`workbench.html:37-54`）可直接作为新视图模型/三阶段 UI 骨架。

### Backup / restore
- **无需为 0024 新表单独打包**：`backup.Create` 调用 `store.ConsistencyBackup` 生成整个 SQLite 快照（`backup.go:179-192`），所有 0024 表和数据自动包含；`Restore` 将经 hash 校验的 DB 原样安装（`backup.go:242-330`）。CLI 已通过 `backupCore`/`restoreCore` 接上（`cmd/cloudwisepod/main.go:80-95,141-146`）。
- **要补的验证**：扩展 `buildFixture` 和端到端测试，以 0024 的代表性行（至少 material candidate/change、relevance、proposal batch）做 create→backup→restore→查询断言；同时将 `backup_test.go:123` 的最新版本从 23 改为 24。

## Review Findings

1. **high — `quality_status` 是死字段，质量闸门实际被绕过。** migration 在 `internal/store/migrations/0024_learning_creation_workspaces.sql:4-6` 将所有 KeyPoint 默认设为 `needs_review`，但 `KeyPointRow` 没有该字段（`internal/store/keypoints.go:16-34`），所有 INSERT/SELECT/Scan 也没有读写它（`keypoints.go:102-110,243-345`）。Theme 的候选选择无质量筛选（`themes.go:119-129`），Scout 也不检查质量（`themes.go:250-287`）。应先把字段加到 store view/filter、写入明确默认/转换规则，并只允许规定的 ready/owner-confirmed 状态进入发现。
2. **high — `MaterialCandidate` 可形成不存在 Source 的孤儿记录，Citation JSON 未校验。** `CreateMaterialCandidate` 只校验 source type、origin/content，未检查空 `SourceID`、`sourceExists` 或 citation 是否可解析/属于该 Source（`creation_workspace.go:21-37`）；表本身也没有 source FK（migration `:8-20`）。这会使 source detail/UI 或后续提升流程无法可靠回读证据。应复用 `prepareManualKeyPoint` 的 source/citation 校验路径（`keypoints.go:345-384`）或加等价校验。
3. **high — 0024 大部分模型/表尚不可达。** `models/creation_workspace.go` 声明完整 creation 领域，但 Store 只实现 material candidates/changes；migration 中的其他实体无 CRUD，`routes.go:42-105` 也没有其路由。因此不能仅把新模型“接 UI”：必须先补仓储契约和测试。
4. **medium — `CreationProposal.ProposalBatchID`、`ResearchNeed.ResolutionSourceID/ResolvedAt`、`CreationBrief.ConfirmedAt` 等模型字段是 `string`，对应 migration 可 NULL（migration `:100-101,153-155,179`）。** 若后续直接 Scan 会因 NULL 失败，或无法区分 NULL 与空串；应使用指针/`sql.NullString`，或在每条 SELECT 明确 `COALESCE` 并定义空串语义。
5. **medium — backup 回归测试当前失败。** `internal/backup/backup_test.go:123-124` 仍断言 migration 23，但真实快照为 24。实测 `go test ./internal/store ./internal/backup`：store 通过；backup 仅 `TestBackupRestore_EndToEnd` 因 `version=24` 失败。

## Architecture

`Store.Open` 嵌入并顺序执行 migration，0024 已会自动落库。服务器将所有受保护页面/API 集中于 `routes.go`；当前学习到创作路径是 `KeyPoint → Theme → explicit Scout → old ArticleProposal/Brief/Draft`。0024 添加了平行的新数据模型，但只有 material、batch、relevance 三小部分仓储；没有把其状态反馈到 KeyPoint、Theme、Scout 或 Workbench。备份是数据库级快照而非按实体序列化，因而数据保留天然覆盖 0024，测试覆盖尚未跟上。

## Start Here

先打开 `internal/store/keypoints.go`：先把 0024 的 `quality_status/stale_*` 纳入 `KeyPointRow`、查询/筛选和更新 API；这是防止未审核素材进入现有 Theme/Scout 的最小且阻塞性的接线点。之后再改 `internal/server/themes.go` 和 `keypoints.html`。

```acceptance-report
{
  "criteriaSatisfied": [
    {
      "id": "criterion-1",
      "status": "satisfied",
      "evidence": "已给出带路径、行范围和 high/medium 严重级别的 HTTP/UI、backup 与迁移审查发现。"
    }
  ],
  "changedFiles": [
    "context.md"
  ],
  "testsAddedOrUpdated": [],
  "commandsRun": [
    {
      "command": "go test ./internal/store ./internal/backup",
      "result": "failed",
      "summary": "internal/store 通过；internal/backup 的 TestBackupRestore_EndToEnd 因仍期望 migration version 23、实际为 24 而失败。"
    }
  ],
  "validationOutput": [
    "0024 migration 已被 Migrate 测试列为版本 24；backup fixture 的端到端版本断言仍为 23。"
  ],
  "residualRisks": [
    "未审核的 KeyPoint 目前仍可进入 Theme/Scout。",
    "MaterialCandidate 可引用不存在的 Source 或无效 citation。",
    "大多数 0024 表尚无 Store/HTTP/UI 实现。",
    "备份未验证 0024 代表性数据的恢复。"
  ],
  "noStagedFiles": true,
  "diffSummary": "只读检查；仅按任务要求写入 context.md，未改动产品代码。",
  "reviewFindings": [
    "high: internal/store/keypoints.go:16-345 - 0024 quality_status/stale 字段未被模型、查询、筛选或 UI/Scout 使用，质量闸门被绕过。",
    "high: internal/store/creation_workspace.go:21-37 - MaterialCandidate 不验证 source_id/Source 存在性和 citations，且 migration 无 Source FK，可产生孤儿记录。",
    "high: internal/store/migrations/0024_learning_creation_workspaces.sql:35-207 - 多数新表仅有 schema/model，尚无 Store CRUD 或 HTTP/UI 路由。",
    "medium: internal/backup/backup_test.go:123 - 断言 migration 23，0024 后实际为 24，测试失败。"
  ],
  "manualNotes": "工作区原本已有其他未提交改动；本次未修改任何产品文件。"
}
```