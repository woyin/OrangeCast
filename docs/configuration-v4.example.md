# 第四轮配置示例

以下均为占位内容，无真实密钥。复制仓库 [`.env.example`](../.env.example) 后只在本机填写；不要提交 `.env`。进程环境优先于当前目录 `.env`。修改连接后重启，新任务冻结新配置，旧任务保留旧连接身份；重试前核对原任务而非直接换模型。

```dotenv
SESSION_SECRET=<openssl rand -hex 32 的输出>
GROQ_API_KEY=<你的转录密钥>
PORT=8080
DATA_DIR=./data
PUBLIC_URL=https://cwp.example.com
TRUSTED_PROXIES=<真实反向代理IP/CIDR>

# 自动知识文章：三项同时填写，基础地址含服务要求的 /v1 路径。
POD_BASE_URL=https://api.example.com/v1
POD_API_KEY=<独立文章密钥>
POD_MODEL=<文本模型>
# 留空回退 POD_MODEL。审校可指定不同模型，但连接相同。
POD_REVIEW_MODEL=
POD_DISCOVERY_MODEL=
POD_SELECTION_MODEL=
POD_WRITE_MODEL=
POD_LEARNING_REVIEW_MODEL=
POD_QUESTION_STUDY_MODEL=
POD_QUESTION_STUDY_REVIEW_MODEL=

# 独立语音连接：四项同时填写；全部留空沿用站内转录选择。
VOICE_ASR_PROVIDER=
VOICE_ASR_MODEL=
VOICE_ASR_BASE_URL=
VOICE_ASR_API_KEY=

# 独立语义连接：三项成组，默认关闭，配置不授权外发。
LEARNING_EMBEDDING_BASE_URL=
LEARNING_EMBEDDING_API_KEY=
LEARNING_EMBEDDING_MODEL=
# 1..2048；不支持 dimensions 参数的模型留空，由明确预检固定维度。
LEARNING_EMBEDDING_DIMENSIONS=
```

文章角色模型默认回退到 `POD_MODEL`。问题学习生成回退 `POD_MODEL`；其审校回退 `POD_REVIEW_MODEL`，再 `POD_MODEL`。`POD_*` 不改变旧 StudyChat 的既有 Provider 和学习模型，也不复用为语音或 embedding 端点。

独立语音支持的 Provider 及语音权限操作见[第三轮说明](personal-learning-v3.md)。embedding 的模型、维度和连接身份须与预检、价格表及索引匹配，不能只换字符串继续用旧向量。

费用和自动化不是环境变量默认开启：在网页模型价格表登记对应 Provider/模型价格，核对预算后再明确启动任务。文章自动生成默认关闭，开启后默认每天 1 篇、等待素材更新 30 分钟；语义搜索默认关闭且须通过质量准入；离线每台设备单独授权 24 小时。这些是应用设置，不能靠填入密钥视为授权。

实际配置是否完整由启动校验和实例设置页决定。本文编写未读取密钥值；协调验收只检查字段存在性，文本 POD 基础地址、密钥和模型已配置，独立 embedding 基础地址、密钥、模型和 dimensions 均缺失。自建真实文本调用收到 usage，但实际模型身份不匹配被拒绝，不能据此认定真实质量或费用结算通过。生产实际配置仍以运行实例校验为准。

## Jina 文本向量接入

已实测 `jina-embeddings-v5-text-small`、1024维与当前客户端兼容。将实际密钥仅写入本地或部署环境：

```dotenv
JINA_API_KEY=你的实际密钥
LEARNING_EMBEDDING_BASE_URL=https://api.jina.ai/v1
LEARNING_EMBEDDING_API_KEY="${JINA_API_KEY}"
LEARNING_EMBEDDING_MODEL=jina-embeddings-v5-text-small
LEARNING_EMBEDDING_DIMENSIONS=1024
```

`make serve` 通过 shell 加载 `.env`，支持上面的引用。直接运行二进制时需先导出这些环境变量；若部署工具不支持变量展开，须将 `LEARNING_EMBEDDING_API_KEY` 单独设置为实际密钥。仅有 `JINA_API_KEY` 不会自动配置模型路由。`.env` 不随 Git 推送，部署环境需单独设置。

官方 HTTPS `api.jina.ai` 端点仅返回 `usage.total_tokens` 时，客户端将其登记为供应商报告的输入用量；其他端点不推断这一语义。返回模型不匹配时不据此计价；维度或向量无效时仍保留已知调用用量。用量已知不代表现金费用已知，仍需登记该模型的准确价格。

配置连接不自动建立索引或开启语义搜索。重启后在 `/search/settings` 执行明确预检、价格登记和资料范围选择；索引、查询准备及质量准入沿用现有持久任务流程。不要混用不同模型的旧向量。真实调用记录见[Jina验证](superpowers/validation/jina-embedding/README.md)。
