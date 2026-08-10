#!/usr/bin/env bash
# NodePilot 管理端（控制面）一键安装脚本（参考 x-ui 风格）
# 功能：检测系统/架构 -> 获取 server 二进制 + web -> 注册为 systemd 服务（运行目录隔离）
# 用法：
#   交互：  bash install-server.sh
#   非交互： NP_ADDR=:8080 bash install-server.sh
#
# 环境变量（非交互模式）：
#   NP_ADDR        监听地址，默认 :8080
#   NP_DB          sqlite 文件名（相对工作目录 data/），默认 nodepilot.db
#   NP_WEB_DIR     web 目录，默认 $INSTALL_DIR/web
#   NP_INSTALL_DIR 安装目录，默认 /opt/nodepilot
#   NP_BINARY_URL  自定义 server 二进制 tar 包地址（覆盖默认 release 下载）

set -e

# ---------- 颜色 ----------
red()    { echo -e "\033[31m$1\033[0m"; }
green()  { echo -e "\033[32m$1\033[0m"; }
yellow() { echo -e "\033[33m$1\033[0m"; }
blue()   { echo -e "\033[34m$1\033[0m"; }

# ---------- 常量 ----------
REPO_OWNER="lgpay"
REPO_NAME="nodepilot"
RELEASE_TAG="v0.1.0"
LEGO_VERSION="${NP_LEGO_VERSION:-v5.3.1}"
INSTALL_DIR="${NP_INSTALL_DIR:-/opt/nodepilot}"
WEB_DIR="${NP_WEB_DIR:-$INSTALL_DIR/web}"
DATA_DIR="$INSTALL_DIR/data"
LOG_DIR="$INSTALL_DIR/logs"
ADDR="${NP_ADDR:-:8080}"
DB_NAME="${NP_DB:-nodepilot.db}"
SERVICE_NAME="nodepilot"
BINARY_NAME="nodepilot-server"

# ---------- 工具 ----------
need_root() {
  if [ "$(id -u)" -ne 0 ]; then
    red "请使用 root 运行本脚本"; exit 1
  fi
}
command_exists() { command -v "$1" >/dev/null 2>&1; }

detect_arch() {
  case "$(uname -m)" in
    x86_64|amd64) echo "amd64" ;;
    aarch64|arm64) echo "arm64" ;;
    *) red "不支持的架构: $(uname -m)"; exit 1 ;;
  esac
}

