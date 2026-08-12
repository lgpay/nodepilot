#!/usr/bin/env bash
# NodePilot 管理端 SQLite 在线备份（WAL 安全快照）
#
# 用法：
#   bash backup.sh [数据库路径]          # 默认 data/nodepilot.db
#   BACKUP_DIR=/opt/nodepilot/backups KEEP=7 bash backup.sh
#
# 环境变量：
#   BACKUP_DIR  备份输出目录，默认 /opt/nodepilot/backups
#   KEEP        保留最近几份，默认 7
#
# 恢复：
#   systemctl stop nodepilot
#   cp /opt/nodepilot/backups/nodepilot_<时间戳>.db /opt/nodepilot/data/nodepilot.db
#   rm -f /opt/nodepilot/data/nodepilot.db-wal /opt/nodepilot/data/nodepilot.db-shm
#   systemctl start nodepilot
#
# 说明：SQLite 在 WAL 模式下直接 cp 主库文件不安全（未合并的 WAL 会丢失），
# 必须用 sqlite3 ".backup" 做在线一致性快照（只读打开，原子）。

set -euo pipefail

DB="${1:-data/nodepilot.db}"
BACKUP_DIR="${BACKUP_DIR:-/opt/nodepilot/backups}"
KEEP="${KEEP:-7}"

command -v sqlite3 >/dev/null 2>&1 || { echo "需要 sqlite3 命令（apt install sqlite3）"; exit 1; }
[ -f "$DB" ] || { echo "数据库不存在: $DB"; exit 1; }

mkdir -p "$BACKUP_DIR"
TS=$(date +%Y%m%d_%H%M%S)
OUT="$BACKUP_DIR/nodepilot_${TS}.db"

# WAL 模式在线快照：以只读 URI 打开并 .backup，保证备份一致
sqlite3 "file:${DB}?mode=ro" ".backup '${OUT}'"
echo "已备份到 $OUT ($(du -h "$OUT" | cut -f1))"

# 清理旧备份，仅保留最近 $KEEP 份
ls -1t "$BACKUP_DIR"/nodepilot_*.db 2>/dev/null | tail -n +$((KEEP + 1)) | while read -r old; do
  rm -f "$old"
  echo "已清理旧备份: $old"
done
