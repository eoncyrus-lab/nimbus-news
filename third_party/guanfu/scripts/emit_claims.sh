#!/usr/bin/env bash
# emit_claims.sh — 每日为四个资产各跑一次 forecast，让 emitClaim 写入 claim ledger。
# 这是 calibrate 闭环的数据来源（2026-07 G1：修复"cron 跑裸 --json 不产 claim"的断档）。
# 输出丢弃——目的只是 claim 副作用；盘面归档仍由原 cron 行负责。
set -u
GUANFU="${GUANFU_BIN:-$HOME/.local/bin/guanfu}"
LOG="$HOME/.guanfu/cron.log"

# 先增量刷新数据底座——2026-07 发现 gold 因无人跑 refresh 断更 35 天，
# forecast 一直基于 5 月末的序列在写 claim。refresh 有 fresh-skip，代价极低。
if "$GUANFU" refresh > /dev/null 2>> "$LOG"; then
  echo "$(date '+%Y/%m/%d %H:%M:%S') emit_claims: refresh ok" >> "$LOG"
else
  echo "$(date '+%Y/%m/%d %H:%M:%S') emit_claims: refresh FAILED (continuing with cached)" >> "$LOG"
fi

for asset in btc qqq spy gold; do
  if "$GUANFU" "$asset" --forecast --json > /dev/null 2>> "$LOG"; then
    echo "$(date '+%Y/%m/%d %H:%M:%S') emit_claims: $asset ok" >> "$LOG"
  else
    echo "$(date '+%Y/%m/%d %H:%M:%S') emit_claims: $asset FAILED" >> "$LOG"
  fi
done
