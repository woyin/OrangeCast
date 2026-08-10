# CloudWisePod

> 单 Owner、自托管、默认零 AI 成本的播客证据库。

把主动选择的播客音频转化为持久、可检索、可回听的证据，再在严格分层的衍生内容之上提供学习辅助（带证衍生逐字可核验，生成衍生明确标注为 AI 生成），最终通过 Markdown 沉淀到个人知识库。

---

## 核心能力

### 内容处理

- **RSS 订阅 + 批量入队**：订阅播客 feed，30 分钟自动拉取新单集；单集列表页勾选多集一键批量入队
- **音频上传**：mp3/m4a/wav，每个 Source 持久保存标准化 EvidenceAudio（SHA256 校验）
- **AI 转录**：Groq `whisper-large-v3`，带稳定 Segment ID 的逐句时间戳
- **知识卡片**：不可变 ArtifactVersion；摘要/要点/章节/金句全部携带真实 Citation；金句逐字校验
- **高光片段（AI DJ）**：AI 自动选出最值得听的高光区间，播放清单页面（AI 解说音轨 Narration + 原音区间交替播放；Narration 由自托管 Kokoro TTS 合成，默认零成本）

### 知识管理

- **全局关键要点**：所有 Source 的 KeyPoint 聚合在一处，按时间排列，FTS5 全文搜索
- **标注 / 收藏 / 集合**：对任意 Citation 加个人标注、收藏、归入跨 Source 主题集合
- **知识图谱**：KeyPoint 粒度的力导向可视化——同一集合的要点成簇，文本相似度建议跨 Episode 关联
- **版本历史**：查看 Transcript / KnowledgeCard 的全部不可变版本，一键回退

### 检索与播放

- **分段级全文搜索**：FTS5 命中返回实际 Transcript Segment 与时间范围，直接跳转到 EvidenceAudio 对应位置
- **播放器联动**：转录稿/章节/金句三层时间戳联动；`?t=` / `#seg-` 深链跳转
- **证据问答（EvidenceQA）**：查证型，只使用模型实际引用的 Segment；无可靠引用明确拒答（CitedDerivative，挂 Citation）
- **复述讲解（Paraphrase）**：在任意片段上触发"重讲"，AI 用别的话重新讲解（GeneratedDerivative，挂 Reference，标为非原文）；同锚点保留最近 3 次
- **学习对话（StudyChat）**：围绕本期内容多轮自由提问；硬约束一"无 Reference 不生成"+ 硬约束二"ReferenceCheck 主题锚定校验"防止脱稿；按会话（StudySession）保存
- **Markdown 下载**：确定性 Obsidian Markdown；证据块（CitedDerivative）带 Citation 链接（?t=），AI 讲解块（GeneratedDerivative）带 Reference 链接（?ref=），视觉区分；可一键下载"仅证据"或"含 AI 讲解"

### 可靠性与安全

- **可恢复任务队列**：SQLite 持久化，租约 + 心跳，进程重启自动恢复
- **处理进度**：全局进度页，5 秒自动轮询，显示正在处理 + 排队中 + 最近完成
- **备份/恢复**：`cloudwisepod backup|restore` 一致性备份包（manifest + SHA256，不含密钥），可在全新实例恢复
- **公网安全**：CSRF、登录限流、可信代理 + Secure Cookie、SSRF 防护（逐跳重定向校验 + 私网拦截）

---

## 快速开始

### 前置依赖

- Go 1.25+
- ffmpeg（音频转码，适配 Groq Whisper 上传限制）
- Groq API key（https://console.groq.com/keys，免费）
- Kokoro TTS（可选，仅 Narration 解说音轨需要；未安装时 Narration 自动跳过、不影响其他功能）

### 本地运行

```bash
# 1. 配置环境变量
cp .env.example .env
# 填入 SESSION_SECRET（openssl rand -hex 32）和 GROQ_API_KEY

# 2. 启动
set -a; source .env; set +a
go run ./cmd/cloudwisepod

# 3. 访问 http://localhost:8080/register 认领唯一 Owner
```

认领后注册永久关闭。后续用 `/login` 登录。

### Docker 部署（VPS 推荐）

```bash
docker pull ghcr.io/woyin/orangecast:latest
docker run -d --name orangecast --restart unless-stopped \
  -p 8080:8080 \
  -e SESSION_SECRET="$(openssl rand -hex 32)" \
  -e GROQ_API_KEY="你的 Groq API Key" \
  -e PUBLIC_URL="https://cwp.example.com" \
  -v orangecast-data:/app/data \
  ghcr.io/woyin/orangecast:latest
```

`/app/data` 单一持久卷承载全部数据（数据库 + EvidenceAudio + 临时文件 + 备份）。

```bash
# 或用 Compose 从源码构建
docker compose up -d
```

---

## 配置

