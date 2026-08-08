#!/usr/bin/env bash
# NodePilot Agent 一键安装脚本（参考 x-ui 风格）
# 功能：检测系统/架构 -> 安装 xray-core -> 获取 agent 二进制 -> 注册为 systemd 服务
# 用法：
#   交互：  bash install-agent.sh
#   非交互： NP_SERVER=http://1.2.3.4:8080 NP_TOKEN=xxxx NP_NODE_ID=1 bash install-agent.sh
#
# 环境变量（非交互模式）：
#   NP_SERVER      管理端基址，如 http://1.2.3.4:8080（必填）
#   NP_TOKEN       节点 token（管理端注册节点后返回，必填）
#   NP_NODE_ID     节点 id（必填）
#   NP_ADDR        agent 监听地址，默认 :8081（需与注册节点时 address 端口一致）
#   NP_XRAY        xray 二进制路径，默认 /usr/local/bin/xray
#   NP_CONFIG_DIR  xray 配置目录，默认 /opt/nodepilot-agent/xray
#   NP_INSTALL_DIR 安装目录，默认 /opt/nodepilot-agent
#   NP_BINARY_URL  自定义 agent 二进制 tar 包地址（覆盖默认 release 下载）

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
INSTALL_DIR="${NP_INSTALL_DIR:-/opt/nodepilot-agent}"
CONFIG_DIR="${NP_CONFIG_DIR:-$INSTALL_DIR/xray}"
ADDR="${NP_ADDR:-:8081}"
XRAY_BIN="${NP_XRAY:-/usr/local/bin/xray}"
SERVICE_NAME="nodepilot-agent"
BINARY_NAME="nodepilot-agent"

# ---------- 工具函数 ----------
confirm() {
  # $1 = 提示语; 读取 y/n，默认 y
  local prompt="$1"
  read -r -p "$prompt [Y/n]: " ans
  case "$ans" in
    [nN]) return 1 ;;
    *) return 0 ;;
  esac
}

detect_arch() {
  local a
  a=$(uname -m)
  case "$a" in
    x86_64|amd64) echo "amd64" ;;
    aarch64|arm64) echo "arm64" ;;
    *) red "不支持的架构: $a"; exit 1 ;;
  esac
}

need_root() {
  if [ "$(id -u)" -ne 0 ]; then
    red "请使用 root 运行本脚本"; exit 1
  fi
}

command_exists() { command -v "$1" >/dev/null 2>&1; }

# ---------- 步骤 ----------
install_xray() {
  if [ -x "$XRAY_BIN" ]; then
    green "xray 已存在于 $XRAY_BIN，跳过安装"
    return
  fi
  yellow "正在安装 xray-core（官方脚本）..."
  if command_exists curl; then
    bash <(curl -L https://github.com/XTLS/Xray-install/raw/main/install-release.sh) || {
      red "xray 安装失败，请手动安装到 $XRAY_BIN 后重试"; exit 1
    }
  else
    red "未找到 curl，无法安装 xray，请先安装 curl 或手动放置 xray 到 $XRAY_BIN"; exit 1
  fi
  if [ ! -x "$XRAY_BIN" ]; then
    red "xray 安装后仍未在 $XRAY_BIN 找到，请检查"; exit 1
  fi
  green "xray-core 安装完成: $($XRAY_BIN version 2>/dev/null | head -1)"
}

get_agent_binary() {
  mkdir -p "$INSTALL_DIR/bin"
  local dest="$INSTALL_DIR/bin/$BINARY_NAME"
  # 1) 本地已有（从项目目录运行脚本时）
  if [ -x "./bin/agent" ]; then
    green "使用本地 ./bin/agent"
    cp ./bin/agent "$dest"
    return
  fi
  # 2) 自定义 URL
  local url="${NP_BINARY_URL:-}"
  if [ -z "$url" ]; then
    local arch; arch=$(detect_arch)
    url="https://gitee.com/$REPO_OWNER/$REPO_NAME/releases/download/$RELEASE_TAG/$BINARY_NAME-linux-$arch.tar.gz"
  fi
  yellow "下载 agent 二进制: $url"
  if command_exists curl; then
    curl -L "$url" -o /tmp/$BINARY_NAME.tar.gz || true
  elif command_exists wget; then
    wget -O /tmp/$BINARY_NAME.tar.gz "$url" || true
  else
    red "未找到 curl/wget，无法下载"; exit 1
  fi
  if [ -s /tmp/$BINARY_NAME.tar.gz ]; then
    tar -xzf /tmp/$BINARY_NAME.tar.gz -C "$INSTALL_DIR/bin" && rm -f /tmp/$BINARY_NAME.tar.gz
    green "agent 二进制已下载并解压"
    return
  fi
  # 3) 回退：本地 go build
  red "下载失败，尝试本地 go build 回退..."
  if command_exists go; then
    green "使用 go build 生成 agent"
    (cd "$(dirname "$0")/.." && go build -o "$dest" ./cmd/agent) || {
      red "go build 失败，请手动将 agent 二进制放到 $dest"; exit 1
    }
    green "agent 已通过 go build 生成"
  else
    red "下载失败且未安装 Go，无法继续。请设置 NP_BINARY_URL 指向可用的 agent 二进制包。"; exit 1
  fi
  chmod +x "$dest"
}

