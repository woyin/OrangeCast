#!/usr/bin/env bash
# 新机器一键完成：编译 → 恢复备份（可选）→ SESSION_SECRET 持久化 → 启动服务。
# 幂等：可重复执行；已有数据库不会被覆盖，除非 --force-restore。
#
# 用法：
#   ./scripts/remote-setup.sh                          # 全新实例（空数据目录）
#   ./scripts/remote-setup.sh --backup ~/backup.tar.gz # 从备份包恢复后启动
#   ./scripts/remote-setup.sh --backup ~/backup.tar.gz --force-restore  # 覆盖已有数据
#   ./scripts/remote-setup.sh --port 8080              # 指定端口（默认 8080）
set -euo pipefail

cd "$(dirname "$0")/.."

usage() {
  cat <<'EOF'
用法：scripts/remote-setup.sh [--backup <backup.tar.gz>] [--force-restore] [--port <port>]

  --backup         备份包路径；恢复到 DATA_DIR（默认 ./data）
  --force-restore  DATA_DIR 已有数据库时允许覆盖（默认拒绝并退出）
  --port           监听端口（默认 8080）
EOF
}

BACKUP=""
FORCE_RESTORE=0
PORT="${PORT:-8080}"

while [ $# -gt 0 ]; do
  case "$1" in
    --backup)
      [ $# -ge 2 ] || { usage >&2; exit 2; }
      BACKUP="$2"; shift 2 ;;
    --backup=*)
      BACKUP="${1#--backup=}"; shift ;;
    --force-restore)
      FORCE_RESTORE=1; shift ;;
    --port)
      [ $# -ge 2 ] || { usage >&2; exit 2; }
      PORT="$2"; shift 2 ;;
    --port=*)
      PORT="${1#--port=}"; shift ;;
    -h|--help)
      usage; exit 0 ;;
    *)
      echo "未知参数：$1" >&2; usage >&2; exit 2 ;;
  esac
done

DATA_DIR="${DATA_DIR:-./data}"
BIN="./cloudwisepod"
DB_FILE="$DATA_DIR/cloudwisepod.db"
SECRET_FILE="$DATA_DIR/.session-secret"

# 1. 依赖检查 + 编译
if ! command -v go >/dev/null 2>&1; then
  echo "错误：未找到 go（需要 Go 1.25+）。安装：https://go.dev/dl/" >&2
  exit 1
fi
echo "==> 编译 $BIN"
go build -o "$BIN" ./cmd/cloudwisepod

mkdir -p "$DATA_DIR"

# 2. SESSION_SECRET 持久化：必须在 restore 之前（restore 也走 config.Load 校验该变量）。
#    首次生成随机值存盘（0600），此后复用；保证重启后登录态不失效。
if [ ! -f "$SECRET_FILE" ]; then
  mkdir -p "$DATA_DIR"
  SECRET="$(head -c 32 /dev/urandom | shasum -a 256 | cut -d' ' -f1)"
  printf '%s' "$SECRET" > "$SECRET_FILE"
  chmod 600 "$SECRET_FILE"
  echo "==> 已生成 SESSION_SECRET（${SECRET_FILE}）"
else
  echo "==> 复用已有 SESSION_SECRET（${SECRET_FILE}）"
fi
SECRET="$(cat "$SECRET_FILE")"

# 3. 恢复备份（有 --backup 时）
if [ -n "$BACKUP" ]; then
  if [ ! -f "$BACKUP" ]; then
    echo "错误：备份包不存在：$BACKUP" >&2
    exit 1
  fi
  if [ -f "$DB_FILE" ] && [ "$FORCE_RESTORE" -ne 1 ]; then
    echo "错误：$DB_FILE 已存在。确认覆盖请加 --force-restore（现有数据将被替换）。" >&2
    exit 1
  fi
  echo "==> 恢复备份 $BACKUP → $DATA_DIR"
  if [ "$FORCE_RESTORE" -eq 1 ]; then
    env PORT="$PORT" DATA_DIR="$DATA_DIR" SESSION_SECRET="$SECRET" "$BIN" restore "$BACKUP" --force
  else
    env PORT="$PORT" DATA_DIR="$DATA_DIR" SESSION_SECRET="$SECRET" "$BIN" restore "$BACKUP"
  fi
fi

# 4. 启动
echo "==> 启动：http://127.0.0.1:$PORT （DATA_DIR=${DATA_DIR}）"
echo "    停止：Ctrl-C"
exec env PORT="$PORT" DATA_DIR="$DATA_DIR" SESSION_SECRET="$SECRET" "$BIN" serve
