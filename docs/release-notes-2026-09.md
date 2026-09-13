# 2026-09 首轮发布说明：学习 DJ 与创作工作区

> 本文档汇总首轮 53 项原子计划的交付内容、提交对照、迁移序列、验收证据与已知限制。
> 实际部署或发布按后续明确指令执行；本文不自动部署。

## 交付概览

首轮 53 项任务完成 **50 项**（计划状态见
[`docs/superpowers/plans/2026-09-12-learning-dj-creation-atomic-plan.md`](superpowers/plans/2026-09-12-learning-dj-creation-atomic-plan.md)），
提交区间 `8ff8845..9b29d02`（2026-09-12 → 2026-09-13）。新增能力：

- **来源快照（B01）**：版本冻结的不可变阅读输入，digest/创作共用；可显式清除。
- **任务意图（B02）**：partial unique index CAS 幂等入队、输入快照继承、检查点续跑。
- **用量与预算（B03/B04）**：receipt 去重的不可变用量审计；月度/日预算 hold→settle→release。
- **处理深度（B05）**：knowledge vs knowledge_dj 按 Subscription 显式 opt-in。
- **关键点质量（K01–K03）**：单集洞察整合、接地性/有用性评分、重分析 reconcile 保留 Owner 决定。
- **Owner 笔记（K04）**：修订链 + 并发防护，源锚点与个人理解分离。
- **单集精读（G01–G08）**：冻结材料生成、检查点 + 原子发布、不可变修订链、Citation 冻结、
  交付就绪门、独立渠道改写、页面集成与创作历史登记。
- **DJ（D01–D08）**：确定性编排清单（时长目标 + 版本化）、真实 Kokoro TTS（D03）、
  开场/串场/收尾解说、媒体时间驱动的播放状态机（D05）、传输控件（D06）、
  听播进度持久化 + Media Session（D07）、收藏与个人理解笔记（D08）。
- **跨集创作（C01–C13）**：素材选择与资格校验、构思轮次（nonce 幂等）、接地诊断、
  研究缺口验证、提案决策、Brief 复核、旧文章体系桥接、Writer v2（typed claims）、
  持久写作任务、Claim 审校、ClaimMap 继承、发布导出与精确修订历史。
- **日常界面（U01/U02）**：首页学习/创作双行动区并列、双工作区交叉导航。
- **恢复验证（V01）**：覆盖全部新关系的备份→恢复全量验证。
- **TTS 预检（D03）**：`cloudwisepod tts-check` 预检 + 真实引擎短句试听。

## 任务组与提交对照

逐任务 SHA 见 `docs/implementation-log/`（A/B/C/D/G 五份记录）；组级锚点：

| 组 | 内容 | 锚点提交 |
|---|---|---|
| A | 契约与基线 | 8ff8845, 4aa0b08, b680b90, 839a7cb |
| B | 快照/意图/预算/深度/持久任务/自动 DJ/阶段重试 | 9d0a237…f5a39c6（9 项），a54ad63（日限额） |
| K | 洞察整合/质量评分/reconcile/笔记 | 9c22bbf, 1657772, 659efd6, 474a53c |
| G | 精读生成/修订/改写/就绪/集成 | 1b67121…d12884a（8 项），8aae842 |
| D | DJ 编排/TTS/解说/播放/进度/笔记 | 7cd1996, d890dc0, 7ccff43, 488a1e9, 2a78e8f, 78e0275, 03ed4bf |
| C | 构思→写作 v2→审校→发布 | c16c7ff…91d84f7（13 项） |
| U/V | 首页双区/工作区导航/备份全量验证/移动 CSS | c6a8a7a, 182b817, 8ea9432, 3f8a342 |

## 迁移序列

44 个增量迁移（`internal/store/migrations/`），启动时按版本顺序应用，
全部随对应实现同任务闭合，`schema_migrations` 记录已应用版本：

