# CloudWisePod

> 单 Owner、自托管的播客学习与知识文章工作空间。

把播客与文档转化为可回听、可检索的学习重点与个人笔记，再自动发现选题、写作与审校知识文章。需要承担个人主张和制作发布包时，继续使用现有创作工作台。

---

## 核心能力

### 内容处理

- **RSS 订阅 + 批量入队**：订阅播客 feed，30 分钟自动拉取新单集；单集列表页勾选多集一键批量入队
- **音频上传**：mp3/m4a/wav，每个 Source 持久保存标准化 EvidenceAudio（SHA256 校验）
- **AI 转录**：Groq `whisper-large-v3`，带稳定 Segment ID 的逐句时间戳
- **知识卡片**：不可变 ArtifactVersion；摘要/要点/章节/金句全部携带真实 Citation；金句逐字校验
- **高光片段（AI DJ）**：AI 自动选出最值得听的高光区间，播放清单页面（AI 解说音轨 Narration + 原音区间交替播放；Narration 由自托管 Kokoro TTS 合成，默认零成本）

### 知识管理

- **全局素材 Inbox**：所有 Source 的 KeyPoint 聚合在一处，支持筛选、批量状态、全文与本地向量混合搜索
- **标注 / 收藏 / 集合**：对任意 Citation 加个人标注、收藏、归入跨 Source 主题集合
- **知识图谱**：KeyPoint 粒度的力导向可视化——同一集合的要点成簇，文本相似度建议跨 Episode 关联
- **版本历史**：查看 Transcript / KnowledgeCard 的全部不可变版本，一键回退

### 自动知识文章

- **存量素材选题**：从已就绪重点、来源笔记和个人反思中寻找文章方向；支持单集、上传与文档。
- **自动成稿**：一次触发后后台选题、写作、独立审校，必要时最多修订一次；关闭页面后继续。
- **明确归因**：来源整理、个人笔记与 AI 综合分开显示，来源链接由程序生成；不自动承担个人主张。
- **可控自动化**：`/knowledge-articles` 可立即生成或开启后台自动生成；默认每天最多一篇，素材更新等待30分钟。
- **独立 AI 配置**：读取 `.env` 的 `POD_BASE_URL`、`POD_API_KEY`、`POD_MODEL`，不会覆盖转录和学习模型。
- **恢复与导出**：同一输入复用结果、按阶段恢复、失败可重试；通过审校的文章可下载 Markdown。

### 个人听学与回顾

- **连续收听**：主要学习页面切换保留播放；持久队列支持排序、续听和显式连播。原音与 DJ 分别保存进度，支持倍速、±15秒、睡眠定时和媒体控制。
- **随听笔记**：自动捕获来源快照与位置，区分来源笔记和我的理解，保留修订与冲突草稿。
- **统一搜索与首页**：优先继续听、最近笔记、新文章和待处理，统一找回原文、文档、重点与文章段落。
- **多方向文章**：从新材料召回历史笔记，分别保存候选、选材、修订与审校；工作稿保留旧通过版。
- **学习问题**：明确记录问题、目标与进展，确认材料关联，围绕当时的问题版本生成文章。
- **文章增量更新**：新材料形成更新提案，接受后修订同一篇文章，旧通过版继续可读。
- **私有语音笔记**：冻结实际播放位置，录音转成独立建议；编辑不会被转写覆盖，明确保存才成为笔记。
- **每日与每周回顾**：每周生成最多5题；日常短会话复用已有题目，个人自评安排1/3/7/14个日历日后再看。
- **任务与成本**：查看冻结模型、材料和费用状态，持久暂停类别/方向、停止任务及调整排队优先级。

详见[第三轮使用说明](docs/personal-learning-v3.md)、[开发与验证记录](docs/superpowers/plans/2026-10-01-personal-learning-and-knowledge-articles-v3-validation.md)和[综合验收报告](docs/acceptance/2026-10-personal-learning-v3.md)。真实个人笔记质量与手机后台体验仍待验证。

### 内容生产

