# ADR-0009：付费 Provider 按单次任务显式授权

Groq 是所有 AI 操作的默认零成本 Provider；使用 OpenAI 或其他付费 Provider 时，Owner 必须针对某次 ProcessingJob 尝试明确授权，选择随该次尝试持久记录，系统不得全局切换或在失败后静默付费降级。该决策放弃无感自动兜底，以换取成本可预测性；免费处理失败或质量不合格时，由 Owner 决定是否发起一次付费重试。
