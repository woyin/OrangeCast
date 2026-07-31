# ADR-0002：Groq 真实约束与应对（跑通后记录）

日期：2026-07-31
状态：已采纳

## 背景
端到端跑通真实 Groq 链路时发现三个硬约束，均非文档明确、需实测才知。记录以免重蹈。

## 约束与应对

### 1. RSS feed 大小 → 2MB 限制过小
**现象**：大型播客 feed（NPR Planet Money ~数 MB）解析报 `unexpected EOF`。
**根因**：`io.LimitReader` 限 2MB 截断了 XML。
**应对**：放宽到 16MB。播客 feed 含大量历史单集，几 MB 很常见。

### 2. Whisper 单次上传文件 ~25MB 上限 → 需转码
**现象**：转录 35MB mp3 报 `HTTP 413 Request Entity Too Large`。
**根因**：完整播客单集（30+ 分钟 / 128kbps）常 30-50MB，超 Groq Whisper 单次上传限制。
**应对**：worker 下载后用 ffmpeg 转码为 64kbps / 16kHz / 单声道，体积砍半，音质对语音转录足够（Whisper 内部本就用 16kHz）。Dockerfile 加 ffmpeg 依赖。

### 3. json_schema 仅 gpt-oss 支持 + gpt-oss TPM 仅 8K → 用 json_object
**现象**：
- `llama-3.3-70b` 用 `response_format=json_schema` 报 HTTP 400（不支持）。
- 换 `gpt-oss-120b`（支持 strict）后，整集 transcript 单次请求 8375 token 撞 8K TPM 限（结构性失败，重试无解）。
**应对**：
- 分析改用 `llama-3.3-70b-versatile` + `response_format=json_object`（所有模型支持，TPM 12K 宽裕），靠 prompt 约束字段 + `parseJSONLoose` 容错解析兜底。
- Q&A 用 RAG chunk 检索，只喂 top-5 chunk，单次请求 token 远低于 TPM 上限。

### 4. parseJSONLoose 必须忽略未知字段
**现象**：`json_object` 模式不强制 schema，LLM 偶尔输出额外字段。
**应对**：`parseJSONLoose` 不用 `DisallowUnknownFields`，默认忽略未知字段；剥离 markdown 代码块包裹 + 前后噪声文本。

## 启示
Groq 免费配额为轻量试用设计，非批量流水线。每一步都需验证真实限制，不能照搬模型名。