- **跨集选题**：Theme 组织支持、补充与冲突材料；Scout 生成并去重候选提案
- **可审核 Brief**：Curator 从 Owner 接受的提案中选材，Owner 确认后才授权付费写作
- **证据门禁**：Writer 输出 EvidenceMap；独立 EvidenceReviewer 与 StyleEditor 都完成后才可生成发布包
- **公众号发布包**：富文本、Markdown、纯文本、候选标题、摘要、推荐语、封面文案与可下载封面
- **费用治理**：角色级 Provider/模型、Owner 维护的价格表、月度/单篇预算和不可变用量审计
- **一等文档 Source**：网页快照、粘贴文本与 PDF，支持稳定 Citation、知识卡片、搜索和版本历史

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
- **备份/恢复**：`cloudwisepod backup|restore` 一致性备份包（manifest + SHA256），可在全新实例恢复；`scripts/export.sh` / `scripts/remote-setup.sh` 一键跨机器迁移
- **公网安全**：CSRF、登录限流、可信代理 + Secure Cookie、SSRF 防护（逐跳重定向校验 + 私网拦截）

---

## 快速开始

### 前置依赖

- Go 1.25+
- ffmpeg（音频转码，适配 Groq Whisper 上传限制）
- Groq API key（https://console.groq.com/keys）
- Kokoro TTS（可选，仅 Narration 解说音轨需要；未安装时 Narration 自动跳过、不影响其他功能）

直接启动二进制也会读取当前目录 `.env`（已有进程环境变量优先）。`POD_*` 必须三项同时填写；已有月预算时，需要在内容工作台的模型价格表中登记 `pod` Provider 对应模型的价格。

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

`/app/data` 单一持久卷承载全部数据（数据库 + EvidenceAudio + 私有录音 + 临时文件 + 备份）。

```bash
# 或用 Compose 从源码构建
docker compose up -d
```

---

## 配置

| 变量 | 必填 | 说明 | 默认 |
|---|---|---|---|
| `SESSION_SECRET` | ✅ | 会话密钥，`openssl rand -hex 32` | — |
| `GROQ_API_KEY` | ✅ | Groq API key（默认转录 Provider） | — |
| `POD_BASE_URL` | 否 | 自动知识文章的 OpenAI 兼容基础地址（含 `/v1` 等路径） | — |
| `POD_API_KEY` | 否 | 自动知识文章的独立密钥 | — |
| `POD_MODEL` | 否 | 自动知识文章使用的文本模型 | — |
| `POD_REVIEW_MODEL` | 否 | 独立审校模型，使用同一POD连接 | `POD_MODEL` |
| `POD_DISCOVERY_MODEL` | 否 | 发现选题模型 | `POD_MODEL` |
| `POD_SELECTION_MODEL` | 否 | 检索后选材模型 | `POD_MODEL` |
| `POD_WRITE_MODEL` | 否 | 写作及定向修订模型 | `POD_MODEL` |
| `POD_LEARNING_REVIEW_MODEL` | 否 | 学习回顾模型 | `POD_MODEL` |
| `VOICE_ASR_PROVIDER` / `VOICE_ASR_MODEL` | 否 | 个人语音转录选择；支持 groq/openai，未设沿用站内转录配置 | 站内转录配置 |
| `VOICE_ASR_BASE_URL` / `VOICE_ASR_API_KEY` | 否 | 独立音频连接，成对填写，不能复用 POD 文本端点 | — |
| `OPENAI_API_KEY` | 否 | OpenAI key；仅按单次任务显式授权使用 | — |
| `PORT` | 否 | 监听端口 | 8080 |
| `DATA_DIR` | 否 | 统一数据目录（DB/evidence/voice-notes/tmp/backups） | ./data |
| `PUBLIC_URL` | 否 | 公网 URL；决定 Secure Cookie 与 Citation 链接 | http://localhost:8080 |
| `TRUSTED_PROXIES` | 否 | 受信任反向代理 CIDR，逗号分隔 | 空（只信直连） |
| `NARRATION_DIR` | 否 | Narration 解说音轨目录（独立于 evidence，不进备份） | `$DATA_DIR/narrations` |
| `KOKORO_BINARY` | 否 | 解说 TTS 可执行文件（推荐 `scripts/kokoro` 包装，见 [生产部署「解说音轨」](docs/production-deployment.md)） | `kokoro`（PATH 查找） |
| `KOKORO_VOICE` | 否 | Narration 默认音色（中文 `zf_xiaobei`/`zm_yunyang`；英文 `af_heart`） | `af_heart` |
| `KOKORO_MODEL` / `KOKORO_MODEL_DIR` | 否 | 权重：完整 onnx 路径 / 目录（取其中 `kokoro-v1.0.onnx` + `voices-v1.0.bin`） | 空 |
| `KOKORO_LANGUAGE` | 否 | `en`/`zh`；仅用于 `tts-check` 预检一致性提示，实际语言按音色前缀推断 | `en` |
| `KOKORO_TIMEOUT_SECONDS` | 否 | 单次合成超时秒数 | 120 |