| 变量 | 必填 | 说明 | 默认 |
|---|---|---|---|
| `SESSION_SECRET` | ✅ | 会话密钥，`openssl rand -hex 32` | — |
| `GROQ_API_KEY` | ✅ | Groq API key（默认零成本 Provider） | — |
| `OPENAI_API_KEY` | 否 | OpenAI key；仅按单次任务显式授权使用 | — |
| `PORT` | 否 | 监听端口 | 8080 |
| `DATA_DIR` | 否 | 统一数据目录（DB/evidence/tmp/backups） | ./data |
| `PUBLIC_URL` | 否 | 公网 URL；决定 Secure Cookie 与 Citation 链接 | http://localhost:8080 |
| `TRUSTED_PROXIES` | 否 | 受信任反向代理 CIDR，逗号分隔 | 空（只信直连） |
| `NARRATION_DIR` | 否 | Narration 解说音轨目录（独立于 evidence，不进备份） | `$DATA_DIR/narrations` |
| `KOKORO_BINARY` | 否 | Kokoro TTS 二进制路径（自托管，零成本） | `kokoro`（PATH 查找） |
| `KOKORO_VOICE` | 否 | Narration 默认音色 | `af_heart` |
| `KOKORO_MODEL` | 否 | Kokoro 模型文件路径（某些发行版需要） | 空 |

---

## 备份与恢复

```bash
# 备份（一致性快照 + 证据音频 + manifest，不含密钥）
./cloudwisepod backup /path/backup.tar.gz

# 恢复到全新目录
DATA_DIR=/new/instance ./cloudwisepod restore /path/backup.tar.gz

# 目标已有数据时显式覆盖
DATA_DIR=/new/instance ./cloudwisepod restore /path/backup.tar.gz --force
```

恢复会校验 manifest 格式版本、数据库 SHA256 与每个证据文件的 SHA256。

---

## 架构

```
cmd/cloudwisepod/        入口（serve / backup / restore）
internal/
  config/                环境变量 + DATA_DIR 布局
  store/                 SQLite + FTS5 + 迁移系统 + 全部仓储
    migrations/          有序 SQL 迁移（0001–0008）
  auth/                  argon2id 密码 + cookie session + CSRF + 限流
  models/                领域类型
  provider/              Groq/OpenAI 实现 + Citation 校验 + Highlight
  rss/                   gofeed 解析 + SSRF 防护 + cron
  queue/                 SQLite 持久 worker + lease/heartbeat + ffmpeg 转码
  server/                html/template SSR + 原生 JS + REST API
  safehttp/              共享 SSRF 防护 HTTP 客户端
  backup/                一致性备份/恢复（tar.gz + manifest）
  markdown/              确定性 Obsidian Markdown 渲染
  evalset/               KnowledgeCard 质量自动校验
docs/adr/                17 份架构决策记录
CONTEXT.md              领域词汇表
```

### 技术栈

- **Go 1.25** 单二进制，无 CGO（modernc.org/sqlite 纯 Go）
- **SQLite** + WAL + FTS5（单文件数据库）
- **html/template** SSR + 原生 JS（无前端框架）
- **D3.js** 知识图谱可视化（CDN）
- **Groq** 默认零成本 Provider（whisper-large-v3 + llama-3.3-70b）

---

## 领域概念

| 概念 | 定义 |
|---|---|
| **Owner** | 唯一使用并拥有实例全部内容的人；实例只能被认领一次 |
| **Source** | Owner 选择处理的音频（Episode 或 Upload） |
| **EvidenceAudio** | 每个 Source 持久保存的标准化音频 |
| **Transcript** | EvidenceAudio 的带时间对齐文本，由 Segment 组成 |
| **Segment** | 有起止时间的连续文本，Citation 的最小核验单位 |
| **Citation** | 衍生内容与 Segment 之间的可验证关系；时间范围由程序计算 |
| **KnowledgeCard** | AI 生成的结构化中间产物（摘要/要点/章节/金句，全部带 Citation） |
| **Highlight** | AI 选出的高光音频区间（DJ 模式），带稳定 ID |
| **Reference** | GeneratedDerivative 与 Segment 的弱关联（表示参考，非可核验） |
| **CitedDerivative** | 忠实于原文、挂 Citation 可核验的衍生内容 |
| **GeneratedDerivative** | AI 重新组织/讲解、挂 Reference 非原文的衍生内容 |
| **Gist** | 高光/章节的 AI 概括解说（GeneratedDerivative） |
| **Paraphrase** | 按需触发的局部重讲（GeneratedDerivative，按锚点保留最近 3 次） |
| **EvidenceQA** | 查证型问答，挂 Citation、无可靠引用拒答 |
| **StudyChat** | 学习型对话（GeneratedDerivative），两条硬约束防脱稿 |
| **StudySession** | 一次 StudyChat 交互的会话容器 |
| **Narration** | 高光 Gist 的 TTS 解说音轨（GeneratedDerivative 音频形态，自托管 Kokoro） |
| **Annotation** | Owner 在 Citation 上的个人标注 |
| **Pin** | Owner 标记 Citation 值得记住 |
| **Collection** | Owner 把跨 Source 的 Citation 按主题组织成的集合 |

完整定义见 [`CONTEXT.md`](CONTEXT.md)，架构决策见 [`docs/adr/`](docs/adr/)。

---

## 开发

```bash
# 全量验证
go test ./...
go vet ./...
go build ./cmd/cloudwisepod
git diff --check
```

