#!/usr/bin/env bash
# 导出一份可跨机器迁移的完整备份包（DB 快照 + 证据音频 + manifest）。
# 产物命名带时间戳，权限收紧为 600；打印 SHA256 与 scp 提示。
# 注意：包内 settings 表含 Provider API key 明文，只能走加密通道传输（scp/AirDrop/加密U盘）。
set -euo pipefail

cd "$(dirname "$0")/.."

usage() {
  cat <<'EOF'
用法：scripts/export.sh [目标目录]    # 默认 ~/backups/cloudwisepod
  依赖 DATA_DIR（默认 ./data）与 SESSION_SECRET 环境变量。
EOF
}

DEST_DIR="${1:-$HOME/backups/cloudwisepod}"
DATA_DIR="${DATA_DIR:-./data}"
BIN="./cloudwisepod"

# 编译产物不存在则现编（导出走同一二进制，保证与数据 schema 一致）
if [ ! -x "$BIN" ]; then
  echo "==> 未找到 ${BIN}，先编译..."
  go build -o "$BIN" ./cmd/cloudwisepod
fi

if [ ! -f "$DATA_DIR/cloudwisepod.db" ]; then
  echo "错误：$DATA_DIR/cloudwisepod.db 不存在（DATA_DIR 设置是否正确？）" >&2
  exit 1
fi

: "${SESSION_SECRET:?错误：需要 SESSION_SECRET 环境变量（任意长随机串，导出校验用）}"

mkdir -p "$DEST_DIR"
STAMP="$(date +%Y%m%d-%H%M%S)"
OUT="$DEST_DIR/cloudwisepod-$STAMP.tar.gz"

echo "==> 导出到 $OUT"
DATA_DIR="$DATA_DIR" "$BIN" backup "$OUT"
chmod 600 "$OUT"

echo ""
echo "SHA256: $(shasum -a 256 "$OUT" | cut -d' ' -f1)"
echo ""
echo "传输到另一台机器（加密通道）："
echo "  scp '$OUT' <user>@<host>:~/"
echo ""
echo "对端接收后核对："
echo "  shasum -a 256 ~/$(basename "$OUT")"
