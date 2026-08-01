# CloudWisePod 第一版产品设计

> **历史文档，不再执行。** 本文描述已废弃的 Cloudflare/TypeScript 多用户方案，仅用于追溯。现行目标见 [`../../product-goal.md`](../../product-goal.md)，实施路线见 [`../../implementation-roadmap.md`](../../implementation-roadmap.md)。

日期：2026-06-14

## 1. 背景与目标

CloudWisePod 是一个部署在 Cloudflare 上的个人云端播客知识卡片系统。它借鉴 Podwise.ai 的核心价值，但第一版聚焦自用和基础多用户能力：把 RSS 播客和手动上传的音频转成完整转录稿、深度知识卡片、单期问答内容，以及 Obsidian 友好的 Markdown 文件。

第一版目标：

- 让用户能够添加播客 RSS，发现 episode，并手动选择要处理的内容。
- 让用户能够上传受控格式和大小的音频文件，并手动处理。
- 为每个处理过的 episode/upload 保存完整带时间戳转录稿。
- 为每个处理过的 episode/upload 生成深度知识卡片。
- 支持基础全文搜索和单期问答。
- 支持单期 Markdown 导出和多期 zip 批量导出。
- 使用 Cloudflare 原生资源构建，保留多用户、AI provider、YouTube、CLI、语义检索等后续扩展点。

## 2. 第一版范围

### 2.1 核心流程

1. 用户注册、登录、登出。
2. 用户添加 RSS 播客订阅，或上传音频文件。
3. 系统展示可处理的 episode/upload。
4. 用户手动点击“处理”。
5. 后台任务获取音频、调用转录 provider、保存完整转录稿、调用分析 provider、生成知识卡片。
6. 用户查看详情页、搜索已处理内容、针对单期内容问答、导出 Markdown 或 zip。

### 2.2 明确不做

第一版不实现：

- YouTube 导入。
- 新 episode 自动 AI 处理。
- 全库语义搜索。
- 跨 episode RAG 问答。
- 主题聚类或知识图谱。
- Obsidian 插件。
- 本地同步 CLI。
- 支付、套餐、团队空间。
- 复杂音视频转码。

这些能力可作为二期或后续扩展，但第一版不能被它们阻塞。

## 3. 技术架构

第一版采用 Remix / React Router 全栈应用，部署到 Cloudflare Pages/Workers，并使用 Cloudflare 原生服务承载数据、存储和异步任务。

### 3.1 Web App 层

Web App 负责用户交互：

- 注册、登录、登出。
- podcast 订阅管理。
- episode/upload 列表。
- 处理任务状态展示。
- 详情页。
- 搜索。
- 单期问答。
- Markdown/zip 导出。

### 3.2 Application Service 层

业务逻辑放在服务层，避免页面直接耦合 provider、数据库和队列细节：

- `AuthService`
- `PodcastService`
- `EpisodeService`
- `UploadService`
- `ProcessingJobService`
- `TranscriptService`
- `AnalysisService`
- `SearchService`
- `ExportService`
- `QuestionAnsweringService`

每个服务应有清晰输入输出，并通过显式依赖访问 D1、R2、Queues 和 provider。

### 3.3 Provider 层

AI 和外部能力通过 provider 抽象：

- `TranscriptionProvider`：输入音频 URL 或临时访问地址，返回完整文本和时间戳 segments。
- `AnalysisProvider`：输入 transcript 和来源元数据，返回结构化知识卡片 JSON。
- `ChatProvider`：输入单期上下文和用户问题，返回答案及相关时间戳依据。
- `StorageProvider`：封装 R2 对象读写和临时下载地址。
- `ExportRenderer`：将 analysis/transcript 渲染为 Markdown 或 zip 文件。

第一版默认接一个稳定的外部 provider，例如 OpenAI 或同等能力的转录/LLM API。代码层不能让页面或业务流程直接依赖具体模型名称。

### 3.4 Cloudflare 资源

- **D1**：用户、会话、podcast、episode、upload、处理任务、分析元数据、导出记录、usage records。
- **R2**：上传音频、可能的 RSS 音频缓存、转录全文、转录 segments、知识卡片 JSON、生成的 Markdown 和 zip。
- **Queues**：转录、分析、批量导出等异步任务。
- **Cron Triggers**：定期刷新 RSS，发现新 episode，但不自动 AI 处理。
- **Workers/Pages Functions**：承载 Remix/React Router 服务端逻辑。
- **KV（可选）**：短期缓存、速率限制、会话辅助；不作为核心持久化数据源。

## 4. 数据模型

所有用户拥有的数据都必须带 `user_id`，所有查询必须按当前用户隔离。

### 4.1 用户与认证

`users`：

- `id`
- `email`
- `password_hash` 或第三方 auth 标识
- `created_at`
- `updated_at`

`sessions`：

- `id`
- `user_id`
- `expires_at`
- `created_at`

### 4.2 播客来源

`podcasts`：

- `id`
- `user_id`
- `feed_url`
- `title`
- `description`
- `image_url`
- `site_url`
- `last_fetched_at`
- `created_at`

