#!/bin/bash
# 把 launchd 模板渲染成当前机器可用的 plist 并加载。
# 用法：./deploy/install-launchd.sh [--uninstall]
set -eo pipefail

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

# 与 launch-mac.sh 一致：NIMBUS_CONFIG 可写在 .env 里。
if [ -f "$ROOT/.env" ]; then
    set -a
    # shellcheck disable=SC1091
    . "$ROOT/.env"
    set +a
fi
CONFIG="${NIMBUS_CONFIG:-config.platform.yaml}"

[ -x "$ROOT/bin/platform" ] || { echo "ERROR: 先构建 $ROOT/bin/platform（make build-native）" >&2; exit 1; }
[ -f "$ROOT/$CONFIG" ] || { echo "ERROR: 缺少 $ROOT/$CONFIG，先运行 make init" >&2; exit 1; }
if [ ! -d "$ROOT/.venv" ] && [ ! -d "$ROOT/venv" ]; then
    echo "WARN: 未找到 .venv/venv，脚本源会因缺依赖失败；首次部署请先 make python-deps" >&2
fi
mkdir -p "$HOME/Library/LaunchAgents" "$HOME/Library/Logs"

sed -e "s|__ROOT__|$ROOT|g" -e "s|__HOME__|$HOME|g" "$TEMPLATE" > "$TARGET"
plutil -lint "$TARGET" >/dev/null || { echo "ERROR: 渲染出的 plist 不合法：$TARGET" >&2; exit 1; }

# 先卸载旧实例再加载，避免 kickstart 沿用旧环境
launchctl bootout "gui/$(id -u)" "$TARGET" 2>/dev/null || true
launchctl bootstrap "gui/$(id -u)" "$TARGET"
echo "已加载 $LABEL -> $TARGET"
echo "配置：$ROOT/$CONFIG（角色默认 standby，可在 .env 覆盖）"

# 给进程启动留出时间；只做提示，不影响已完成的安装。
ready=0
for _ in $(seq 1 10); do
    if curl -sf -m 2 http://127.0.0.1:8081/api/health/live >/dev/null 2>&1; then
        ready=1
        break
    fi
    sleep 1
done
if [ "$ready" -eq 1 ]; then
    echo "健康检查：/api/health/live OK"
else
    echo "WARN: 已加载但 /api/health/live 暂未响应，查看日志："
    echo "  tail -f $HOME/Library/Logs/news-platform.err.log"
fi
echo "日志：$HOME/Library/Logs/news-platform.{out,err}.log"
echo "重启：make mac-restart    卸载：make uninstall-mac"
