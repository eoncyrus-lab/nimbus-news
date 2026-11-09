#!/bin/bash
# Mac 侧每日拉取 VPS 的 Bolt 快照（离线副本，只读，不参与运行）。
# 通过 ssh 让 VPS 现场生成一致性快照再取回，避免直接拷正在写的文件。
#
# 用法: ./deploy/mac-backup.sh          # 拉一份并保留 7 份
# 建议 crontab: 30 3 * * * /path/to/repo/deploy/mac-backup.sh >> ~/Library/Logs/news-backup.log 2>&1
set -euo pipefail

VPS_HOST="${VPS_HOST:-root@45.77.26.44}"
DIR="${NIMBUS_BACKUP_DIR:-$HOME/nimbus-backups}"
KEEP=7
STAMP="$(date +%Y%m%d)"

mkdir -p "$DIR"

ssh -o ConnectTimeout=10 "$VPS_HOST" \
  'set -a; . /opt/news/.env; set +a; curl -sf -m 120 -H "Authorization: Bearer $NIMBUS_ADMIN_TOKEN" -X POST http://127.0.0.1:8081/api/store/snapshot' \
  > "$DIR/platform.$STAMP.db"

# 空文件说明快照失败，不要覆盖上一份可用备份
if [ ! -s "$DIR/platform.$STAMP.db" ]; then
  rm -f "$DIR/platform.$STAMP.db"
  echo "$(date '+%F %T') snapshot failed" >&2
  exit 1
fi

ls -1t "$DIR"/platform.*.db 2>/dev/null | tail -n +$((KEEP + 1)) | xargs -r rm -f
echo "$(date '+%F %T') pulled $DIR/platform.$STAMP.db ($(du -h "$DIR/platform.$STAMP.db" | cut -f1))"