`episodes`：

- `id`
- `user_id`
- `podcast_id`
- `guid`
- `title`
- `description`
- `audio_url`
- `duration_seconds`
- `published_at`
- `processing_status`
- `created_at`

### 4.3 手动上传

`uploads`：

- `id`
- `user_id`
- `original_filename`
- `content_type`
- `size_bytes`
- `duration_seconds`
- `r2_object_key`
- `processing_status`
- `created_at`

第一版上传限制：

- 支持 `mp3`、`m4a`、`wav`。
- 单文件最大 100MB。
- 单条音频最长 2 小时。
- Workers 不做转码；超限或格式不支持时提示用户在本地转换或压缩。

### 4.4 处理任务

`processing_jobs`：

- `id`
- `user_id`
- `source_type`：`episode` 或 `upload`
- `source_id`
- `job_type`：`transcribe`、`analyze` 或 `export`
- `status`：`queued`、`running`、`succeeded` 或 `failed`
- `attempt_count`
- `error_message`
- `provider`
- `model`
- `started_at`
- `finished_at`

### 4.5 转录稿

`transcripts`：

- `id`
- `user_id`
- `source_type`
- `source_id`
- `language`
- `provider`
- `model`
- `text_r2_key`
- `segments_r2_key`
- `duration_seconds`
- `created_at`

完整文本和 segments 存 R2；D1 只保存元数据。这样避免把长文本塞进 D1，也方便未来重建搜索索引、问答上下文或导出格式。

### 4.6 知识卡片与分析结果

`analyses`：

- `id`
- `user_id`
- `source_type`
- `source_id`
- `provider`
- `model`
- `title`
- `summary`
- `content_json_r2_key`
- `markdown_r2_key`
- `created_at`

`content_json` 包含：

- optimized title
- summary
- key points
- chapter timeline
- quotes
- entities
- action items
- glossary
- suggested questions
- tags

### 4.7 使用与成本记录

`usage_records`：

- `id`
- `user_id`
- `source_type`
- `source_id`
- `provider`
- `model`
- `operation`
- `input_units`
- `output_units`
- `estimated_cost`
- `created_at`

该表用于自用成本观察，也为未来配额、计费或多用户管理预留基础。

## 5. 处理流程与状态机

### 5.1 RSS 刷新

1. 用户添加 podcast RSS。
2. 系统解析 feed，写入 podcast metadata 和 episode 列表。
3. Cron 定期刷新 RSS，只发现新 episode，不自动 AI 处理。
4. Episode 初始状态为 `unprocessed`。
5. 用户在列表页点击“处理”。

### 5.2 上传音频

1. 用户选择 `mp3`、`m4a` 或 `wav` 文件。
2. 前端校验格式和大小。
3. 后端生成 R2 上传 URL 或通过受控接口上传。
4. 上传完成后写入 `uploads` 记录。
5. 系统记录或校验时长，最大 2 小时。
6. Upload 初始状态为 `unprocessed`。
7. 用户点击“处理”。

### 5.3 AI 处理

每个 source 的处理拆成两个阶段。

#### Transcribe

1. 创建 `processing_jobs`，`job_type = transcribe`。
2. Queue worker 获取 RSS 音频 URL 或 R2 临时 URL。
3. 调用 `TranscriptionProvider`。
4. 保存 transcript text 和 segments 到 R2。
5. 写入 `transcripts` 元数据。
6. 更新 source 状态为 `transcribed`。

#### Analyze

1. 创建 `processing_jobs`，`job_type = analyze`。
2. Queue worker 读取 transcript。
3. 调用 `AnalysisProvider`。
4. 保存结构化 JSON 和 Markdown 到 R2。
5. 写入 `analyses` 元数据。
6. 更新 source 状态为 `processed`。

### 5.4 Source 状态

`episodes` 和 `uploads` 使用统一状态：

- `unprocessed`
- `queued`
- `transcribing`
- `transcribed`
- `analyzing`
- `processed`
- `failed`

失败时记录失败阶段、provider、错误摘要和 retry count，并允许用户手动重试。

### 5.5 重跑策略

- 转录成功、分析失败时，重试只重跑分析。
- 用户修改分析模板时，只重跑分析。
- 音频变更或转录 provider 变更时，可选择重跑转录和分析。
- 默认最多自动重试 2-3 次；超过后进入 `failed`，等待手动处理。
- 队列 worker 必须幂等，重复执行不能产生重复 transcript、analysis 或 export 记录。

## 6. 知识卡片、问答、搜索与导出

### 6.1 知识卡片结构

详情页展示一份深度知识卡片：

1. 标题与来源信息：优化标题、原始标题、podcast 名称或上传文件名、发布时间/上传时间、时长、来源链接。
2. 摘要：用于快速判断内容价值。
3. 核心观点：结构化 bullet list，每条保留足够上下文。
4. 章节时间轴：章节标题、时间范围、章节摘要。
5. 金句/重要摘录：带时间戳，可复制，尽量关联到转录片段。
6. 实体：人物、书籍、工具、公司/组织、概念和其他专有名词。
7. 行动项：todo、experiment、habit、reading 等实践建议。
8. 术语解释：对重要术语做简短解释。
9. 推荐追问：适合继续用单期问答追问的问题。
10. 完整转录稿：按时间戳分段展示，可搜索、复制。

