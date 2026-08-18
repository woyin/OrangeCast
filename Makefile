.PHONY: test cover cover-gate lint serve

# 本地运行：加载 .env 后启动（二进制缺失时先构建）
serve:
	@test -x ./cloudwisepod || go build -o cloudwisepod ./cmd/cloudwisepod
	@set -a; . ./.env; set +a; exec ./cloudwisepod

# 测试：跑全部 Go 单测（含 race 检测）
test:
	go test ./... -race

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
