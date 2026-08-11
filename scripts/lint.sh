#!/usr/bin/env bash
# 注释 / 导出符号门禁：用 revive 强制"导出符号必须有以符号名开头的 Godoc 注释"。
# 若存在任何违规则非零退出（revive 默认输出违规则但返回 0，故需显式检测）。
# 规则配置见 revive.toml。
set -uo pipefail

cd "$(dirname "$0")/.."

if ! command -v revive >/dev/null 2>&1; then
  echo "==> 未找到 revive，先安装 @latest..."
  go install github.com/mgechev/revive@latest
  # 确保 $GOBIN/$GOPATH/bin 在 PATH 内
  export PATH="$(go env GOPATH)/bin:$PATH"
fi

echo "==> 运行 revive（exported：导出符号必须有以符号名开头的注释）..."
OUTPUT=$(revive -config revive.toml ./... 2>&1)
STATUS=$?

echo "$OUTPUT"

# revive 在无规则错误时退出 0，但存在 lint 违规则时也会返回 0；
# 因此必须解析输出：有"violations/count"或匹配到 .go:行即视为违规则。
# 这里用 revive 自带的形式输出末尾的计数行判断。兼容 friendly/默认两种 formatter：
if echo "$OUTPUT" | grep -qE '[0-9]+ files?? (examined|analyzed), *[0-9]+ file.*; *[0-9]+ violations? (found )?$'; then
  echo ""
  echo "revive 发现注释违规则，门禁失败。"
  exit 1
fi

# 兜底：若上面计数行未匹配，但确有 .go: 行形如 lint 报告，也视为违规则。
if echo "$OUTPUT" | grep -qE '\.go:[0-9]+:[0-9]+:'; then
  echo ""
  echo "revive 发现注释违规则，门禁失败。"
  exit 1
fi

echo ""
echo "注释门禁通过。"
exit "$STATUS"
