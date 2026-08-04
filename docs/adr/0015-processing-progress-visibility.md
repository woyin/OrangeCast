# ADR-0015：处理进度的全局可见性

状态：已确认  
日期：2026-08-04

## 背景
Roadmap Phase 3 把 ProcessingJob 改为 SQLite 持久化、lease + 心跳 + 启动恢复，但进度只暴露在单个 Source 详情页的粗状态（transcribing/analyzing）。Owner 无法一眼看到"当前在处理哪些、排队到第几位"，只能逐个翻 Source 详情页。

## 决策
新增独立页面 `/progress`，导航栏加"进度"入口，展示两类信息：
1. **正在处理**（0 或 1 个 Source）：粗阶段（queued / transcribing / analyzing）+ 队列位置。
2. **排队中**（0~N 个 Source）：按入队顺序列出，显示 Source 标题与排队序号。

刷新方式：页面首屏服务端渲染，之后每 5 秒用原生 JS `fetch('/api/progress')` 更新 DOM，不引入框架。

暴露粒度：**粗状态**（Source 级别的 transcribing/analyzing），不暴露 sub-step（下载/转码/调 Groq）。理由：粗阶段已足够回答"处理到哪一步"；sub-step 持续时间差异大（下载几秒、Groq 转录几分钟），显示反而焦虑。schema 在 processing_jobs 预留 `current_step` TEXT 字段（nullable），将来要细化时 worker 写几行即可暴露，不破坏 schema。

## 取舍
- 选择粗状态而非 sub-step：牺牲了诊断精度（卡在哪一步要靠日志），换取日常观察的简洁。
- 选择独立页面而非 Dashboard 增强：给"看进度"一个明确入口，且轮询只作用于该页，不污染首页。
## 后果
- Owner 可在一处掌握全部处理动态。
- 读取负载可忽略（单 SQLite 查询，5 秒一次）。
- `current_step` 预留让未来细化成本极低。
