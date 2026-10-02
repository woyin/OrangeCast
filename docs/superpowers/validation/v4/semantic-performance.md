# S07 语义检索性能验证

最新状态：后续检索优化同条件对照冷 p95 为124.8/690.6ms；10k通过150ms，50k仍超过500ms，实际语义保持关闭。详见[优化对照/原始数据](../semantic-optimization/README.md)。下文0089索引补丁历史轮冷p95 175.1/837.7ms的失败证据保留，不作为新源码最终值。索引补丁前的四案例失败记录也完整保留。真实 embedding 的 Recall 收益另待验证，下面的合成向量测量不能证明真实质量。

## 冻结环境与代码

- 时间：2026-10-02，Asia/Singapore（UTC+8）。
- 机器：Apple M4，10 个逻辑 CPU，24GiB RAM；Darwin 27.0.0 arm64。
- Go：go1.27.1 darwin/arm64。
- 主干：`d2d5b15e8e5e2aa170aaa25523eb42fa541b795c`，加本轮未提交实现；实际执行代码以冻结 test binary 为准。
- 二进制：`/tmp/cwp-semantic-benchmark.test`。
- 二进制 SHA256：`70381a2071eeb65bdba753d0f36239955818071b354b79013895b612743560e8`。
- 未读取 `.env`，未使用默认资料库，未调用 embedding 供应商，未开启生产语义开关。

## 测量方法与边界

四个 case 使用同一个已编译的二进制，分别在独立进程顺序执行；每个 case 20 个定时样本。维数固定 2048，规模固定 10000/50000，阈值保持冷 p95 150/500ms。p95 使用排序后的第 19 个样本（20 个样本的 nearest-rank 95%），不删除慢样本。

cold 指每次定时样本前清除 Store 的应用矩阵缓存；warm 复用该缓存。这沿用原基准定义，不声称清除操作系统页缓存。基准包括 FTS、SQL 向量加载、来源权限与当前版本检查、维数/norm 校验、RRF 排序及有界结果投影；使用显式只读 `EvaluateKnowledgeRetrieval` 评测入口，允许测量未通过质量准入的索引，不会开启普通检索。HTTP/render、真实模型网络调用、质量报告导入和预算调度不在本计时范围内；正常 `Retrieve` 与 query API 的质量准入另由测试验证。

每次调用 `/usr/bin/time -l` 直接执行二进制，避免把 Go 编译过程计入 RSS。macOS 本机 `man getrusage` 规定 `ru_maxrss` 的单位为 bytes；下表 peak RSS 为该值除以 1048576。它是造库、基准校准和检索整个独立进程的真实峰值，不是矩阵单独驻留量。Go `sampled-heap-MiB` 是每次查询后采样的 HeapInuse，不能当成 RSS 或精确峰值。

运行并非纯 idle：其他 Python 进程、Swift 编译器和窗口系统持续使用 CPU。根代理暂停了本项目全库 coverage/heavy Go，但这不等于机器无其他工作。未停止无关进程。所有 case 的实际负载按原样登记，不据此声称隔离环境下的性能提升。

## 最终索引补丁后结果

0089 增加 `knowledge_embedding_vectors(doc_key)` 索引：现有主键以 config_id 开头，文档更新触发的 DELETE WHERE doc_key 原为全表 SCAN；新数据库 EXPLAIN 为 SEARCH USING COVERING INDEX idx_embedding_vectors_document。该索引减少造库及文档失效清理的扫描，不授予权限、不改变维数/窗口规模、正常查询和阈值。

新增真实 SQLite 测试验证两个 config 同一窗口同步失效、另一窗口保留、upsert 事件保持和撤权后全部 config 向量清理与零待付费窗口。定向 race 测试三例通过（8.720s）。

新冻结二进制 `/tmp/cwp-semantic-indexed-benchmark.test` SHA256：`dc2b3c70d6200d6e3e51d8600df507ec9229ebfcc118dd2888c78f37a85ca8fe`。与下方旧 binary 相比，其间也包含其他代理在共享工作树落地的更改，且系统负载不同；不能把两轮查询差值全部归因于这个索引。

| 规模 | 缓存 | 样本 | 平均 ms/op | p95 ms | 冷阈值 ms | Go采样heap MiB | 进程peak RSS MiB | 分配 MiB/op | allocs/op | 进程 real 秒 | 结论 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| 10000 | cold | 20 | 169.42 | 175.1 | 150 | 111.2 | 197.09 | 160.91 | 98763 | 5.72 | 未通过 |
| 10000 | warm | 20 | 96.35 | 113.6 | 不适用 | 122.1 | 146.67 | 1.99 | 8510 | 3.96 | 仅观察，不能替代cold |
| 50000 | cold | 20 | 802.54 | 837.7 | 500 | 522.5 | 828.62 | 799.55 | 459111 | 24.40 | 未通过 |
| 50000 | warm | 20 | 456.98 | 594.4 | 不适用 | 514.2 | 560.39 | 5.83 | 8599 | 19.35 | 仅观察，不能替代cold |

| case | 开始 load average（1/5/15m） | 结束 load average（1/5/15m） |
| --- | --- | --- |
| 10000/cold | 4.75 / 6.57 / 10.31 | 4.35 / 6.37 / 10.15 |
| 10000/warm | 4.35 / 6.37 / 10.15 | 4.22 / 6.27 / 10.08 |
| 50000/cold | 4.22 / 6.27 / 10.08 | 6.79 / 6.55 / 9.99 |
| 50000/warm | 6.79 / 6.55 / 9.99 | 6.14 / 6.41 / 9.84 |

