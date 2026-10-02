# 语义检索性能优化（2026-10-02）

基线：2e090cf；工作树开始干净。本轮只改检索适配器、候选SQL与精确排名，不改模型、维数、窗口容量、供应商调用、权限/来源失效或准入阈值。没有读取.env/默认资料库、没有embedding付费调用、没有开启实际语义检索。

## 实现

1. 成功hybrid不再先加载用户FTS页又加载100候选；降级才按原查询读取FTS，保留分页/摘要/总数/错误。
2. `matched AS MATERIALIZED`复用一次授权匹配扫描，计数与排序共享身份/分数；选出100候选后才读取展示正文。原知识范围、归档、provider谓词保持，投影复用同一解码器。
3. 矩阵加载时分配固定文档slot，每次查询只分配分数数组。全部窗口逐维float64累计点积保持原顺序；按每文档最大分数聚合，精确200项heap替代全库排序，score/key平分排序不变，没有ANN/降维/提前截断窗口。

## 对照方法与结果

Apple M4/10CPU，Darwin arm64，Go1.27.1。原BenchmarkKnowledgeHybridMaximumDimensions夹具未改，2048维、10000/50000完整窗口；cold每次清应用矩阵缓存，warm复用，均非清OS页缓存。每个case20样本，nearest-rank第19值为p95。8个独立进程按10k cold/warm、50k cold/warm各自before→after顺序运行，未同时跑全库gate，其他系统负载没有停；进程exit0和PASS仅表示测量完成，不等于性能/Recall准入通过。

编译不计入计时。before binary SHA256 `283d923c40ce82b681cc14834c3b895d8919eb00fb91a35c882a704ce541ddf0`，after `3dac7284b277bf579677719fa2283585f742b2492f25f59ec3f6f8b4e1109d05`。所有原始stdout/rusage在本目录，macOS ru_maxrss按bytes/1048576换算；它含造库/calibration全过程，不是矩阵专属内存。Go heap不是RSS。

| 窗口 | 缓存 | p95 ms（前→后） | p95减少 | 分配MiB/op（前→后） | 进程峰值RSS MiB（前→后） |
| --- | --- | --- | --- | --- | --- |
| 10000 | cold | 161.60 → 124.80 | 22.8% | 160.91 → 161.32 | 191.91 → 193.27 |
| 10000 | warm | 91.33 → 53.19 | 41.8% | 1.99 → 0.91 | 148.91 → 139.20 |
| 50000 | cold | 910.20 → 690.60 | 24.1% | 799.55 → 803.68 | 836.81 → 854.12 |
| 50000 | warm | 470.20 → 251.60 | 46.5% | 5.83 → 1.22 | 575.78 → 541.53 |

10k冷124.8ms低于150ms，50k冷690.6ms仍高于500ms；后者没有写成通过。50k热每查询分配由5.83MiB降到1.22MiB，约79.1%；冷分配略增（文档slot元数据），冷RSS836.81→854.12MiB约+2.1%。本轮延迟/热分配改进有相同规模样本支持，单轮调度噪声仍存在，不宣称严格统计因果区间。真实40查询relevance/Recall与模型/费用报告尚缺，实际语义继续关闭、FTS可用。

## 诊断与未采纳实验

当前CPU采样将主要成本定位到SQLite读页，原warm profile检索SQL子树6.85s，向量点积2.05s（profile含造库，不能把其全程百分比当单次查询分解）。FTS EXPLAIN显示虚表scan→文档rowid查找→临时排序；新SQL将匹配身份/score复用，权限没有被移到选取之后。

临时benchmark实验已经撤回：32MiB page cache冷p95941.7ms/RSS约920MiB，没有可用收益；512MiB mmap冷p95565.9ms，但RSS约1299MiB，故未默认启用。SQLite官方描述[映射I/O的机制与限制](https://www.sqlite.org/mmap.html)，本轮没有改Store.Open/cache/mmap设置，也没有unsafe指针/跨SQL缓冲区别名。仍需后续优化大规模冷载入，不以增加400MiB常驻映射换取一个达标标签。

## 行为验证

`TestKnowledgeSemanticPoolPreservesLexicalProjection`：120条资料，原SearchKnowledge与新pool逐字段一致（词项、空/无结果、kind/source、时间、主题、external谓词、100截止）。`TestKnowledgeSemanticLazyFallbackPreservesPagination`比较降级与原FTS分页/错误。`TestKnowledgeSemanticExactTopMatchesFullSort`：407文档/多窗口/负分/平分/反向键，exact score/revision/key结果和旧完整排序一致，缓存未被查询写入，取消/无效slot/维数拒绝。原检索/配置/撤权/理解/缺口全组相关race与全库coverage/lint/vet/build结果另落下方门禁日志。

## 复验命令

```sh
go test -c ./internal/store -o /tmp/cwp-semantic-final.test
/usr/bin/time -l /tmp/cwp-semantic-final.test -test.run '^$' -test.bench '^BenchmarkKnowledgeHybridMaximumDimensions$/^50000$/^cold$' -test.benchtime=20x -test.count=1 -test.timeout=20m
```

其余三个case将规模/cache替换为10000/cold、10000/warm、50000/warm。旧实现从2e090cf独立checkout编译，避免覆盖工作树。原八份结果不删除慢样本，10k通过不能覆盖50k失败，性能样本不能替代真实Recall。

## 最终门禁

- 相关Store/Server/Queue：`go test ./internal/store ./internal/server ./internal/queue -run 'Embedding|KnowledgeHybrid|KnowledgeRetriev|KnowledgeSearch|KnowledgeSemantic|EvidenceGap|Understanding' -race -count=1` EXIT0，109.233/26.255/27.137s，见[race原日志](race.log)。没有将此命令称作全项目race。
- `make cover-gate` EXIT0，包含全库普通行为测试，门槛/地板/豁免未改，见[完整日志](cover-gate.log)。
- `make lint`、`go vet ./...`、`go build ./cmd/cloudwisepod`、`git diff --check` EXIT0；见[lint日志](lint.log)。无前端JS实现更改。
- 变更源码和测试的[SHA清单](source-sha256.txt)与最终测量输入对应；原benchmark夹具已恢复、没有cache/mmap测试注入。
