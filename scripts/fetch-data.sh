#!/usr/bin/env bash
#
# 下载最新的 xdb 数据（来自 fa1seut0pia/ip2region-xdb）
#
# 用法：
#   ./scripts/fetch-data.sh          # 下载到 ./data
#   ./scripts/fetch-data.sh /tmp/x   # 下载到指定目录
#
set -euo pipefail

cd "$(dirname "$0")/.."
DEST="${1:-data}"
BASE="https://github.com/fa1seut0pia/ip2region-xdb/releases/latest/download"

mkdir -p "$DEST"

filesize() {
  # macOS: stat -f%z ; Linux: stat -c%s
  stat -f%z "$1" 2>/dev/null || stat -c%s "$1"
}

for f in ip2region_v4.xdb ip2region_v6.xdb; do
  echo "==> 下载 $f"
  tmp="$DEST/.$f.part"
  curl -fL --retry 3 --retry-delay 2 --connect-timeout 15 -o "$tmp" "$BASE/$f"
  size="$(filesize "$tmp")"
  if [ "$size" -lt 1048576 ]; then
    rm -f "$tmp"
    echo "!! $f 体积异常（${size} bytes），已丢弃" >&2
    exit 1
  fi
  mv "$tmp" "$DEST/$f"   # 原子替换，避免正在使用时读到半截文件
  echo "    ok: $DEST/$f ($(filesize "$DEST/$f") bytes)"
done

echo "==> 完成"
ls -lh "$DEST"
