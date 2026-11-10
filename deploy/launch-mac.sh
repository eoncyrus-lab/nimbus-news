#!/bin/bash
# macOS 本机启动脚本：加载 .env，前置 venv python，exec platform。
# launchd 通过 deploy/com.news.platform.plist 调用本脚本；手工调试也可直接运行。
set -eo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

# 前置 venv python，让 script 源 subprocess 调用的 python3 解析到带依赖的解释器
export PATH="$ROOT/.venv/bin:$ROOT/venv/bin:/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin"

if [ -f .env ]; then
    set -a
    source .env
    set +a
fi

CONFIG="${NIMBUS_CONFIG:-config.platform.yaml}"

# 运行角色默认 active（唯一摄入/推送实例）。环境变量优先：需要让本机
# 退回收只读 standby / admin 时，在 .env 里显式设置 NIMBUS_RUNTIME_ROLE 覆盖。
# 注意：多实例同时设 active 会重复推送，切换前先确认另一实例已停用。
export NIMBUS_RUNTIME_ROLE="${NIMBUS_RUNTIME_ROLE:-active}"

# 调度器按本地时区计算推送时间，必须固定为北京时间
export TZ=Asia/Shanghai

if [ ! -x ./bin/platform ]; then
    echo "ERROR: 缺少 $ROOT/bin/platform，先运行 make build-native" >&2
    exit 1
fi
if [ ! -f "$CONFIG" ]; then
    echo "ERROR: 缺少配置文件 $ROOT/$CONFIG，先运行 make init" >&2
    exit 1
fi

exec ./bin/platform -config "$CONFIG"