# ---------- 步骤 ----------
get_server_binary() {
  mkdir -p "$INSTALL_DIR/bin" "$WEB_DIR" "$DATA_DIR" "$LOG_DIR"
  # 1) 本地已有（从项目目录运行脚本时）
  if [ -x "./bin/server" ] && [ -d "./web" ]; then
    green "使用本地 ./bin/server 与 ./web"
    cp ./bin/server "$INSTALL_DIR/bin/$BINARY_NAME"
    cp -r ./web/* "$WEB_DIR/"
    return
  fi
  # 2) 下载 tar 包（内含 bin/ 与 web/）
  local url="${NP_BINARY_URL:-}"
  if [ -z "$url" ]; then
    local arch; arch=$(detect_arch)
    url="https://github.com/$REPO_OWNER/$REPO_NAME/releases/download/$RELEASE_TAG/$BINARY_NAME-linux-$arch.tar.gz"
  fi
  yellow "下载管理端二进制: $url"
  local tmp; tmp=$(mktemp -d)
  if command_exists curl; then
    curl -L "$url" -o "$tmp/srv.tgz" || true
  elif command_exists wget; then
    wget -O "$tmp/srv.tgz" "$url" || true
  else
    red "未找到 curl/wget，无法下载"; exit 1
  fi
  if [ -s "$tmp/srv.tgz" ]; then
    tar -xzf "$tmp/srv.tgz" -C "$INSTALL_DIR"
    rm -rf "$tmp"
    green "管理端二进制已下载并解压到 $INSTALL_DIR"
    return
  fi
  # 3) 回退：本地 go build（server 依赖 CGO/sqlite，需要 gcc）
  red "下载失败，尝试本地 go build 回退..."
  if command_exists go && command_exists gcc; then
    green "使用 go build 生成 server"
    (cd "$(dirname "$0")/.." && go build -o "$INSTALL_DIR/bin/$BINARY_NAME" ./cmd/server) || {
      red "go build 失败"; exit 1
    }
    cp -r ./web/* "$WEB_DIR/" 2>/dev/null || true
    green "管理端已通过 go build 生成"
  else
    red "下载失败且未安装 Go/gcc，无法继续。请设置 NP_BINARY_URL 指向可用的 server 二进制包。"; exit 1
  fi
  chmod +x "$INSTALL_DIR/bin/$BINARY_NAME"
}

# 安装 lego（TLS 证书签发依赖，使用 GitHub 镜像加速）
get_lego() {
  if [ -x "/usr/local/bin/lego" ]; then
    green "lego 已存在于 /usr/local/bin/lego，跳过安装"
    return
  fi
  local arch; arch=$(detect_arch)
  local ver="${LEGO_VERSION#v}"
  local fname="lego_v${ver}_linux_${arch}.tar.gz"
  local url="https://github.com/go-acme/lego/releases/download/${LEGO_VERSION}/${fname}"
  yellow "下载 lego (${LEGO_VERSION}) 用于证书签发: $url"
  local tmp; tmp=$(mktemp -d)
  if command_exists curl; then
    curl -L "$url" -o "$tmp/lego.tgz" || true
  elif command_exists wget; then
    wget -O "$tmp/lego.tgz" "$url" || true
  else
    red "未找到 curl/wget，无法下载 lego"; return
  fi
  if [ -s "$tmp/lego.tgz" ]; then
    tar -xzf "$tmp/lego.tgz" -C "$tmp" && cp "$tmp/lego" /usr/local/bin/lego && chmod +x /usr/local/bin/lego && rm -rf "$tmp"
    green "lego 已安装到 /usr/local/bin/lego"
  else
    red "lego 下载失败（证书签发功能需要它）。可手动安装后重试，或继续（证书功能将不可用）。"
  fi
}

prompt_config() {
  if [ -z "$NP_ADDR" ]; then
    read -r -p "监听地址 [默认 :8080]: " input
    [ -n "$input" ] && ADDR="$input"
  fi
  yellow "管理端将监听 $ADDR，Web 目录 $WEB_DIR，数据库 $DATA_DIR/$DB_NAME"
}

write_service() {
  cat > /etc/systemd/system/$SERVICE_NAME.service <<EOF
[Unit]
Description=NodePilot Control Plane
After=network.target

[Service]
Type=simple
User=root
WorkingDirectory=$DATA_DIR
ExecStart=$INSTALL_DIR/bin/$BINARY_NAME --web-dir $WEB_DIR --db $DB_NAME --addr $ADDR >> $LOG_DIR/server.log 2>&1
Restart=on-failure
RestartSec=3

[Install]
WantedBy=multi-user.target
EOF
}

verify() {
  sleep 2
  local port="${ADDR#:}"
  local code
  code=$(curl -s -o /dev/null -w "%{http_code}" "http://127.0.0.1:$port/" || true)
  if [ "$code" = "200" ]; then
    green "管理端自检通过（Web 控制台可访问）"
  else
    yellow "自检未通过（HTTP $code），请查看日志: journalctl -u $SERVICE_NAME -n 50"
  fi
}

install_server() {
  need_root
  yellow "=== NodePilot 管理端安装 ==="
  get_server_binary
  get_lego
  prompt_config
  write_service
  systemctl daemon-reload
  systemctl enable "$SERVICE_NAME"
  systemctl restart "$SERVICE_NAME"
  if systemctl is-active --quiet "$SERVICE_NAME"; then
    green "NodePilot 管理端安装并启动成功！"
    blue "控制台: http://<本机IP>$ADDR"
    yellow "初始管理员 admin 的随机密码已打印在上方日志与 journalctl 中，请登录后立即修改！"
    verify
  else
    red "管理端启动失败，请查看日志: journalctl -u $SERVICE_NAME -n 50"
    exit 1
  fi
}

uninstall_server() {
  need_root
  yellow "=== 卸载 NodePilot 管理端 ==="
  systemctl stop "$SERVICE_NAME" 2>/dev/null || true
  systemctl disable "$SERVICE_NAME" 2>/dev/null || true
  rm -f /etc/systemd/system/$SERVICE_NAME.service
  systemctl daemon-reload
  rm -rf "$INSTALL_DIR"
  green "已卸载（安装目录 $INSTALL_DIR 已删除）"
}

show_status() { systemctl status "$SERVICE_NAME" --no-pager 2>/dev/null || red "服务未安装/未运行"; }
show_config() { [ -f /etc/systemd/system/$SERVICE_NAME.service ] && cat /etc/systemd/system/$SERVICE_NAME.service || red "服务未安装"; }
restart_s() { systemctl restart "$SERVICE_NAME" && green "已重启"; }
stop_s()    { systemctl stop "$SERVICE_NAME" && green "已停止"; }
start_s()   { systemctl start "$SERVICE_NAME" && green "已启动"; }

menu() {
  echo; blue "=== NodePilot 管理端 管理脚本 ==="
  echo "  0) 安装"
  echo "  1) 启动"
  echo "  2) 停止"
  echo "  3) 重启"
  echo "  4) 查看状态"
  echo "  5) 查看配置"
  echo "  6) 卸载"
  echo "  9) 退出"
  echo
  read -r -p "请选择 [0-9]: " choice
  case "$choice" in
    0) install_server ;;
    1) start_s ;;
    2) stop_s ;;
    3) restart_s ;;
    4) show_status ;;
    5) show_config ;;
    6) uninstall_server ;;
    9) exit 0 ;;
    *) red "无效选择" ;;
  esac
}

if [ -n "$NP_ADDR" ]; then
  install_server
  exit 0
fi

if [ "$1" = "uninstall" ]; then
  uninstall_server
  exit 0
fi

while true; do
  menu
done
