# 真实语义检索质量评测

`scripts/semantic-quality-eval.py` 对既有索引和查询向量进行只读 Recall@10 比较。不准备向量、不创建任务、不调用供应商、不导入准入报告、不打开语义搜索。只需 Python 3 标准库。

## 准备真实输入

1. 在 `.env` 配置独立的 `LEARNING_EMBEDDING_BASE_URL`、`LEARNING_EMBEDDING_API_KEY`、`LEARNING_EMBEDDING_MODEL`、`LEARNING_EMBEDDING_DIMENSIONS` 并重启服务。密钥不要写进 manifest、命令参数或报告。
2. 在 `/search/settings` 明确选择允许索引的真实资料、登记价格并执行预检和建立索引。这些动作可能付费，脚本不会代为执行。
3. 人工编写恰好 40 条不同查询，包含 `original` 与 `rewrite` 两组，逐条标注相关知识对象的 `Key`。这些键是搜索结果的稳定对象身份，不是来源 ID。先固定标注再执行评测，避免根据检索结果倒填答案。查询非空、最长 200 字符，每条至少一个相关键。
4. 在设置页明确提交每条查询的 `evaluation_query` 并等待完成，取得真实查询向量。这也可能付费。索引、模型、维度与权限范围必须保持一致，变更后重新准备。

manifest 为 JSON，下面只是格式示例；实际需要 40 条人工标注，不能以此冒充真实评测集：

```json
{"queries": [
  {"id": "original-01", "group": "original", "query": "自己的查询", "relevant_keys": ["实际知识对象Key"]},
  {"id": "rewrite-01", "group": "rewrite", "query": "同义改写的查询", "relevant_keys": ["实际知识对象Key"]}
]}
```

## 运行和解释

通过环境变量 `CWP_SESSION_COOKIE` 提供已登录 Owner 的会话 cookie（`名称=值`），避免放在命令行参数。地址只接受本地 HTTP 或 HTTPS，不接受带凭证、路径、query 的 URL，不跟随重定向。

```sh
python3 scripts/semantic-quality-eval.py \
  --base-url http://127.0.0.1:8080 \
  --config-id 实际配置ID \
  --manifest /私有目录/queries.json \
  --output /私有目录/recall-report.json
```

`measured` 退出码为 0；缺查询向量等降级为 `incomplete`，网络失败、配置或索引身份变化为 `blocked`，两者退出码为 2。无效 manifest 在请求前拒绝；评测受阻仍保存部分结果，身份变化时不给出组级结果。输出原子替换且权限 0600，含对象键和查询 ID，不含查询正文、标题、摘要或 cookie。私有文件不要提交仓库。

每条 Recall@10 = 前十个返回对象与人工相关对象的交集数 / 人工相关对象数。组级指标是各查询的算术平均。该组任一查询缺有效 RRF 结果，hybrid 指标为 null；FTS 降级不能冒充语义成绩。首次、每条查询及末次读取必须保持相同服务端 quality identity；报告另保存 manifest SHA256。

报告始终写 `real_embeddings_verified=false`、`quality_gate_passed=false`、`cost=null`。只读接口不能证明向量来自真实模型，也不能补齐价格、延迟或人工评分。Owner 仍需核对真实模型执行记录、费用及完整要求，在设置页完成既有准入流程；脚本输出不是直接导入格式。

本地已接入并实测 Jina `jina-embeddings-v5-text-small`，配置和供应商用量识别通过验证；尚未建立真实资料索引及40查询人工标注集，因此没有执行真实 Recall 评测。三条示例文本的相似度不能替代 Recall 验收。模拟单测与实际 API 集成只验证计算、失效、降级及只读契约。

验证：`python3 -B scripts/semantic-quality-eval-test.py`；API 集成：`go test ./internal/server -run TestKnowledgeSemanticQualityRunnerReadonly -count=1`。