prompt_config() {
  if [ -z "$NP_SERVER" ]; then
    read -r -p "管理端地址 (如 http://1.2.3.4:8080): " NP_SERVER
  fi
  if [ -z "$NP_TOKEN" ]; then
    read -r -p "节点 token (管理端注册节点后返回的 token): " NP_TOKEN
  fi
  if [ -z "$NP_NODE_ID" ]; then
    read -r -p "节点 id: " NP_NODE_ID
  fi
  [ -z "$NP_SERVER" ] && { red "管理端地址不能为空"; exit 1; }
  [ -z "$NP_TOKEN" ]  && { red "节点 token 不能为空"; exit 1; }
  [ -z "$NP_NODE_ID" ] && { red "节点 id 不能为空"; exit 1; }
}

write_service() {
  cat > /etc/systemd/system/$SERVICE_NAME.service <<EOF
[Unit]
Description=NodePilot Agent
After=network.target

[Service]
Type=simple
User=root
WorkingDirectory=$INSTALL_DIR
ExecStart=$INSTALL_DIR/bin/$BINARY_NAME \
  --token $NP_TOKEN \
  --node-id $NP_NODE_ID \
  --server $NP_SERVER \
  --addr $ADDR \
  --config-dir $CONFIG_DIR \
  --xray $XRAY_BIN
Restart=on-failure
RestartSec=3

[Install]
WantedBy=multi-user.target
EOF
}

install_agent() {
  need_root
  yellow "=== NodePilot Agent 安装 ==="
  install_xray
  get_agent_binary
  prompt_config
  mkdir -p "$CONFIG_DIR"
  write_service
  systemctl daemon-reload
  systemctl enable "$SERVICE_NAME"
  systemctl restart "$SERVICE_NAME"
  sleep 2
  if systemctl is-active --quiet "$SERVICE_NAME"; then
    green "NodePilot Agent 安装并启动成功！"
    blue "管理端: $NP_SERVER  节点ID: $NP_NODE_ID  监听: $ADDR"
    yellow "查看状态: systemctl status $SERVICE_NAME"
    yellow "查看日志: journalctl -u $SERVICE_NAME -f"
  else
    red "Agent 启动失败，请查看日志: journalctl -u $SERVICE_NAME -n 50"
    exit 1
  fi
}

uninstall_agent() {
  need_root
  yellow "=== 卸载 NodePilot Agent ==="
  systemctl stop "$SERVICE_NAME" 2>/dev/null || true
  systemctl disable "$SERVICE_NAME" 2>/dev/null || true
  rm -f /etc/systemd/system/$SERVICE_NAME.service
  systemctl daemon-reload
  rm -rf "$INSTALL_DIR"
  green "已卸载（安装目录 $INSTALL_DIR 已删除）"
}

show_status() {
  systemctl status "$SERVICE_NAME" --no-pager 2>/dev/null || red "服务未安装/未运行"
}

show_config() {
  if [ -f /etc/systemd/system/$SERVICE_NAME.service ]; then
    blue "=== $SERVICE_NAME 服务配置 ==="
    cat /etc/systemd/system/$SERVICE_NAME.service
  else
    red "服务未安装"
  fi
}

restart_agent() { systemctl restart "$SERVICE_NAME" && green "已重启"; }
stop_agent()    { systemctl stop "$SERVICE_NAME" && green "已停止"; }
start_agent()   { systemctl start "$SERVICE_NAME" && green "已启动"; }

# ---------- 菜单 ----------
menu() {
  echo
  blue "=== NodePilot Agent 管理脚本 ==="
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
    0) install_agent ;;
    1) start_agent ;;
    2) stop_agent ;;
    3) restart_agent ;;
    4) show_status ;;
    5) show_config ;;
    6) uninstall_agent ;;
    9) exit 0 ;;
    *) red "无效选择" ;;
  esac
}

# 非交互模式（提供必填环境变量时）直接安装后退出
if [ -n "$NP_SERVER" ] && [ -n "$NP_TOKEN" ] && [ -n "$NP_NODE_ID" ]; then
  install_agent
  exit 0
fi

if [ "$1" = "uninstall" ]; then
  uninstall_agent
  exit 0
fi

while true; do
  menu
done
