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
