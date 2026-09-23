.PHONY: test cover cover-gate lint serve

# 本地运行：加载 .env 后启动（二进制缺失时先构建）
serve:
	@test -x ./cloudwisepod || go build -o cloudwisepod ./cmd/cloudwisepod
	@set -a; . ./.env; set +a; exec ./cloudwisepod

# 测试：跑全部 Go 单测（含 race 检测）。
# timeout 30m：queue/store 等长包在 -race 下合法耗时 ~10 分钟（90 测试 ×
# 独立库 × 全量迁移被探针拖慢），默认 10m 会误报超时（R24 已验证基线）。
test:
	go test ./... -race -timeout 30m

# 覆盖率：生成可读的逐函数覆盖率报告
cover:
	go test -coverprofile=coverage.out -covermode=set ./...
	go tool cover -func=coverage.out

# 覆盖率门禁：断言每个包 >= 95%（另有豁免列表），不达标则非零退出
cover-gate:
	bash scripts/cover-gate.sh

# 注释/导出符号门禁（revive）
lint:
	bash scripts/lint.sh
