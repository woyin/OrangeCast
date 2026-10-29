# 部署手册（Docker / Compose / 裸二进制）

本文是可直接照做的部署操作手册。解说音轨（Kokoro TTS）安装细节、验收记录等专项内容见
[`docs/production-deployment.md`](../production-deployment.md)；产品目标与边界见
[`docs/product-goal.md`](../product-goal.md)。

当前发布版本：**v0.2.3**（镜像 `ghcr.io/woyin/orangecast:0.2.3`）。

> v0.2.1 及更早的镜像在容器内执行 `restore` 会报 `invalid cross-device link`（解包临时目录
> 与数据卷不在同一设备）。恢复路径请使用 **v0.2.2 或更新版本**。

## 1. 前置条件

| 项目 | 要求 | 说明 |
|---|---|---|
| 主机 | 1 vCPU / 1 GB 内存 / 10 GB 磁盘起 | 音频与数据库在同一数据目录，按订阅量预留 |
| Docker | 24+ 与 Compose v2（方式 B） | 方式 A 只需 `docker` |
| 域名与 TLS | 公网访问必需 | 由 Caddy/Nginx 终止 TLS，应用不管证书 |
| ffmpeg | 镜像已内置 | 裸二进制部署需自行安装 `ffmpeg` 与 `ffprobe`（转码、片段导出） |
| 模型 key | 转录需要 `GROQ_API_KEY` | 只要认领后能处理单集就需要；译文、练习问题等复用“分析角色”配置 |

> 实例是**单 Owner**：第一个访问 `/register` 的人认领后，注册永久关闭。公网暴露前先认领，
> 或先经内网 / SSH 隧道完成认领。

## 2. 方式 A：官方镜像（推荐）

```bash
docker pull ghcr.io/woyin/orangecast:0.2.3

docker run -d --name orangecast --restart unless-stopped \
  -p 127.0.0.1:8080:8080 \
  -e SESSION_SECRET="$(openssl rand -hex 32)" \
  -e GROQ_API_KEY="你的 Groq key" \
  -e PUBLIC_URL="https://cwp.example.com" \
  -e TRUSTED_PROXIES="127.0.0.1/32" \
  -v cloudwisepod-data:/app/data \
  ghcr.io/woyin/orangecast:0.2.3
```

要点：

- `SESSION_SECRET` 必填且**必须固定**：换掉它会让所有登录会话失效（数据不受影响）。
- 端口只绑 `127.0.0.1`，由反向代理对外；若要直接暴露，改成 `-p 8080:8080` 并接受无 TLS 的风险。
- 容器内固定 `DATA_DIR=/app/data`，卷名可自取；**不要**让两个容器同时挂同一个卷。

标签含义：`:0.2.3`（精确版本，推荐）、`:0.2`（同小版本最新补丁）、`:latest`（最新发布）。

## 3. 方式 B：Compose 从源码构建

```bash
git clone git@github.com:woyin/OrangeCast.git && cd OrangeCast
cp .env.example .env    # 至少填 SESSION_SECRET、GROQ_API_KEY、PUBLIC_URL
docker compose up -d --build
docker compose logs -f --tail=50
```

`docker-compose.yml` 把宿主 `./data` 挂到 `/app/data`。它与 `make serve`（裸二进制）共用同一实例，
**二者严禁同时运行**（SQLite 写锁与 EvidenceAudio 写入冲突）。

## 4. 方式 C：裸二进制

```bash
# 依赖：Go 1.25+、ffmpeg + ffprobe（Makefile 没有 build 目标，直接编译）
go build -o orangecast ./cmd/orangecast

export SESSION_SECRET="$(openssl rand -hex 32)"
export GROQ_API_KEY="你的 Groq key"
export PUBLIC_URL="https://cwp.example.com"
./orangecast            # 默认子命令 serve
```

跨机器迁移用 `scripts/remote-setup.sh`：编译 → 生成或复用 `DATA_DIR/.session-secret` → 可选恢复备份
（以该密钥作为环境变量传入）→ 启动服务。已有数据库默认拒绝覆盖，需 `--force-restore`；脚本本身不读 `.env`。

## 5. 环境变量

必填：

| 变量 | 说明 |
|---|---|
| `SESSION_SECRET` | 会话密钥，`openssl rand -hex 32`；固定不变。**唯一被启动校验强制要求**的变量 |
| `GROQ_API_KEY` | 默认转录 Provider；缺失时转录任务会显式失败 |
| `PUBLIC_URL` | 公网地址；https 时启用 Secure Cookie，并决定 Markdown 引用链接 |

