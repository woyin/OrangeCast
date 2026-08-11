#!/usr/bin/env bash
# 覆盖率门禁：断言每个有测试的包语句覆盖率 >= 95%。
# 豁免清单见 EXEMPT_PACKAGES（当前仅 internal/models —— 纯类型/常量声明，无可执行语句，0% 为覆盖率假象）。
set -euo pipefail

THRESHOLD=95.0
EXEMPT_PACKAGES=(
  "github.com/woyin/orangecast/internal/models"
)

# is_exempt 判断给定包是否在豁免清单中。
is_exempt() {
  local pkg="$1"
  for e in "${EXEMPT_PACKAGES[@]}"; do
    if [[ "$pkg" == "$e" ]]; then
      return 0
    fi
  done
  return 1
}

cd "$(dirname "$0")/.."

echo "==> 运行带覆盖率统计的测试..."
# shellcheck disable=SC2046
OUTPUT=$(go test -cover ./... 2>&1) || {
  echo "测试失败："
  echo "$OUTPUT"
  exit 1
}

echo "$OUTPUT"

failures=""
echo ""
echo "==> 覆盖率门禁（阈值 ${THRESHOLD}%）"
# 解析 "coverage: XX.X% of statements"
while IFS= read -r line; do
  pkg=$(echo "$line" | awk '{print $2}')
  pct=$(echo "$line" | grep -oE 'coverage: [0-9.]+%' | grep -oE '[0-9.]+' | head -1)
  # 跳过无测试的包（"no test files"）与豁免包
  if [[ -z "$pct" ]]; then
    continue
  fi
  if is_exempt "$pkg"; then
    echo "  (豁免) $pkg — ${pct}%"
    continue
  fi
  below=$(awk "BEGIN{ print (${pct} < ${THRESHOLD}) ? 1 : 0 }")
  if [[ "$below" == "1" ]]; then
    echo "  FAIL $pkg — ${pct}% (低于 ${THRESHOLD}%)"
    failures="$failures $pkg:${pct}%"
  else
    echo "  ok   $pkg — ${pct}%"
  fi
done <<< "$(echo "$OUTPUT" | grep 'coverage:')"

if [[ -n "$failures" ]]; then
  echo ""
  echo "覆盖率门禁失败，以下包未达标：$failures"
  exit 1
fi
echo ""
echo "覆盖率门禁通过。"
