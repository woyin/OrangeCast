# CloudWisePod 生产部署

> 与 `docs/deployment.md`（历史 Cloudflare 文档，不再使用）不同，本文是当前 Go 版本的生产部署说明。

## 数据目录（ADR-0010）

所有持久数据位于单一 `DATA_DIR`：

```
DATA_DIR/
├── cloudwisepod.db     # SQLite 数据库
├── evidence/           # 持久 EvidenceAudio（播放/引用只依赖它）
├── tmp/                # 下载/转码中间产物
└── backups/            # 备份输出目录（CLI 写入）
```

默认 `DATA_DIR=./data`；容器内为 `/app/data`（单一持久卷）。

## 环境变量

| 变量 | 必填 | 说明 |
|---|---|---|
| `SESSION_SECRET` | ✅ | 会话密钥，`openssl rand -hex 32` |
| `GROQ_API_KEY` | ✅ | 默认零成本 Provider（ADR-0009） |
| `OPENAI_API_KEY` | 否 | 仅按单次任务显式授权时使用 |
| `DATA_DIR` | 否 | 统一数据目录（默认 `./data`） |
| `PORT` | 否 | 监听端口（默认 8080） |
| `PUBLIC_URL` | ✅（公网） | 公网 https URL；决定 Secure Cookie 与 Markdown Citation 链接 |
| `TRUSTED_PROXIES` | 否 | 受信任反向代理 CIDR，逗号分隔；仅这些来源的转发头被信任 |

## 反向代理

TLS 由 Caddy 或 Nginx 终止（ADR-0013：CloudWisePod 不管理证书）。

- Caddy：见 [`docs/deploy/Caddyfile.example`](deploy/Caddyfile.example)
- Nginx：见 [`docs/deploy/nginx.example`](deploy/nginx.example)

公网部署必须设置 `PUBLIC_URL=https://cwp.example.com`，并把代理地址加入 `TRUSTED_PROXIES`（否则登录限流按错误 IP 计，Secure Cookie 也不会启用）。

## Docker（单一持久卷）

```bash
# .env 填好 SESSION_SECRET / GROQ_API_KEY / PUBLIC_URL / TRUSTED_PROXIES
docker compose up -d
```

镜像/容器/卷均见 `docker-compose.yml`；`cwp-data` 一个卷承载全部数据。

## 备份与恢复（CLI）

```bash
# 备份（一致性 SQLite 快照 + EvidenceAudio + manifest；不含 API key/Session secret）
./cloudwisepod backup /backup/cwp-2026-08-01.tar.gz

# 恢复到全新目录（默认目标必须为空）
DATA_DIR=/new/instance ./cloudwisepod restore /backup/cwp-2026-08-01.tar.gz

# 目标已有数据时显式覆盖
DATA_DIR=/new/instance ./cloudwisepod restore /backup/cwp-2026-08-01.tar.gz --force
```

恢复会校验 manifest 格式版本、数据库 SHA256 与每个证据文件的 SHA256；校验失败不会污染目标目录。

## 首次启动

1. 启动服务后访问 `/register` 认领唯一 Owner（ADR-0003）。
2. 添加 RSS / 上传音频 → 选择处理 → worker 自动完成转录与分析（ADR-0006）。
3. 处理后页面可播放 EvidenceAudio、搜索跳转、下载带 Citation 的 Markdown。