常用可选：

| 变量 | 默认 | 说明 |
|---|---|---|
| `PORT` | `8080` | 监听端口 |
| `DATA_DIR` | `./data` | 唯一数据根目录（容器内 `/app/data`） |
| `TRUSTED_PROXIES` | 空 | 受信任反代 CIDR，逗号分隔；只信这些来源的转发头 |
| `OPENAI_API_KEY` | 空 | 备用 Provider，仅按单次任务显式授权时使用 |
| `CLIP_DIR` | `$DATA_DIR/clips` | 高光片段音频缓存（按源音频 SHA256，可重建） |
| `NARRATION_DIR` | `$DATA_DIR/narrations` | AI 解说音轨目录（不进备份） |

功能型配置（不填则该功能不可用或自动跳过，不影响转录与阅读）：

| 功能 | 变量 |
|---|---|
| 自动知识文章 | `POD_BASE_URL` / `POD_API_KEY` / `POD_MODEL`（三项齐全）及 `POD_*_MODEL` 分阶段覆盖 |
| AI 解说音轨 | `KOKORO_BINARY` / `KOKORO_VOICE` / `KOKORO_MODEL`（或 `_MODEL_DIR`） |
| 语音笔记独立 ASR | `VOICE_ASR_PROVIDER` / `VOICE_ASR_MODEL`（或成对的 `_BASE_URL` / `_API_KEY`） |
| 语义检索与重排 | `LEARNING_EMBEDDING_*`、`LEARNING_RERANK_*`（成组配置，默认关闭） |

