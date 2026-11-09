#!/bin/bash
# macOS 本机启动脚本：加载 .env，前置 venv python，exec platform。
# launchd 通过 deploy/com.news.platform.plist 调用本脚本；手工调试也可直接运行。
set -e

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

# 前置 venv python，让 script 源 subprocess 调用的 python3 解析到带依赖的解释器
export PATH="$ROOT/.venv/bin:$ROOT/venv/bin:/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin"

if [ -f .env ]; then
    set -a
    source .env
    set +a
fi

# 调度器按本地时区计算推送时间，必须固定为北京时间
export TZ=Asia/Shanghai

# 双实例：本机默认 standby（只读常驻），由 .env 的 NIMBUS_RUNTIME_ROLE 覆盖。
# 未设置时用 config.platform.yaml 里的 runtime.role。
exec ./bin/platform -config config.platform.yaml
