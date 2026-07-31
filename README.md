# CloudWisePod

自用版 Podwise 复刻：把播客音频变成结构化、可检索、带引用的知识库。RSS 订阅 → Groq 转录 → AI 知识卡片 → 播放器时间戳联动 + Q&A 带引用。

部署在 VPS（Go 单二进制 + Docker），AI 主力用 Groq（零成本），OpenAI 作可切换兜底。

## 功能

- **RSS 订阅**：订阅播客 feed，30 分钟 cron 自动拉取新单集（含 SSRF 防护）
- **音频上传**：手动上传 mp3/m4a/wav
- **AI 转录**：Groq `whisper-large-v3`，带逐句时间戳
- **知识卡片**：Groq `llama-3.3-70b` 生成标题、摘要、章节（带时间戳）、要点、金句、标签
- **播放器联动**：内嵌播放器 + 转录稿/章节/金句三层时间戳联动（点句跳转、播放高亮、双向跳转）
- **Q&A 带引用**：RAG 检索相关片段回答，附可点击的时间戳引用（点击跳转播放）
- **全库搜索**：SQLite FTS5 全文检索（覆盖转录与分析）
- **Markdown 导出**：Obsidian frontmatter 格式
- **运行时切换 provider**：后台一键 Groq ↔ OpenAI，对新任务即时生效

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

# 3. 访问 http://localhost:8080/register 注册首个用户
```

### Docker 部署（VPS 推荐）

已发布的多架构镜像（`linux/amd64`、`linux/arm64`）可从公开 GHCR 匿名拉取：

```bash
docker pull ghcr.io/woyin/orangecast:latest
docker run -d --name orangecast --restart unless-stopped \
  -p 8080:8080 \
  -e SESSION_SECRET="$(openssl rand -hex 32)" \
  -e GROQ_API_KEY="你的 Groq API Key" \
  -v orangecast-data:/app/data \
  -v orangecast-tmp:/app/tmp \
  ghcr.io/woyin/orangecast:latest
```

访问 `http://localhost:8080/register` 注册首个用户。发布版本 `v0.1.0` 对应镜像标签 `0.1.0`、`0.1` 和 `latest`；后续版本也遵循相同规则。

如需从源码构建并用 Compose 部署：

```bash
# .env 中填好 SESSION_SECRET 和 GROQ_API_KEY
docker compose up -d
```

容器已含 ffmpeg。SQLite 数据库与上传音频通过 volume 持久化（`cwp-data`、`cwp-tmp`）。

## 配置（环境变量）

| 变量 | 必填 | 说明 | 默认 |
|---|---|---|---|
| `SESSION_SECRET` | ✅ | 会话密钥，`openssl rand -hex 32` | — |
| `GROQ_API_KEY` | ✅ | Groq API key（主力 provider） | — |
| `OPENAI_API_KEY` | 否 | OpenAI key（兜底，留空则无法切换到 openai） | — |
| `PORT` | 否 | 监听端口 | 8080 |
| `DB_PATH` | 否 | SQLite 路径 | ./data/cloudwisepod.db |
| `TEMP_DIR` | 否 | 音频临时落盘目录 | 系统 temp |
| `BASE_URL` | 否 | 站点根 URL | http://localhost:8080 |

## Groq 真实约束

实测踩到的坑（详见 `docs/adr/0002-groq-real-constraints.md`）：

1. **音频文件 ≤ 25MB**：完整播客单集（30+ 分钟）常超限。worker 下载后用 ffmpeg 转码为 64kbps/16kHz/单声道再上传。
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

领域概念与决策见 `CONTEXT.md` 和 `docs/adr/`。

## 测试

```bash
go test ./...
```

51 个单元测试，覆盖：级联删除、状态机幂等、argon2id、JSON 容错解析、RAG 分词检索、SSRF 防护、HTTP 路由集成。

## 与 Podwise 的差异

砍掉了多语言翻译、Notion/Readwise 集成、思维导图、CLI/MCP（自用非必需）。核心体验（订阅 → 转录 → 知识卡片 → 联动听 → Q&A 引用）完整复刻。
