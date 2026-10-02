# 精确语义检索第二轮优化（2026-10-02）

基线 `6da9791`。本轮保留完整维数、窗口、float64 点积累加顺序、每文档最大分数和精确 top-200 平分排序。没有 ANN、迁移、mmap、降维或供应商调用；未修改 `.env`、默认资料库或开启语义检索。

## 已实现

- 大于等于 1,048,576 次乘加的查询，最多四个 worker 分区计算；同一窗口仍逐维按原顺序累加，不跨线程归约单个点积。进程内至多一个大任务并行计算，排队可取消，所有 worker 完成后才返回。小任务按原顺序计算。
- 每个 Store 至多一个矩阵载入任务，相同键等待者共享结果，不同筛选键等待后重新检查；缓存仍只有一个。等待者独立取消，载入者取消后存活等待者可重试。载入末尾校验索引和删除 epoch，结果输出沿用当前权限、版本及准入检查。活跃请求可以持有旧不可变矩阵，因此不宣称进程总共只分配一个矩阵。
- 40 查询只读 Recall@10 批量工具，比较原始查询与同义改写的 FTS/RRF；全部请求仅 GET，已有查询向量缺失时记为降级，索引身份变化时整组失效。脚本不准备向量、不证明真实模型来源、不导入准入、不打开搜索。操作见[评测说明](../../../semantic-quality-evaluation.md)。

## 同机对照

Apple M4、Darwin arm64、Go1.27.1，原有 2048 维 benchmark 未改。10k/50k × cold/warm，各前后独立进程20样本，顺序执行八组；测量期间没有并行运行本轮全库门禁。cold 清应用矩阵缓存，不清 OS 页缓存；p95 为排序第19个样本。模拟向量测试性能及行为，不能证明真实 Recall。原 stdout 和 `/usr/bin/time -l` 数据均保留。

| 完整窗口 | 缓存 | p95 ms 前→后 | 降低 | 分配 MiB/op 前→后 | 峰值 RSS MiB 前→后 |
| --- | --- | --- | --- | --- | --- |
| 10000 | cold | 122.5 → 107.1 | 12.6% | 161.32 → 161.40 | 194.69 → 192.66 |
| 10000 | warm | 50.17 → 36.72 | 26.8% | 0.91 → 0.99 | 139.59 → 139.78 |
| 50000 | cold | 617.8 → 499.6 | 19.1% | 803.68 → 804.07 | 847.75 → 851.28 |
| 50000 | warm | 232.4 → 158.6 | 31.8% | 1.22 → 1.60 | 549.47 → 549.19 |

50k cold 本次低于500ms，仅有0.4ms余量，不能认为不同设备或负载下稳定满足门槛。临时独立试跑 p95496.9ms，正式测量 p95499.6ms；没有取快样本替换正式结果。warm 增加逐窗口分数数组，50k 约增加0.38MiB/op，换取有界精确并行；cold RSS 增加约0.4%，不是依靠大块映射达标。RSS 包含造库与整个进程；采样 Go heap 与 RSS 含义不同。前一轮基线617.8ms与旧记录690.6ms的差异也表明环境波动，不跨轮拼接收益。

before binary SHA256：`3dac7284b277bf579677719fa2283585f742b2492f25f59ec3f6f8b4e1109d05`；after：`00f6e857349df3c2a56ff2e8eb45e819fb250e744a8d74073eaddaa34dfc7e74`。性能编译后仅补充/整理测试及文档，检索生产源码未再改动。

## 撤回实验

WITHOUT ROWID 存储布局试验 cold50k p95953.6ms，收益不足，没有新增迁移。按授权身份筛选、再按 rowid 每批256条读取向量，即使强制 rowid 查询，cold50k p95931.5ms，额外查询与分配不划算；已完整撤回，保留原单遍解码。两组探索日志保留 `rejected-*`，不与正式八组作严格因果比较。没有删除失败结果来制造达标结论。

## 行为与质量边界

新增并发测试覆盖同键共享、等待者超时、载入者取消后的重试、旧 epoch 拒绝和重试、稠密1200×1024向量点积分数逐位一致、计算排队取消与无效维数拒绝。已有完整排序 oracle 继续验证文档聚合及平分顺序。

Python八项测试覆盖Recall计算、FTS降级、末次身份变化、部分请求失败、非法输入、来源URL/cookie限制及0600原子输出。真实 Go API 集成运行40查询，验证认证、实际JSON字段、FTS降级不生成语义成绩及数据库 `total_changes()` 不变；该集成使用测试配置与模拟标注，不能当真实评测。

当前缺完整 `LEARNING_EMBEDDING_*` 配置及真实40查询人工相关性标注，没有模型付费请求，没有真实 Recall/成本结果；实际语义继续关闭。脚本输出刻意不是既有质量准入的直接导入格式，完整真实性、费用、人工质量及门槛仍需实际验收。

## 复测

```sh
go test -c ./internal/store -o /tmp/cwp-semantic.test
/usr/bin/time -l /tmp/cwp-semantic.test -test.run '^$' -test.bench '^BenchmarkKnowledgeHybridMaximumDimensions$/^50000$/^cold$' -test.benchtime=20x -test.count=1 -test.timeout=20m
python3 -B scripts/semantic-quality-eval-test.py
go test ./... -race -timeout 30m
make cover-gate
make lint
go vet ./...
go build ./cmd/cloudwisepod
```

其余性能 case 替换规模及缓存。门禁最终结果见同目录日志与下方记录。

## 最终验证

- 首次 `go test ./... -race -timeout 30m` 因新增 Server 测试中注册函数返回值写错而 EXIT1，原始日志保留为 `race-initial-failure.log`。该次其余全部包成功，包括 Store 542.419s、Queue 209.612s。没有称这条失败命令为通过。
- 修复测试编译和选择器配置后，完整 `go test ./internal/server -race -timeout 30m` EXIT0，158.217s，见 `server-race.log`。检索生产代码与首轮相同；随后新增等待者超时场景的测试调整由 Store 专项 race 连续3次验证，10.779s，见 `concurrency-race.log`。因此全部有测试包的 race 均有成功执行证据，采用分批补验，没有重跑第二条全库命令。
- 修复后的 `make cover-gate` EXIT0，Store79.3%、Server79.8%，全部包达到原有门槛/登记地板，未降低门槛。首轮编译失败另留 `cover-gate-initial-failure.log`。
- Python八项测试、真实API集成、`make lint`、`go vet ./...`、构建到临时路径及 `git diff --check` 均 EXIT0。原始日志、测量和 `source-sha256.txt` 保留在本目录。
