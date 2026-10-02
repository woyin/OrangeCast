# CloudWisePod 生产部署

> 与 `docs/deployment.md`（历史 Cloudflare 文档，不再使用）不同，本文是当前 Go 版本的生产部署说明。

## 数据目录（ADR-0010）

所有持久数据位于单一 `DATA_DIR`：

```
DATA_DIR/
├── cloudwisepod.db     # SQLite 数据库
├── evidence/           # 持久 EvidenceAudio（播放/引用只依赖它）
├── voice-notes/        # 私有录音（目录0700，文件0600，鉴权下载）
├── tmp/                # 下载/转码中间产物
└── backups/            # 备份输出目录（CLI 写入）
```

默认 `DATA_DIR=./data`；容器内为 `/app/data`（单一持久卷）。

## 环境变量

| 变量 | 必填 | 说明 |
|---|---|---|
| `SESSION_SECRET` | ✅ | 会话密钥，`openssl rand -hex 32` |
| `GROQ_API_KEY` | ✅ | 默认转录 Provider（ADR-0009） |
| `OPENAI_API_KEY` | 否 | 仅按单次任务显式授权时使用 |
| `POD_BASE_URL` / `POD_API_KEY` / `POD_MODEL` | 否 | 自动文章文本连接，三项齐全才可用；阶段模型配置见[README](../README.md#配置) |
| `VOICE_ASR_PROVIDER` / `VOICE_ASR_MODEL` | 否 | groq/openai音频转录选择；缺省沿用站内设置 |
| `VOICE_ASR_BASE_URL` / `VOICE_ASR_API_KEY` | 否 | 独立音频连接须成对配置，POD文本端点不能替代 |
| `DATA_DIR` | 否 | 统一数据目录（默认 `./data`） |
| `PORT` | 否 | 监听端口（默认 8080） |
| `PUBLIC_URL` | ✅（公网） | 公网 https URL；决定 Secure Cookie 与 Markdown Citation 链接 |
| `TRUSTED_PROXIES` | 否 | 受信任反向代理 CIDR，逗号分隔；仅这些来源的转发头被信任 |
| `KOKORO_BINARY` | 否 | 解说 TTS 可执行文件（默认 PATH 查找 `kokoro`；推荐 `scripts/kokoro` 包装，见下节） |
| `KOKORO_VOICE` | 否 | 默认音色（默认 `af_heart`；中文 `zf_xiaobei` / `zm_yunyang` 等） |
| `KOKORO_MODEL` / `KOKORO_MODEL_DIR` | 否 | 权重路径：完整 onnx 文件 / 目录（取其中 `kokoro-v1.0.onnx` + `voices-v1.0.bin`） |
| `KOKORO_LANGUAGE` | 否 | `en`（默认）/`zh`；仅用于预检一致性提示，实际语言按音色前缀自动推断 |
| `KOKORO_TIMEOUT_SECONDS` | 否 | 单次合成超时（默认 120） |

## 解说音轨（Narration / Kokoro TTS，D03）

解说音轨（ADR-0019）由自托管 Kokoro TTS 合成，独立于 Groq/OpenAI。引擎未安装时
`Available()==false`，worker 自动跳过合成——原音播放与全部学习功能不受影响，可随时补装。

已验证发行版：**kokoro-onnx 1.x**（Apache-2.0，ONNX Runtime CPU 推理）+ **Kokoro-82M v1.0**
权重（`kokoro-v1.0.onnx` ≈325MB + `voices-v1.0.bin` ≈28MB）。kokoro-onnx 是 Python 库而非 CLI，
仓库提供 `scripts/kokoro` + `scripts/kokoro_cli.py` 把它包装成 Provider 期望的
`--text/--voice/--output` 风格命令（语言按音色前缀自动推断：`zf_`/`zm_`→中文 cmn）。

安装（macOS/Linux，约 5 分钟）：

```sh
# 1. Python 运行时（uv 任选 Python 3.12；无需系统 pip 装包）
uv venv --python 3.12 ~/.local/share/cloudwisepod/kokoro/venv
uv pip install --python ~/.local/share/cloudwisepod/kokoro/venv/bin/python kokoro-onnx soundfile

# 2. 权重（kokoro-onnx 官方 release 资产）
cd ~/.local/share/cloudwisepod/kokoro
curl -LO https://github.com/thewh1teagle/kokoro-onnx/releases/download/model-files-v1.1/kokoro-v1.0.onnx
curl -LO https://github.com/thewh1teagle/kokoro-onnx/releases/download/model-files-v1.1/voices-v1.0.bin
```

配置与验证：

```sh
# .env / 环境变量
KOKORO_BINARY=scripts/kokoro      # 相对仓库根或绝对路径
KOKORO_VOICE=zf_xiaobei           # 中文女声；英文可用 af_heart
KOKORO_LANGUAGE=zh

# 预检（不落文件）+ 短句试听（经生产 Adapter 全链路合成）
cloudwisepod tts-check
cloudwisepod tts-check --output /tmp/audition.wav && ffprobe /tmp/audition.wav
```

人耳试听：`/tmp/audition.wav` 即默认样本（专名、数字、停顿、中英混合，含"AI 解说："前缀）。
注意 kokoro-onnx 的中文 G2P 走 espeak-ng（cmn），韵律偏机器味；如需更自然中文可后续评估
Kokoro v1.1-zh 权重或官方 misaki[zh] 路线（接口不变，只换权重与 G2P）。

## 反向代理

TLS 由 Caddy 或 Nginx 终止（ADR-0013：CloudWisePod 不管理证书）。

- Caddy：见 [`docs/deploy/Caddyfile.example`](deploy/Caddyfile.example)
- Nginx：见 [`docs/deploy/nginx.example`](deploy/nginx.example)

公网部署必须设置 `PUBLIC_URL=https://cwp.example.com`，并把代理地址加入 `TRUSTED_PROXIES`（否则登录限流按错误 IP 计，Secure Cookie 也不会启用）。

## 手机录音与个人任务

手机通过实际HTTPS地址访问并授予麦克风权限；局域网HTTP地址不能当作已验收的录音部署。需要浏览器提供MediaRecorder，以及服务端ffmpeg/ffprobe。设置`PUBLIC_URL`并不能代替TLS反向代理。

原音、私人录音与生成的解说音轨分别保存。`voice-notes`不映射为公开静态目录，录音下载保留登录校验；私有文件纳入数据卷。环境配置改变后重启，旧任务冻结的模型或端点不匹配时需处理阻断，不会隐式改用别的连接。

新方向发现、文章更新和每周题目生成的自动开关独立且默认关闭。Owner在界面明确启用；暂停类别/方向仍在重启后保留。任务面板说明已知响应、未知结果、预算和恢复动作。停止运行任务不能保证供应商停止计费。

文本价格使用确切provider/model；音频价格使用每分钟美元分。有月预算而无确切音频估价时阻止调用；未知实际费用继续显示未知，不当作零。手机真机记录模板见[综合验收报告](acceptance/2026-10-personal-learning-v3.md)。

## Docker（单一持久卷）

```bash
# .env 填好 SESSION_SECRET / GROQ_API_KEY / PUBLIC_URL / TRUSTED_PROXIES
docker compose up -d
```

镜像/容器/卷均见 `docker-compose.yml`；`cwp-data` 一个卷承载全部数据。

## 备份与恢复（CLI）

```bash
# 备份（一致性 SQLite 快照 + EvidenceAudio + 明确保留的私有录音 + manifest）
./cloudwisepod backup /backup/cwp-2026-08-01.tar.gz

# 恢复到全新目录（默认目标必须为空）
DATA_DIR=/new/instance ./cloudwisepod restore /backup/cwp-2026-08-01.tar.gz

# 目标已有数据时显式覆盖
DATA_DIR=/new/instance ./cloudwisepod restore /backup/cwp-2026-08-01.tar.gz --force
```

当前备份格式为v2，增加已保存且明确保留的私有录音，继续恢复v1。清单校验路径、长度及SHA256；恢复录音文件为0600。临时草稿录音不进入备份，草稿文字仍随数据库保存；缺失录音不制造可播放状态。校验失败不会污染目标目录。

备份数据库包含settings中的Provider API key，因此备份包是敏感文件；manifest不包含密钥，不能据此认为整个包不含密钥。使用私有权限及加密通道传输。SESSION_SECRET由新实例另行配置或生成。

升级前备份并先在独立目录恢复验证，再以新程序升级该副本。需要回退时恢复升级前完整备份及对应程序，旧程序不能直接读取0069结构。来源彻底删除会清理队列/问题/回顾关系并安排私人录音清理，已经发生的用量仍保留。

## 首次启动

1. 启动服务后访问 `/register` 认领唯一 Owner（ADR-0003）。
2. 添加 RSS / 上传音频 → 选择处理 → worker 自动完成转录与分析（ADR-0006）。
3. 处理后页面可播放 EvidenceAudio、搜索跳转、下载带 Citation 的 Markdown。

## 第四轮配置与离线运维

配置及使用说明见[第四轮配置示例](configuration-v4.example.md)和[第四轮使用说明](personal-learning-v4.md)。这些说明不代表真实模型质量或手机体验已通过。


仓库 Compose 从源码构建，但其 `environment` 当前只传入会话、Groq、OpenAI、公网和代理基础项。仅在宿主 `.env` 填入 `POD_*`、`VOICE_ASR_*` 或 `LEARNING_EMBEDDING_*` 不会自动把这些项送入容器。使用私有 Compose override 显式传入所需变量，或运行容器时用 `--env-file`；不要把密钥写入仓库或镜像。

```sh
docker build -t cloudwisepod:local .
docker run -d --name cloudwisepod --restart unless-stopped \
  --env-file .env -e DATA_DIR=/app/data \
  -p 127.0.0.1:8080:8080 -v "$(pwd)/data:/app/data" \
  cloudwisepod:local
```

检查反向代理实际来源，设置对应 `TRUSTED_PROXIES`，不要信任全部公网。`PUBLIC_URL` 必须是实际 HTTPS URL，影响 Secure Cookie 和来源链接。参考 [Caddy](deploy/Caddyfile.example) 或 [Nginx](deploy/nginx.example) 配置，TLS 在代理终止。代理地址、证书和防火墙须按实际环境配置，不把示例域名当成已部署地址。

手机录音及离线 Service Worker 需要安全上下文；HTTPS 部署后仍需真实设备验证麦克风、后台播放、缓存配额及登录清理。scope `/` 只决定 SW 控制路径，动态认证页面不应被缓存。离线限制和撤回延迟见[使用说明](personal-learning-v4.md#离线听与学)。


### 第四轮升级与任务恢复


```sh
./cloudwisepod backup /private/path/cwp-v2.tar.gz
DATA_DIR=/new/instance ./cloudwisepod restore /private/path/cwp-v2.tar.gz
```

使用空目标目录；已有数据仅在明确接受覆盖时加 `--force`。先在隔离目录恢复，核对 manifest、文件校验、登录、来源和任务记录再切换服务。v2 兼容旧 v1，备份包括明确保留的私人录音；未保存录音、Narration、临时导出与浏览器离线副本不能据此恢复。

数据库设置可能包含模型密钥，备份包按敏感文件私有保存及加密传输，不作公开发布附件。恢复/重启更换离线 epoch，设备回网先核对并重新授权。已发生模型用量与未知远端状态保留，重启不意味着免费重发。不能在恢复后让过期、撤回、停止或已知响应任务重新调用付费模型。

## 运行核对与未验项

验证服务能登录，数据目录写入正常，启动无迁移错误；在 `/automation` 查看任务恢复和费用状态。不要为健康检查自动触发生成、ASR 或 embedding。模型连接和费用另行明确测试，记录实际调用及结果。质量 CLI 的 `--run` 同样经过持久阶段预算准入、派发 CAS 锁及月度 receipt 幂等记账；已知响应恢复零新调用，未知不得伪记零或自动重发。数据库迁移包含 0088 理解关系和 0089 doc_key 索引；索引优化后仍未满足语义冷性能门槛，保持 FTS 可用、语义关闭。

仓库工程测试与部署验收分别记录。当前本说明没有执行公网部署、升级已有生产数据库、真实 Provider 质量评测或实体手机测试；按[验证记录](superpowers/plans/2026-10-01-personal-learning-and-knowledge-articles-v4-validation.md)和[手机验收表](acceptance/2026-10-02-v4-offline-mobile.md)补充实际证据后才能声明相应通过。
