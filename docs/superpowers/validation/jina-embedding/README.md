# Jina embedding 接入验证（2026-10-02）

用户授权处理已验证的 Jina API 配置及用量兼容。模型 `jina-embeddings-v5-text-small`，1024维，官方 `https://api.jina.ai/v1`。本地 `.env` 保留已有 `JINA_API_KEY`，通过 `LEARNING_EMBEDDING_API_KEY="${JINA_API_KEY}"` 引用；另补齐基础地址、模型、维度。完整配置检查发现本地没有 SESSION_SECRET，生成了随机本地值；未覆盖已有密钥。`.env` 和构建后二进制均被 Git 忽略，不推送凭据。

## 实现与实测

仅对 Jina 官方 HTTPS 主机，将响应单独的 `total_tokens` 解释为报告的输入用量。标准 `prompt_tokens`/`total_tokens` 两字段一致性检查保持。无用量仍未知；任意其他端点和仿冒后缀主机不作 total-only 推断。无效向量保留已知调用回执；返回模型不匹配标为 unknown/unverified，不套用请求模型的价格。

临时 Go 探针从实际 `.env` 加载完整 Config，经 Selector 与实际 EmbeddingClient 发起一个三文本请求。配置验证通过、模型身份正确、三组1024维向量、`usage_known=true`、`input_tokens=35`，退出0。相关/无关示例文本点积0.803/0.2705，耗时452ms。安全结果见 `live-probe.json`，无密钥、响应正文或向量原文。

这是实际连通性与用量协议验证，不是40查询Recall评测。未读取或发送播客/笔记，未写默认资料库、登记索引价格、建立索引或开启语义检索。未计算现金费用：35 tokens是供应商报告的用量，费用仍需要准确价格表。现有索引预检、外发范围、版本、持久任务和质量准入保持。

## 回归与交付

新增10组协议测试：total-only、已知零用量、标准两字段、缺失用量、其他端点与仿冒主机、负数/冲突字段拒绝、无效向量和错模型保留回执。Provider/Config全包race成功（31.712/1.809s），见 `provider-config-race.log`。覆盖率、lint、vet及构建日志同目录保存。

另核实上一次 GitHub Actions（提交7a64b98，run36996337149）失败：`TestMainServeStartsAndStops` 仅等待10秒，在共享Linux runner上完整迁移尚未就绪。其余包包括Store race1182.043s均通过，没有包级超时。将该测试的HTTP就绪轮询预算调整为60秒；不改变生产启动流程、关闭时限或CI整体测试门槛。修订后该启动测试race连续3次验证，日志见 `startup-race.log`。远端新运行状态单独记录，不能以本地成功冒充CI完成。

本地端口8080由已有SSH进程监听，未替换或停止该进程。未对远端运行服务注入 `.env` 或执行部署；Git推送只携带源码和无密钥文档。配置部署方式见[配置说明](../../../configuration-v4.example.md)。

最终本地门禁：`make cover-gate`、`make lint`、`go vet ./...`、`go build -o cloudwisepod ./cmd/cloudwisepod`、Provider/Config全包race及CLI启动专项race三次均EXIT0。启动测试仅调整时间预算，生产代码与覆盖率测量一致；专项race验证修改后的测试。未将这些结果称作本轮全库race或远端CI成功。
