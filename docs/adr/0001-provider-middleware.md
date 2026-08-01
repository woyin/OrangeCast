# ADR-0001：Provider 中间层与 Groq 主力选型

日期：2026-07-31
状态：已被 [ADR-0009](0009-explicit-per-attempt-paid-provider.md) 取代

## 背景
CloudWisePod 部署在 VPS（Go 单二进制），目标是零 AI 成本复刻 Podwise。需要选择 AI 供应商并设计可切换的抽象层。

## 决策

### 1. Groq 为主力，OpenAI 为兜底
- **转录**：Groq `whisper-large-v3`（免费，配额对轻量自用足够：8h/天、2h/小时吞吐）。
- **分析/QA**：Groq `llama-3.3-70b-versatile`（免费，TPM 12K）。
- **OpenAI 兜底**：通过中间层随时切换，但默认不开通（零成本约束）。

### 2. 中间层 = 全局 `active_provider` 切换（方案 A）
- 单字段 `settings.active_provider ∈ {groq, openai}` 统管三种操作。
- **运行时实时生效**：worker 从队列取任务时读 settings，非入队时写死。
- 抽象边界为接口层各自实现（见下），接受两 provider HTTP 契约不对称。

### 3. 抽象边界：接口层各自实现
`TranscriptionProvider` / `AnalysisProvider` / `QAProvider` 三个 Go interface，Groq 和 OpenAI 各自实现。两 provider 的 HTTP 代码不对称（Groq `/chat/completions` vs OpenAI `/responses`），不强行抹平——抹平需把已验证的 OpenAI `/responses` strict 降级，不值。

## 权衡
- **未选按操作类型切换（方案 B）**：复杂度更高，自用阶段全局切换足够；数据结构从 1 字段升 3 字段是平滑迁移路径。
- **未选统一到 chat completions**：会破坏 OpenAI 的 json_schema strict 语义，赌一个已可用实现不值。

## 相关
- Groq 真实约束见 [ADR-0002](0002-groq-real-constraints.md)。
