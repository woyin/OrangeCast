# 2026-09 真实学习与创作旅程验收记录（V02）

状态：**执行中**（本文件随旅程推进回填；最终结论见文末）。
对应任务卡：V02（执行完整的真实学习与成文旅程）。前置：K05（已完成，见
[learning-creation-baseline.md](learning-creation-baseline.md)）、U03（已完成，见
[mobile-workspace.md](mobile-workspace.md)）、V01（已完成，`TestV01_FullRestore_NewRelationships`）。

## 环境与配置

- 日期：2026-09-13；版本：`bf8e81a`（含本旅程发现的三处生产缺陷修复，见下）。
- 实例：本地 `cloudwisepod serve`，端口 8090，scratch DATA_DIR（`/tmp/cwp-v02`），真实 Groq key（免费档）。
- Owner：注册后唯一认领（`/register` → 303 → `/dashboard`）；第二次注册被拒（既有测试覆盖）。
- 解说引擎：本机 Kokoro（kokoro-onnx 1.x + v1.0 权重，`scripts/kokoro` 包装，zf_xiaobei 中文音色）。
- 价格表（Owner 维护，分/百万 token）：whisper-large-v3 111/0、openai/gpt-oss-120b 15/75、qwen 20/80、compound 60/80；月预算 100000 分；自动任务日限额 40。

## 订阅与自动处理

| 播客 | 策略 | 关键词 | 处理深度 | 结果 |
|---|---|---|---|---|
| 博物志（中文长访谈，typlog） | filtered | 崇明 | knowledge_dj | 手动入队《与段志强谈崇明乡野博物志》转写成功（97 分钟，whisper-large-v3，证据音频 17.5MB 落盘） |
| Lex Fridman Podcast（英文） | filtered | Bernie Sanders | knowledge | 14:00 cron 自动发现《#450 Bernie Sanders Interview》（70 分钟）并入队转写成功；自动管线随后自动入队 analyze（intent `ingest:…`、快照冻结 `{"ingestion_policy":"filtered","processing_depth":"knowledge"}`） |

- 自动处理证据：新增订阅 → cron（*/30）发现新单集 → 自动入队（is_automated=1、intent_id、B05 策略快照冻结）→ B04 预算预占/释放/结算（见账单节）。
- 同批另一匹配单集（David Pakman）转写在下载阶段失败（CDN TLS handshake timeout）——网络瞬断；未重试（不在旅程范围内），B09 阶段重试机制由崇明集 analyze 重试实际验证。

## 本旅程发现并修复的生产缺陷

| # | 缺陷 | 修复 | 提交 |
|---|---|---|---|
| 1 | 默认分析模型 `llama-3.3-70b-versatile` 已从 Groq 下线（404 model_not_found）——所有新实例默认配置即坏 | 默认切换 `openai/gpt-oss-120b` + 设置页 placeholder 同步 | `d8ccb32` |
| 2 | 12000 字分窗预算超免费档 TPM（实测 429/413："TPM Limit 8000, Requested 10296"） | 分窗预算 5000 字、窗口间隔 60s（按实测 0.86 token/字校准） | `d8ccb32` |
| 3 | 归并 5 张候选卡被默认 completion 上限截断（json_validate_failed: "max completion tokens reached"） | chat 调用显式 `max_completion_tokens: 8192`（TPM 只按输入+实际输出计，实测 30000 上限不触发限流） | `d1fea49` |
| 4 | 免费档分钟级 TPM 429 时内置退避（0.25–2s）无效，窗口/归并整任务失败 | 窗口级 TPM 重试：429 等 65s 整限额窗口再试（≤3 次），非 429 不重试 | `bf8e81a` |
| 5 | 375px 视口导航横向溢出 + 设置页表格撑破页面（U03 发现） | 见 [mobile-workspace.md](mobile-workspace.md) | `1edc5b4` |

限额治理观察（免费档，记录为运行事实）：gpt-oss-120b TPM=8000/TPD=200000；单日旅程
（转录 2 集 + 多轮分析重试 + K05 评分）耗尽当日 TPD（196467/200000 @14:24，约 15:12 重置）。
分析任务对 TPD 的消耗随重试次数放大——修复 #4 后每次被 429 的窗口等待 65s 重试而非快速失败重入队，
重试不再放大消耗。

## 账单与预算（B03/B04 实际行为）

- 预算预占：analyze 任务 hold 3 分；失败（429/无远端产出）→ `released_no_call`；成功 → `settled` 按实际用量。
- 用量审计：usage_records 记录 transcription（groq/模型 unknown、费用 NULL=未知不计入）——转写计量单位与价格表（token 口径）不一致导致预估 0，属已知口径缺口，不阻塞（真实成本以 Groq 侧账单为准，免费档为 0）。

## 旅程执行

（进行中——TPD 重置后依次执行：崇明集分析 → 高光 → 精读文 → DJ 清单与真实解说 → 收藏/笔记 →
素材选择 → 构思 → Brief → 文章 → 审校 → 导出与历史登记。每步结果回填此处。）

## 限额、时长与未完成项

（旅程结束后汇总。）