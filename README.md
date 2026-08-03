# CloudWisePod

单 Owner、自托管的播客证据库：把主动选择的音频转化为可检索、可回听、带真实引用的证据，再通过 Markdown 沉淀到个人知识库。

项目以 Go 单二进制或容器部署在 VPS。Groq 是默认零成本 Provider；目标产品只允许对单次任务显式授权付费 Provider。

> **当前状态：** Roadmap Phase 0–7 已完成：单 Owner 认领、EvidenceAudio、可恢复任务队列、不可变版本与真实 Citation、分段搜索、Markdown 下载、备份/恢复、公网安全基线。实施记录见 [`docs/implementation-roadmap.md`](docs/implementation-roadmap.md)，接手说明见 [`docs/handoff.md`](docs/handoff.md)。

## 已实现（Roadmap Phase 0–7）

- **单 Owner 认领**：实例首次认领唯一 Owner，之后注册永久关闭（ADR-0003）
- **RSS 订阅**：30 分钟 cron 拉取新单集；RSS 与音频下载共用 SSRF 防护客户端（逐跳重定向校验 + 私网拦截）
- **音频上传**：mp3/m4a/wav；每个 Source 持久保存标准化 EvidenceAudio（SHA256 校验，ADR-0005）
- **AI 转录**：Groq `whisper-large-v3`，带稳定 Segment ID 的逐句时间戳
- **知识卡片**：不可变 ArtifactVersion（ADR-0011）；摘要/要点/章节/金句全部携带真实 Citation；金句逐字校验（ADR-0008）
- **可恢复任务**：SQLite 驱动 worker，租约 + 心跳，进程重启后自动恢复（ADR-0006）
- **播放器联动**：转录稿/章节/金句三层时间戳联动；`?t=` / `#seg-` 深链跳转
- **单期 Q&A**：只使用模型实际引用的 Segment；无可靠引用明确拒答（Phase 7）
- **全库搜索**：分段级 FTS5，命中返回实际 Segment 与时间范围
- **Markdown 下载**：确定性 Obsidian Markdown（frontmatter + Citation 链接），单 Source 一键下载
- **备份/恢复**：`cloudwisepod backup|restore` 一致性备份包（manifest + SHA256，不含密钥），可在全新实例恢复
- **公网安全**：CSRF、登录限流、可信代理 + `PUBLIC_URL` Secure Cookie、Purge 两阶段可恢复删除

详细验收标准见 [`docs/product-goal.md`](docs/product-goal.md#下一版本黄金旅程)，生产部署见 [`docs/production-deployment.md`](docs/production-deployment.md)。

## 快速开始

### 前置
- Go 1.25+
- ffmpeg（音频转码，适配 Groq Whisper 上传限制）
- Groq API key（https://console.groq.com/keys）

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

认领后注册永久关闭。公网部署配置见 [`docs/production-deployment.md`](docs/production-deployment.md)。

### Docker 部署（VPS 推荐）

已发布的多架构镜像（`linux/amd64`、`linux/arm64`）可从公开 GHCR 匿名拉取：

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

访问 `http://localhost:8080/register` 认领实例。`/app/data` 单一持久卷承载数据库、EvidenceAudio、临时文件与备份（ADR-0010）。

如需从源码构建并用 Compose 部署：

```bash
# .env 中填好 SESSION_SECRET 和 GROQ_API_KEY
docker compose up -d
```

容器已含 ffmpeg。SQLite、EvidenceAudio 与临时文件通过单一 volume（`cwp-data` → `/app/data`）持久化。

## 配置（环境变量）

| 变量 | 必填 | 说明 | 默认 |
|---|---|---|---|
| `SESSION_SECRET` | ✅ | 会话密钥，`openssl rand -hex 32` | — |
| `GROQ_API_KEY` | ✅ | Groq API key（主力 provider） | — |
| `OPENAI_API_KEY` | 否 | OpenAI key；仅按单次任务显式授权使用（ADR-0009） | — |
| `PORT` | 否 | 监听端口 | 8080 |
| `DATA_DIR` | 否 | 统一数据目录（DB/evidence/tmp/backups） | ./data |
| `PUBLIC_URL` | 否 | 公网 URL；决定 Secure Cookie 与 Markdown 链接 | http://localhost:8080 |
| `TRUSTED_PROXIES` | 否 | 受信任反向代理 CIDR，逗号分隔 | 空（只信直连） |

## Groq 真实约束

实测踩到的坑（详见 `docs/adr/0002-groq-real-constraints.md`）：

1. **音频文件 ≤ 25MB**：完整播客单集常超限。worker 下载后用 ffmpeg 转码为 16kHz/单声道，并按时长自适应降低码率，使上传保持在 22MiB 预算内；极长音频会明确要求后续分段转录。
2. **`json_schema` 仅 gpt-oss 支持，且 TPM 仅 8K**：分析用 `llama-3.3-70b` + `json_object`（所有模型支持，TPM 12K），靠 prompt + 容错解析兜底。
3. **Q&A 用 RAG 分块**：只把 top-5 相关片段喂 LLM，避免整集 transcript 撞 TPM。

## 架构

```
cmd/cloudwisepod/    入口
internal/
  config/            环境变量
  store/             SQLite + FTS5，多态 Source 级联删除
  auth/              argon2id 密码 + cookie session
  models/            领域类型
  provider/          Groq/OpenAI 双实现 + 中间层 + RAG 检索
  rss/               gofeed 解析 + SSRF 防护 + cron
  queue/             状态机 + 乐观锁幂等 + ffmpeg 转码 + 429 退避
  server/            html/template SSR + 原生 JS 播放器 + REST API
docs/adr/            架构决策记录
CONTEXT.md           领域词汇表
```

领域概念与决策见 [`CONTEXT.md`](CONTEXT.md) 和 [`docs/adr/`](docs/adr/)。当前 Go 架构与目标架构之间的差距见 [`docs/handoff.md`](docs/handoff.md)。

## 测试

```bash
go test ./...
```

测试覆盖：迁移（v0.1 fixture 升级、失败回滚重试）、一致性备份/恢复 E2E、Owner 认领与 CSRF/限流、EvidenceAudio 持久化与幂等、worker 崩溃恢复与防重复领取、Citation/金句逐字校验、EvalSet 自动校验、Markdown golden 测试、分段搜索、Q&A 拒答。

## 备份与恢复

```bash
./cloudwisepod backup /backup/cwp-2026-08-01.tar.gz
DATA_DIR=/new/instance ./cloudwisepod restore /backup/cwp-2026-08-01.tar.gz
```

备份包含一致性数据库快照 + EvidenceAudio + manifest（DB/证据 SHA256），不含 API key 或 Session secret。

## 产品定位

CloudWisePod 借鉴 Podwise 的播客处理体验，但不再以“完整复刻”为目标。它聚焦单 Owner 的可信证据链和个人知识沉淀，明确不建设多用户、SaaS、跨 Source RAG、语义搜索、Obsidian 插件或双向同步。