### 6.2 单期问答

详情页提供 “Ask this episode” 功能：

1. 用户输入问题。
2. 后端读取该期 transcript 和 analysis。
3. 对内容做窗口化或截断策略。
4. 调用 `ChatProvider`。
5. 返回答案，并尽量附上相关时间戳或片段位置。

第一版只针对单期，不跨库。若 transcript 超出模型上下文，第一版使用关键词分段检索或简单 chunk 排序；embedding 检索留到后续阶段。

### 6.3 搜索

第一版支持基础全文搜索，覆盖：

- podcast title
- episode/upload title
- analysis summary
- key points
- transcript text

实现可以优先用 D1 FTS 或维护轻量 search index。如果 D1 全文检索能力不足以支撑完整体验，第一版允许先提供标题、摘要、知识卡片字段的搜索，并把 transcript 全文索引作为同阶段增强项记录在计划中。

### 6.4 Markdown 与 Obsidian 导出

单期 Markdown 模板包含：

- YAML frontmatter：`title`、`source_type`、`podcast`、`published_at`、`processed_at`、`duration`、`tags`、`entities`。
- Obsidian tags，例如 `#podcast`、`#ai-notes`。
- Obsidian 双链，例如 `[[人物]]`、`[[书名]]`、`[[概念]]`。
- 正文：摘要、核心观点、时间轴、金句、实体、行动项、术语解释、推荐追问。
- 可选完整转录稿附录。

批量导出流程：

1. 用户选择多期。
2. 后台创建 export job。
3. Worker 生成 zip。
4. zip 内按 podcast/source 分目录，每期一个 `.md`。
5. 可选包含 transcript `.md` 或附录。
6. 导出文件放 R2，并设置过期清理策略。

## 7. 权限与安全

第一版是基础多用户产品，必须从一开始做好数据隔离：

- 所有核心表包含 `user_id`。
- 所有查询按当前 session user 过滤。
- 任何 `source_id` 操作都先验证 ownership。
- R2 object key 包含 user namespace，例如：
  - `users/{user_id}/uploads/{upload_id}/audio.mp3`
  - `users/{user_id}/transcripts/{source_type}/{source_id}/segments.json`
  - `users/{user_id}/exports/{export_id}.zip`
- 导出下载使用短期 signed URL 或受控下载接口。
- 不提供跨用户分享。
- provider 原始错误只写内部日志；前端展示安全、可理解的错误摘要。

## 8. 错误处理

第一版必须覆盖以下失败场景：

- RSS feed 无法访问或格式异常。
- episode 没有可用 audio enclosure。
- 上传文件格式不支持。
- 文件超过 100MB 或超过 2 小时。
- R2 上传失败。
- 转录 provider 超时、失败或返回空内容。
- 分析 provider 输出不是合法 JSON。
- Markdown 渲染失败。
- zip 导出失败。
- 队列任务重复执行。
- 用户访问不属于自己的资源。

处理策略：

- 所有异步任务写入 `processing_jobs`。
- 失败时保存用户可读错误摘要。
- source 状态进入 `failed`。
- 支持手动重试。
- 任务设计为幂等，避免重复写入。

## 9. 测试策略

第一版测试重点是关键链路可靠，而不是追求 100% 覆盖率。

### 9.1 单元测试

- RSS parser。
- Markdown renderer。
- Obsidian frontmatter、tags、双链生成。
- provider adapter mock。
- 状态机转换。
- ownership guard。

### 9.2 集成测试

- 添加 RSS 后生成 podcast/episodes。
- 上传元数据后创建 upload。
- 手动处理后依次执行 transcribe job 和 analyze job。
- 分析失败后重试。
- 单期 Markdown 导出。
- 不同用户不能访问彼此资源。

### 9.3 端到端/冒烟测试

- 注册登录。
- 添加 podcast。
- 处理一个短音频。
- 查看知识卡片。
- 下载 Markdown。
- 批量导出 zip。

### 9.4 Provider contract tests

- 固定输入，验证 provider 返回结构满足 schema。
- 分析 JSON 不合法时能修复或以可见错误失败。

## 10. 后续扩展点

后续阶段可以独立扩展：

- YouTube 导入：新增 source/provider，不改变核心 transcript/analysis/export 模型。
- 自动处理规则：在 podcast 或 user 设置中增加 auto-processing policy。
- 语义搜索和全库问答：引入 embeddings、chunking、Cloudflare Vectorize 或其他向量库。
- CLI 同步：基于已有导出 API 拉取 Markdown 到本地 Obsidian vault。
- Obsidian 插件：直接在 vault 内拉取、搜索和同步内容。
- 配额和计费：基于 usage_records 增加 limits、plans 和 billing。
- 组织/团队：在 user_id 之上引入 workspace_id。