四进程均退出0，仅代表 benchmark 正常完成。原始证据在本目录 `semantic-indexed-{10000,50000}-{cold,warm}.{stdout,rusage}`。全程顺序执行，每例20样本、2048维，未删除慢样本。两冷指标未达标，真实 Recall/改写收益也无数据，不能导入通过报告或开启实际语义检索。

## 索引补丁前结果（保留失败证据）

| 规模 | 缓存 | 样本 | 平均 ms/op | p95 ms | 冷阈值 ms | Go采样heap MiB | 进程peak RSS MiB | 分配 MiB/op | allocs/op | 进程 real 秒 | 结论 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| 10000 | cold | 20 | 367.75 | 564.3 | 150 | 112.1 | 189.69 | 160.91 | 98756 | 25.88 | 未通过 |
| 10000 | warm | 20 | 171.04 | 226.8 | 不适用 | 122.1 | 146.75 | 1.98 | 8505 | 29.03 | 仅观察，不能替代cold |
| 50000 | cold | 20 | 1589.54 | 2370 | 500 | 535.2 | 825.72 | 799.55 | 459105 | 537.36 | 未通过 |
| 50000 | warm | 20 | 464.63 | 617.5 | 不适用 | 514.2 | 553.48 | 5.83 | 8602 | 276.21 | 仅观察，不能替代cold |

| case | 开始 load average（1/5/15m） | 结束 load average（1/5/15m） |
| --- | --- | --- |
| 10000/cold | 16.78 / 16.50 / 14.28 | 15.67 / 16.62 / 14.61 |
| 10000/warm | 15.67 / 16.62 / 14.61 | 11.45 / 15.00 / 14.22 |
| 50000/cold | 11.45 / 15.00 / 14.22 | 9.20 / 14.26 / 14.79（过程中13:27为28.43 /17.94 /15.33） |
| 50000/warm | 9.20 / 14.26 / 14.79 | 4.82 / 7.22 / 11.03 |

## 可复验命令

编译只进行一次，放在计时之外：

```sh
go test -c ./internal/store -o /tmp/cwp-semantic-indexed-benchmark.test
shasum -a 256 /tmp/cwp-semantic-indexed-benchmark.test
```

然后顺序执行下面四条命令；不同时运行四个 case：

```sh
/usr/bin/time -l /tmp/cwp-semantic-indexed-benchmark.test -test.run '^$' -test.bench '^BenchmarkKnowledgeHybridMaximumDimensions$/^10000$/^cold$' -test.benchtime=20x -test.count=1 -test.timeout=20m
/usr/bin/time -l /tmp/cwp-semantic-indexed-benchmark.test -test.run '^$' -test.bench '^BenchmarkKnowledgeHybridMaximumDimensions$/^10000$/^warm$' -test.benchtime=20x -test.count=1 -test.timeout=20m
/usr/bin/time -l /tmp/cwp-semantic-indexed-benchmark.test -test.run '^$' -test.bench '^BenchmarkKnowledgeHybridMaximumDimensions$/^50000$/^cold$' -test.benchtime=20x -test.count=1 -test.timeout=20m
/usr/bin/time -l /tmp/cwp-semantic-indexed-benchmark.test -test.run '^$' -test.bench '^BenchmarkKnowledgeHybridMaximumDimensions$/^50000$/^warm$' -test.benchtime=20x -test.count=1 -test.timeout=20m
```

索引补丁前四个 case 的原始 stdout/rusage 已持久化为本目录 `semantic-{10000,50000}-{cold,warm}.{stdout,rusage}`。每个 stdout 的 PASS 仅表示 benchmark 正常结束，不表示性能阈值通过。

## 已落地优化及证据限制

- 单遍读取/decode，使用 `sql.RawBytes` 后立即解码到自有内存，避免保留第二份完整 blob 语料；SQL buffer 不逃逸到缓存。
- 每 256 个窗口分配自有 float32 arena，避免每个窗口单独分配；保留完整维数、有限值与归一化校验。
- MATERIALIZED CTE 对最多 1000 个明确选择的来源先判定权限，再用于所有窗口；理解快照自身权限与全部 Reference 来源的资格仍保留。
- 移除全量向量 blob 的 SQL 排序；最终排序仍有稳定键与 RRF。前置 CTE 后全部筛选参数改为命名，防止范围参数序号错绑。
- 既有 40 条冻结查询、范围/主题/日期/分页、撤权、坏维数、缓存和容量等定向测试通过；这些合成材料只验证算法与生命周期。

继承的 2026-10-01 2048维旧数据为冷 611.7/1823ms、热 87.86/445.1ms。当前运行负载、实现与进程 RSS 方法不同，不能把它们当成同条件因果对照；本轮只判断固定绝对阈值，不宣称优化百分比或真实检索收益。

## 准入状态

真实 40 查询/relevance/corpus 清单、embedding/模型/维数身份、估计与实际费用、源码/机器/方法以及有效质量报告仍是独立准入条件。缺报告、旧报告身份变化、报告损坏、Recall/改写收益或冷性能未达标均保持实际语义关闭，FTS 继续可用。当前数据不能创建通过报告、替代真实质量、开启生产开关或宣称 S07 产品收益验收通过。