测试覆盖（594+ 测试函数）：迁移（含数据修复 0015、破坏性迁移单 Owner 守卫与备份、loadMigrations 版本连续校验、applyOne 事务回滚、AppliedVersion/migrationTableExists/usersTableExists）、备份/恢复 E2E（含 Create/Restore 错误路径、目标为目录、DB/证据哈希校验、缺失证据、非 gzip 包、临时目录创建失败、manifest JSON 损坏、空 DBSHA、未识别条目跳过、copyFromTar 错误、DB 重命名失败、证据目录创建失败）、Owner 认领、注册（弱密码/非法邮箱拒绝/已被认领）、登录（成功/错误密码/限流）、CSRF/限流、safehttp SSRF 防护（safeDialer 私网拦截）、EvidenceAudio、worker 崩溃恢复（含 Run 主循环/doHighlight/fetchRawAudio 错误分支/downloadAudio 临时目录/响应体失败/默认 bundleFor/guessAudioExt/fileSHA256/evidenceBitrateKbps 非正时长/HighlightName）、Citation 校验（含 ValidateCard 全部分支、quoteVerbatim/normalize/segmentIndex/validCitations 边界）、EvalSet（含 Check/checkSample 全部分支、CheckReferenceSamples 放行/挡住/nil/报错、quoteInSegments/allCitations）、Markdown 渲染（含 fmtTime/frontmatterValue/referenceLinks）、分段搜索、EvidenceQA（含拒答/有引用/405/缺转录稿 404/Answer 报错 500/bundleFor 失败 500）、StudyChat 完整 handler（含长标题截断/会话复用/生成失败/缺转录稿 404/建会话/读历史/记录问题/持久化回答 DB 错误 500）、StudyChat 硬约束、Paraphrase（含参考片段不存在/无片段 400/Provider 报错 500/bundleFor 失败 500/持久化复述讲解失败 500）、Narration 合成（含 runCLI/nextNarrationVersion/NewKokoroProvider 默认值、doNarration nil bundle/无高光版本/损坏载荷/空高光/空 ID 全部分支）、Highlight 稳定 ID、DJ 页渲染（含无高光/无转录稿/非法路径 404、载荷损坏 500）、Narration serve（含记录/无记录缺文件 404）、批量入队（含 405）、上传（正常/拒绝类型/缺文件/multipart 解析失败）、Markdown 下载（含 with_generated/无转录稿 404/非法路径 404/卡片或转录损坏 500）、模板渲染（含未知模板错误/formatSeconds、全部页面渲染、StaticFS）、CLI 分发与参数解析（parseCommand/ensureArchiveExt/parseBackupArgs/parseRestoreArgs/backupCore/restoreCore 端到端、main 未知命令、runBackup/runRestore 用法错误退出码、DB 打开失败）、RSS 抓取与解析（FetchFeed/parseFeed 跳过分支/Refresher/runScheduled 错误分支/发布时间）、auth 中间件（含会话 cookie/限流/CSRF/RequireAuth）、store 生命周期与迁移安全（含 Open 多用户守卫/非法路径/ConsistencyBackup/sqlQuoteString/SourceTitle/Status/IndexSearch 错误/ListKeyPoints 分页/ListEpisodesPaginated 分页/GetJob/GetPodcastByID/GetUserByEmail、关闭库法覆盖 artifacts/evidence/jobs/narrations/study_sessions 错误分支）、server 全部 handler（含 source 详情/版本页 episode+upload/回退/音频回退/播客详情/播客列表 500/图谱 API/进度 API/KeyPoint 分页+500/Collection 校验/创建/列表 DB 错误/parseSegmentIDs、用删表法覆盖 handler 内部 DB 错误分支）、provider HTTP 层与模型路径（含 waitBetweenAnalysisWindows sleepFn、doWithRetry 重试耗尽、postJSON/uploadFileAsMultipart 错误、Transcribe HTTP 错误/非法 JSON、StudyChatAnswer/CheckReference 边界，经 WithBaseURL + httptest 隔离）、config 目录创建。各包覆盖率：markdown 100%、config 100%、auth 100%、safehttp 97%、evalset 100%、filehash 100%、provider 92%、server 95%、rss 100%、store 88%、queue 89%、backup 86%、cmd 98%。

---

## 产品边界

**CloudWisePod 是分层学习平台**——原始来源（EvidenceAudio + Transcript）不可改写，AI 衍生内容分两级：
- **CitedDerivative（带证衍生）**：忠实于原文，挂 Citation 可逐字核验（摘要/要点/章节/金句）。
- **GeneratedDerivative（生成衍生）**：AI 重新组织/讲解，明确标注非原文，挂 Reference 仅表示参考（Gist/Paraphrase/StudyChat/Narration）。
平台帮 Owner 一眼区分两类，而不是替 Owner 判断。

**明确不做**：多用户/SaaS、跨 Source RAG、语义搜索、Obsidian 插件/双向同步、自动处理所有新 Episode。

详细产品目标见 [`docs/product-goal.md`](docs/product-goal.md)，部署指南见 [`docs/production-deployment.md`](docs/production-deployment.md)。