0001_baseline → 0002_remove_tenant_ownership → 0003_singleton_settings →
0004_evidence_audio_and_durable_jobs → 0005_immutable_artifact_versions →
0006_segment_level_search → 0007_highlight_and_progress →
0008_keypoint_index_and_annotations → 0009_per_task_provider_model →
0010_provider_keys_and_urls → 0011_relation_kind → 0012_paraphrases →
0013_study_sessions → 0014_narrations → 0015_fix_highlight_pointer →
0016_content_workbench_foundation → 0017_keypoint_inbox →
0018_ingestion_policy → 0019_themes → 0020_automated_ingestion_jobs →
0021_editorial_role_models → 0022_document_sources → 0023_editorial_integrity →
0024_learning_creation_workspaces → 0025_episode_digests → 0026_source_snapshots →
0027_job_intents → 0028_usage_receipts → 0029_owner_budget →
0030_processing_depth → 0031_narration_cache → 0032_keypoint_quality →
0033_owner_note_revisions → 0034_digest_revision_lineage →
0035_digest_rewrite_identity → 0036_digest_history_link → 0037_dj_plans →
0038_dj_script_text → 0039_listening_progress → 0040_creation_selection →
0041_ideation_rounds → 0042_diagnosis_rounds → 0043_creation_article_links →
0044_claim_maps

首轮新增为 0026–0044；备份/恢复（`cloudwisepod backup|restore`）经 V01 全量验证
（快照、进度、修订、质量判定、构思轮次、选择、ClaimMap、创作历史、预算预留逐表核对）。

## 验收证据

- **门禁**：`go vet` / `go build` / `gofmt` / `lint.sh`（注释门禁）全绿；
  `-race` 逐包全绿（本机实测：store 758s、server 588s、queue 309s，CI 已放宽
  `-timeout 25m`）；cover-gate：cmd 96.2%（D03 新增代码已补测）、rss/safehttp/server 达标；
  已知债务（对 V1 登记地板）：evalset 94.0、provider 86.7、queue 77.0 低于 95，
  store 75.2 低于其 78.4 登记地板（本轮大量新增 store 代码所致，恢复到 78.4 需补 store 层测试）。
- **备份恢复（V01）**：`TestV01_FullRestore_NewRelationships` 覆盖全部新关系，一致性备份→恢复→逐表核对通过。
- **DJ 播放状态机（D05）**：Node DOM 桩执行浏览器夹具 5 场景（区间媒体时间驱动、
  缓冲不消耗、暂停不跳段、停止无幽灵续播、快速切换互斥）5/5 通过。
- **真实 TTS（D03）**：kokoro-onnx 1.x + Kokoro-82M v1.0 权重（macOS arm64 CPU）：
  英文 af_heart 2.79s 音频/1.8s 墙钟；中文 zf_xiaobei 专名+数字+中英混合样本 7.08s/3.0s；
  经生产 Adapter `tts-check` 合成中文 12.0s（576,300 字节）、英文 4.68s（224,748 字节），
  ffprobe 均识别为合法 wav，RMS 0.06–0.08 无削波。
- **安装/配置**：干净实例按 `docs/production-deployment.md` 可完成配置
  （环境变量、反向代理、Docker、备份恢复、解说音轨安装与 `tts-check` 验证）。

## 已知限制与外部验收

以下项代码已就绪或不阻塞发布，但需真实条件验收，**不计入"已完成"**：

- **K05 真实 Provider 评分**：质量评估需真实 Groq key + 人工复核评分有效性。
- **U03 移动端人工核对**：响应式 CSS 已交付（3f8a342），触控目标/溢出/焦点需真机浏览器确认。
- **V02 真实双旅程**：学习与创作闭环需真实订阅 + 真实模型调用 + 真实浏览器执行。
- **D03 人耳试听**：引擎真实合成已验证；中文韵律走 espeak cmn（机器味），是否接受或
  换 v1.1-zh/misaki[zh] 路线由 Owner 听感决定。
- **X01–X04 后续扩展**：显式兴趣、跨集 DJ、版本化翻译、embedding 检索——未动工，不混入首轮。

## 配置与部署

- 环境变量全表：`README.md`「配置」、`docs/production-deployment.md`「环境变量」。
- 解说音轨安装（kokoro-onnx + 权重 + `scripts/kokoro` 包装）：`docs/production-deployment.md`「解说音轨」。
- 备份/恢复/跨机迁移：`README.md`「备份与恢复」、`scripts/export.sh`、`scripts/remote-setup.sh`。
- 示例环境变量：`.env.example`。