完整清单与默认值见 [`README.md` 配置表](../../README.md#配置) 与 [`.env.example`](../../.env.example)。

## 6. 数据目录

```text
DATA_DIR/
├── cloudwisepod.db     数据库（唯一事实源：证据、产物、笔记、任务、费用）
├── evidence/           持久 EvidenceAudio（引用与播放只依赖它，必须备份）
├── voice-notes/        私有录音（0700，不进常规导出）
├── clips/              高光片段缓存（可重建，不进备份）
├── narrations/         AI 解说音轨（可重建，不进备份）
├── tmp/                下载与转码中间产物（可清空）
└── backups/            backup 命令输出目录
```

判断标准很简单：**可重建的（clips、narrations、tmp）不必备份；evidence/ 与数据库必须备份**。

## 7. 首次部署核对清单

```bash
# 1) 进程活着，登录页可访问（本实例没有独立健康检查端点，用 /login 探测）
curl -fsS -o /dev/null -w '%{http_code}\n' http://127.0.0.1:8080/login   # 期望 200

# 2) 镜像内 ffmpeg 可用（片段导出与转码依赖它）
docker exec orangecast sh -c 'command -v ffmpeg && ffmpeg -version | head -1'
```

逐项确认：

- [ ] 浏览器打开 `PUBLIC_URL`，`/register` 认领 Owner；认领后再访问 `/register` 应被拒绝或跳转登录。
- [ ] HTTPS 下会话 Cookie 带 `Secure`；若登录后反复跳回登录页，通常 `PUBLIC_URL` 仍是 http。
- [ ] `/settings` 填入 Provider key 并保存；`/automation`（任务与成本）能看到预算与用量。
- [ ] 添加播客并处理一集：转录 → 知识卡片 → 搜索命中 → 播放器可跳到时间点。
- [ ] 单集页有“对齐译文 / 结构导图 / 生成练习问题 / 高光片段”四个入口；片段下载返回音频文件。
- [ ] `docker restart orangecast` 后任务自动恢复，登录状态与数据仍在。

## 8. 升级

```bash
# 先备份（见第 9 节），再升级
docker pull ghcr.io/woyin/orangecast:0.2.3
docker rm -f orangecast
docker run -d ...（与首次相同的参数，挂同一个卷）
```

- 启动时自动应用迁移（`schema_migrations` 当前最高版本 **94**），旧数据原地兼容。
- 一致性备份只在破坏性迁移（0002 移除 `user_id`）前自动生成；0093/0094 属增量迁移，**升级前请自行备份**。
- 回滚到旧镜像通常可行（迁移为增量式）；若某次迁移不可逆，日志会明确提示，请用备份包回滚。
- 升级后核对：`/login` 200、能登录、旧单集可播放、`/automation` 没有堆积的失败任务。

## 9. 备份与恢复

```bash
# 备份（写入数据卷，随后复制到异地）
# 不要在实例刚启动、迁移仍在进行时备份；先确认 /login 返回 200 并稳定几秒。
docker exec orangecast /app/orangecast backup /app/data/backups/cwp-$(date +%Y%m%d-%H%M%S).tar.gz

# 恢复：必须停掉正在运行的实例（SQLite 不能被两个进程同时写）
docker stop orangecast
docker run --rm -v cloudwisepod-data:/app/data \
  -e SESSION_SECRET=restore-only-not-used \
  ghcr.io/woyin/orangecast:0.2.3 \
  restore /app/data/backups/cwp-20261028-120000.tar.gz --force
docker start orangecast
```

说明：镜像的 entrypoint 就是二进制，`restore` 直接跟在镜像名后即可；恢复也走配置校验，
所以 `SESSION_SECRET` 仍须非空（用任意占位值即可，不会写进仓库）。`--force` 只在覆盖已有数据时
使用；恢复到全新卷时可省略。建议先恢复到临时卷验证，再切正式卷。

恢复会校验数据库与音频的长度、SHA256 与清单路径；目标已有数据时拒绝覆盖，需显式 `--force`。

**安全提醒**：备份包内的 `settings` 表含 Provider key 明文。备份包属敏感文件，只经加密通道传输，
用完即删，不要放网盘或聊天工具。

## 10. 反向代理与安全

- 仓库提供 [`Caddyfile.example`](Caddyfile.example) 与 [`nginx.example`](nginx.example)，按实际域名修改。
- 代理在宿主机时，`TRUSTED_PROXIES` 填代理来源（如 `127.0.0.1/32`、Docker 网桥 `172.17.0.1/32`）；
  只有这些来源的 `X-Forwarded-*` 会被信任，避免伪造 IP 绕过登录限流。
- Nginx 需放宽上传限制（`client_max_body_size 500m`）与超时（`proxy_read_timeout 900s`）。
- 已内置：CSRF、登录限流、Secure Cookie、RSS 与音频抓取的 SSRF 防护（逐跳重定向校验 + 私网拦截）。
- 不要暴露到公网后再“补”认领：未认领实例任何人都能成为 Owner。

## 11. 常见问题

| 现象 | 原因 | 处理 |
|---|---|---|
| 启动即退出，日志 `SESSION_SECRET 必须设置` | 缺会话密钥 | 设置固定 `SESSION_SECRET` 后重启 |
| 登录成功又跳回登录页 | `PUBLIC_URL` 与实际访问协议不一致 | 修正 `PUBLIC_URL`，清 Cookie 重试 |
| 片段下载 500，日志含 ffmpeg 报错 | 裸二进制未装 ffmpeg | 安装 `ffmpeg` 与 `ffprobe`；镜像已内置 |
| 上传音频 413 | 反代请求体上限 | 提高 `client_max_body_size` |
| 处理任务长期 `queued` | worker 未启动或容器反复重启 | 看容器日志；`/progress` 与 `/automation` 有失败原因 |
| 译文/练习问题失败“价格未登记” | 配了月预算但没登记模型价格 | 在该 Provider 的价格表登记，或先清空月预算 |
| 重启后任务重新执行 | 设计如此（租约恢复、至少一次执行） | 幂等任务不会重复计费，可在费用页核对 |
| `backup` 报“迁移 00xx 失败：table … already exists” | 正好与启动迁移并发 | 等启动完成后重试；升级前先备份再替换镜像 |
| `restore` 报 `invalid cross-device link` | 旧镜像把解包临时目录放在 `/tmp` | 升级到 v0.2.2+ 再恢复 |

## 12. 本版本已验证的内容

- CI 通过：编译、`go vet`、`go test ./...`（含 store/queue/server 的 `-race`）、`make cover-gate`、`make lint`。
- 真实进程冒烟：认领 → 登录 → `/practice`、`/podcasts/opml`、`/dashboard`、`/settings` 均 200。
- 旧实例升级：用上一版本二进制建库（迁移 92），新二进制启动后升到 94，Owner 与数据保留。
- 片段切出：真实 ffmpeg 生成音频，切出时长等于引用区间，并验证缓存命中。
- 容器内完整恢复回路（v0.2.2 起）：拉起 → `backup` → 停机 → `restore … --force` → 重启 → `/login` 200，且不残留 `.restore-*` 目录。
- 界面（v0.2.3）：顶部导航改为分组菜单，单集页为完整播放器（音量/缓冲/状态）且处理进度自动轮询并显示下一步。

未验证、需真实条件：真实模型下的译文与练习问题质量、真实公网 RSS 的 OPML 导入、手机端体验。
