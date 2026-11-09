#!/bin/bash
# 把 launchd 模板渲染成当前机器可用的 plist 并加载。
# 用法：./deploy/install-launchd.sh [--uninstall]
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
LABEL="com.news.platform"
TARGET="$HOME/Library/LaunchAgents/$LABEL.plist"
TEMPLATE="$ROOT/deploy/com.news.platform.plist.template"

if [ "${1:-}" = "--uninstall" ]; then
    launchctl bootout "gui/$(id -u)" "$TARGET" 2>/dev/null || true
    rm -f "$TARGET"
    echo "已卸载 $LABEL"
    exit 0
fi

[ -x "$ROOT/bin/platform" ] || { echo "ERROR: 先构建 $ROOT/bin/platform（make build-native）"; exit 1; }
mkdir -p "$HOME/Library/LaunchAgents" "$HOME/Library/Logs"

sed -e "s|__ROOT__|$ROOT|g" -e "s|__HOME__|$HOME|g" "$TEMPLATE" > "$TARGET"

# 先卸载旧实例再加载，避免 kickstart 沿用旧环境
launchctl bootout "gui/$(id -u)" "$TARGET" 2>/dev/null || true
launchctl bootstrap "gui/$(id -u)" "$TARGET"
echo "已加载 $LABEL -> $TARGET"
echo "日志：$HOME/Library/Logs/news-platform.{out,err}.log"