解说引擎未安装时 Narration 自动跳过、不影响原音；预检与短句试听：

```sh
cloudwisepod tts-check                    # 仅预检
cloudwisepod tts-check --output /tmp/a.wav # 预检 + 真实合成试听
```

---

## 备份与恢复

```bash
# 备份（一致性快照 + 证据音频 + 明确保留的私人录音 + manifest）
./cloudwisepod backup /path/backup.tar.gz

# 恢复到全新目录
DATA_DIR=/new/instance ./cloudwisepod restore /path/backup.tar.gz

# 目标已有数据时显式覆盖
DATA_DIR=/new/instance ./cloudwisepod restore /path/backup.tar.gz --force
```

新备份为 v2，包含已保存笔记且明确保留的私人录音，恢复仍兼容旧 v1。恢复校验数据库及音频的长度、SHA256和清单路径，私有录音保持0600权限。临时未保存录音不进入长期备份；恢复后文字与历史锚点仍在，缺失音频明确提示。Narration音轨仍不进入备份。

### 跨机器迁移

```bash
# 本机导出（时间戳命名 + chmod 600 + 打印 SHA256）
SESSION_SECRET=<密钥> ./scripts/export.sh ~/backups/cloudwisepod

# 传输（加密通道）后，新机器上：
git clone git@github.com:woyin/OrangeCast.git && cd OrangeCast
./scripts/remote-setup.sh --backup ~/cloudwisepod-YYYYMMDD-HHMMSS.tar.gz
```

`remote-setup.sh` 一条命令完成：编译 → 恢复备份 → 生成并持久化 `SESSION_SECRET`（`DATA_DIR/.session-secret`，0600，重启不失效）→ 启动服务。已有数据库时拒绝覆盖，需 `--force-restore`；`--port` 指定端口（默认 8080）。

**安全注意**：备份包内的 `settings` 表包含 Provider API key 明文（数据库快照的一部分，恢复后新实例直接可用、无需重配）。因此备份包是敏感文件——只能经加密通道（scp / AirDrop / 加密U盘）传输，不要经网盘明文、聊天工具或邮件发送；用完即删。

---

## 架构

```
cmd/cloudwisepod/        入口（serve / backup / restore）
internal/
  config/                环境变量 + DATA_DIR 布局
  store/                 SQLite + FTS5 + 迁移系统 + 全部仓储
    migrations/          有序 SQL 迁移（0001–0069）
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
docs/adr/                架构决策记录
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
| **Source** | Owner 选择进入证据与生产流程的 Episode、Upload 或不可变 Document 快照 |
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

测试覆盖迁移安全、备份/恢复、认证与 CSRF、SSRF 防护、持久任务队列、证据引用、搜索、AI 工作流、内容生产和 HTTP handler。当前逐包覆盖率与已批准的临时地板以 `make cover-gate` 输出及 `scripts/cover-gate.sh` 为准，避免在文档中维护易失真的快照。

---

## 产品边界

**CloudWisePod 是证据优先的内容生产工作台**——PrimarySource 不可改写，AI 衍生内容分层并保留来源关系：
- **CitedDerivative（带证衍生）**：忠实于原文，挂 Citation 可逐字核验（摘要/要点/章节/金句）。
- **GeneratedDerivative（生成衍生）**：AI 重新组织/讲解，明确标注非原文，挂 Reference 仅表示参考（Gist/Paraphrase/StudyChat/Narration）。
平台帮 Owner 一眼区分两类，而不是替 Owner 判断。

**当前边界**：不做多用户/SaaS、不自动发布微信公众号、不允许无 Source 的联网事实直接进入文章。除 Podcast/Upload/Document 之外的素材来源是下一阶段扩展方向。

详细产品目标见 [`docs/product-goal.md`](docs/product-goal.md)，部署指南见 [`docs/production-deployment.md`](docs/production-deployment.md)。
