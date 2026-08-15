#!/usr/bin/env bash
# NodePilot ACL4SSR_Online.ini 自动同步脚本
#
# 从 GitHub 上游拉取最新 ACL4SSR_Online.ini，校验有效性后替换本地文件并重启服务。
# 规则列表(.list)由客户端直接引用 GitHub，本脚本仅同步决定"分组结构/规则路由"的 ini 模板。
#
# 用法：
#   bash sync-acl4ssr.sh              # 使用默认源与路径
#   NP_ACL_SOURCE=... bash sync-acl4ssr.sh   # 自定义上游源
#   DRY_RUN=1 bash sync-acl4ssr.sh    # 试运行（只拉取校验，不替换不重启）
#
# 环境变量：
#   NP_ACL_SOURCE  上游 ini 地址，默认 GitHub ACL4SSR 官方源
#   RULES_DIR      本地 rules 目录，默认 /opt/nodepilot/rules
#   SERVICE_NAME   systemd 服务名，默认 nodepilot
#   DRY_RUN        1 = 只校验不部署

set -euo pipefail

# ---------- 常量 ----------
RULES_DIR="${RULES_DIR:-/opt/nodepilot/rules}"
SERVICE_NAME="${SERVICE_NAME:-nodepilot}"
TARGET="$RULES_DIR/ACL4SSR_Online.ini"
# 上游源（按序尝试，空格分隔）。默认官方源；网络受限的环境可用 NP_ACL_SOURCES
# 注入自建或可信镜像源，例：
#   NP_ACL_SOURCES="https://raw.githubusercontent.com/ACL4SSR/ACL4SSR/master/Clash/config/ACL4SSR_Online.ini <镜像地址>"
NP_ACL_SOURCES="${NP_ACL_SOURCES:-https://raw.githubusercontent.com/ACL4SSR/ACL4SSR/master/Clash/config/ACL4SSR_Online.ini}"

# ---------- 工具 ----------
need_root() {
  if [ "$(id -u)" -ne 0 ]; then
    echo "请使用 root 运行本脚本"; exit 1
  fi
}

# ---------- 主流程 ----------
need_root
[ -d "$RULES_DIR" ] || { echo "rules 目录不存在: $RULES_DIR"; exit 1; }

TMP="$(mktemp)"
trap 'rm -f "$TMP"' EXIT

echo "[acl-sync] 拉取上游（多源尝试）..."
OK=0
for src in $NP_ACL_SOURCES; do
  echo "[acl-sync]   尝试: $src"
  if curl -fsSL --max-time 40 "$src" -o "$TMP" 2>/dev/null; then
    OK=1
    echo "[acl-sync]   成功"
    break
  else
    echo "[acl-sync]   失败"
  fi
done
if [ "$OK" != "1" ]; then
  echo "[acl-sync] 所有上游源拉取失败，保持现有 ini 不变"
  exit 1
fi

# ---------- 有效性校验（与 GitHub Actions 同款防线）----------
grep -q "^ruleset=" "$TMP" || { echo "[acl-sync] 无效: 缺少 ruleset"; exit 1; }
grep -q "^custom_proxy_group=" "$TMP" || { echo "[acl-sync] 无效: 缺少 custom_proxy_group"; exit 1; }
GROUP_COUNT=$(grep -c "^custom_proxy_group=" "$TMP")
[ "$GROUP_COUNT" -ge 5 ] || { echo "[acl-sync] 无效: 分组过少($GROUP_COUNT)"; exit 1; }
LINE_COUNT=$(wc -l < "$TMP")
echo "[acl-sync] 校验通过: $LINE_COUNT 行, $GROUP_COUNT 个分组"

# ---------- 是否变更 ----------
if [ -f "$TARGET" ] && cmp -s "$TMP" "$TARGET"; then
  echo "[acl-sync] ini 无变化，无需重启"
  exit 0
fi

if [ "${DRY_RUN:-0}" = "1" ]; then
  echo "[acl-sync] 试运行模式：检测到变更但未部署（-e 内容见下）"
  diff -u "$TARGET" "$TMP" 2>/dev/null | head -40 || true
  exit 0
fi

# ---------- 备份并替换 ----------
BAK="$TARGET.bak.$(date +%Y%m%d%H%M%S)"
cp "$TARGET" "$BAK"
cp "$TMP" "$TARGET"
chmod 644 "$TARGET"
echo "[acl-sync] 已替换: $TARGET (备份: $BAK)"

# ---------- 重启服务使模板生效（InitACLTemplate 仅在启动时加载）----------
if command -v systemctl >/dev/null 2>&1 && systemctl list-units --type=service 2>/dev/null | grep -q "$SERVICE_NAME.service"; then
  echo "[acl-sync] 重启 $SERVICE_NAME 以加载新模板..."
  if systemctl restart "$SERVICE_NAME"; then
    echo "[acl-sync] 服务已重启"
  else
    echo "[acl-sync] 服务重启失败，请检查: journalctl -u $SERVICE_NAME -n 50"
    exit 1
  fi
else
  echo "[acl-sync] 未检测到 systemd 服务，跳过重启（请手动重启加载新模板）"
fi

echo "[acl-sync] 完成"
