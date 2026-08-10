# NodePilot

> 多节点代理集中管理系统（控制面 + 数据面）

NodePilot 让你用**一台管理服务器**集中配置 / 下发 `vmess` 等多协议参数到**多台节点服务器**，节点运行代理服务，管理端统一管控。参考 [x-ui](https://github.com/vaxilu/x-ui)，并将其扩展为多节点集中管控。

## 特性

- 单管理员 Web 控制台（JWT 登录）
- 节点管理：注册、状态看板、端口范围设置
- 多协议入站：`vmess` / `vless` / `trojan` / `shadowsocks` / `socks` / `http`
- 用户 / 客户端管理：UUID 自动生成、流量上限、到期时间
- 配置下发（推模式）：管理端生成 xray 配置 → 节点 agent 落盘 → 热重载
- 节点心跳上报与在线状态展示
- 配置版本记录与回滚

## 架构

```
管理员 ─HTTPS─> [管理服务器 Control Plane]
                   Web控制台 + 管理API(Go/Gin)
                   + SQLite + 配置生成器 + 下发调度器
                       │ HTTPS + Bearer Token (推)
        ┌──────────────┼──────────────┐
   [节点1]          [节点2]         [节点N]
   node-agent+xray  node-agent+xray  node-agent+xray
```

- **控制面（管理服务器）**：节点 / 入站 / 用户 CRUD、配置生成、下发调度、数据存储
- **数据面（节点）**：`node-agent` 接收配置 → 管理 `xray` 进程 → 上报心跳 / 状态

## 目录结构

```
nodepilot/
├─ cmd/
│  ├─ server/main.go          # 管理端入口 (:8080)
│  └─ agent/main.go           # 节点 agent 入口 (:54321)
├─ internal/
│  ├─ model/                  # GORM 实体
│  ├─ store/                  # SQLite 初始化
│  ├─ auth/                   # JWT / 节点 token
│  ├─ server/                 # 路由与 handler
│  ├─ configgen/              # DB → xray config.json
│  └─ agent/                  # agent HTTP + xray 管理
├─ web/index.html             # 极简 Web 控制台
└─ REQUIREMENTS.md            # 项目需求文档
```

## 快速开始

### 构建

```bash
go mod tidy
go build -o bin/server ./cmd/server
go build -o bin/agent  ./cmd/agent
```

### 运行管理端

```bash
./bin/server        # 监听 :8080
```

首次启动会初始化默认管理员账号 **admin**，并**随机生成密码**（不再使用固定 `admin123`）。
随机密码会打印到控制台与服务端日志（`journalctl -u nodepilot -n 50`），登录后**强制要求立即修改密码**，
未修改前除「修改密码」外的所有接口均返回 `403`。

浏览器打开 `http://<管理端IP>:8080`，用 `web/index.html` 或直接调用 API 操作。

### 密钥与安全配置

硬编码的密钥已全部移除，改为环境变量 / 持久化密钥文件：

| 环境变量 | 说明 | 默认 |
|----------|------|------|
| `NP_JWT_SECRET` | JWT 签名密钥（HS256）。未设置时在数据库同目录生成随机密钥文件 `.nodepilot_jwt_secret`（权限 `0600`）并持久化 | 随机生成 |
| `NP_MASTER_KEY` | AES-256 主密钥，用于加密 Cloudflare API Token 与节点 token。未设置时在数据库同目录生成 `.nodepilot_master_key` | 随机生成 |
| `NP_AGENT_TLS_VERIFY` | 管理端↔agent 通信是否校验 TLS 证书（`true`/`false`）。默认 `false`（MVP 兼容，跳过校验） | `false` |

> 生产环境建议：设置上述密钥环境变量（或备份好密钥文件），并将 `NP_AGENT_TLS_VERIFY=true`（配合可信证书）。
> 节点 token 在数据库内仅以 `sha256` 哈希 + AES 加密形式存储，不再保存明文。

### 运行节点 agent

先在管理端「注册节点」拿到节点 `token` 与 `id`，然后在节点机器上：

```bash
./bin/agent \
  --token <节点TOKEN> \
  --node-id <节点ID> \
  --server http://<管理端IP>:8080 \
  --addr :54321 \
  --config-dir /usr/local/xray \
  --xray /usr/local/bin/xray
```

agent 会周期上报心跳；在管理端对节点点击「下发配置」即可把入站 / 用户推送并热重载。

## 一键部署

### 管理端（控制面）部署

也可使用一键脚本（自动下载二进制 + 注册 systemd 服务，运行目录隔离）：

```bash
bash <(curl -L https://github.com/lgpay/nodepilot/raw/main/scripts/install-server.sh)
```

手动部署步骤（与一键脚本等价）：

```bash
# 1) 准备运行目录并放入二进制
mkdir -p /opt/nodepilot/{bin,data,logs}
cp bin/server /opt/nodepilot/bin/server
cp -r web /opt/nodepilot/web

# 2) 写 systemd 单元
cat > /etc/systemd/system/nodepilot.service <<'EOF'
[Unit]
Description=NodePilot Control Plane
After=network.target

[Service]
Type=simple
User=root
WorkingDirectory=/opt/nodepilot/data
ExecStart=/bin/sh -c '/opt/nodepilot/bin/server --web-dir /opt/nodepilot/web >> /opt/nodepilot/logs/server.log 2>&1'
Restart=on-failure
RestartSec=3

[Install]
WantedBy=multi-user.target
EOF

# 3) 启动
systemctl daemon-reload
systemctl enable --now nodepilot
```

启动后访问 `http://<管理端IP>:8080`（控制台）与 `/api/v1/*`（API）。
可选参数：`--db <路径>` 指定数据库位置、`--addr :9000` 改监听端口。

### 节点 agent 一键安装

提供 x-ui 风格的一键脚本 `scripts/install-agent.sh`，自动完成：检测架构 → 安装 xray-core → 获取 agent 二进制 → 注册为 systemd 服务。

```bash
# 交互式（按提示填管理端地址 / 节点 token / 节点 id）
bash <(curl -L https://github.com/lgpay/nodepilot/raw/main/scripts/install-agent.sh)

# 非交互式 / 批量
NP_SERVER=http://<管理端IP>:8080 NP_TOKEN=<节点TOKEN> NP_NODE_ID=1 \
  bash install-agent.sh
```

环境变量可覆盖：`NP_ADDR`（默认 `:8081`，须与注册节点时 `address` 端口一致）、`NP_XRAY`、`NP_CONFIG_DIR`、`NP_INSTALL_DIR`、`NP_BINARY_URL`。
管理菜单：`bash install-agent.sh` 可选 安装 / 启动 / 停止 / 重启 / 状态 / 配置 / 卸载；`bash install-agent.sh uninstall` 卸载。

> agent 二进制从 Gitee Release `v0.1.0` 下载（当前仓库为 public）。私有化部署可设置 `NP_BINARY_URL` 指向自托管地址。

## API 概览（基址 `/api/v1`）

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/auth/login` | 管理员登录，返回 JWT |
| GET/POST | `/nodes` | 节点列表 / 注册（返回 token） |
| GET/PATCH/DELETE | `/nodes/{id}` | 节点详情 / 改 / 删 |
| GET/POST | `/nodes/{id}/inbounds` | 入站列表 / 新建 |
| PUT/DELETE | `/inbounds/{id}` | 改 / 删入站 |
| GET/POST | `/inbounds/{id}/clients` | 用户列表 / 新建 |
| PUT/DELETE | `/clients/{id}` | 改 / 删用户 |
| POST | `/nodes/{id}/config/sync` | 触发向该节点下发配置 |
| GET | `/nodes/{id}/config/versions` | 配置下发历史 |
| POST | `/nodes/{id}/heartbeat` | 节点心跳上报（节点 token） |

节点 agent 暴露 `PUT /agent/v1/config`（接收下发）、`GET /agent/v1/health`、`GET /agent/v1/status`。

## 已知限制

- 管理端与 agent 间 MVP 使用 HTTP + `InsecureSkipVerify`，生产应启用 HTTPS 并校验证书
- 默认管理员密码与 JWT secret 为开发占位，需改为环境变量 / 配置
- 节点 agent 热重载采用重启 xray 进程（秒级中断），后续可升级为 xray api reload
- 节点 agent 一键脚本会自动安装 xray-core（官方 XTLS 脚本）
- 尚未实现：监控统计报表、预警通知、2FA

## 路线图

**P1（已完成）**

- 订阅分组与订阅链接（vmess / clash / sip008）
- 节点连通性自检与端口自愈（端口范围内换端口，多次失败下线）
- agent 一键部署脚本
- 管理员密码修改（`POST /api/v1/auth/change-password`，需校验旧密码）

**P2（已实现）**

- 证书管理：管理端统一通过 Let's Encrypt + Cloudflare DNS-01 签发**泛域名**证书，自动分发到所有节点（`PUT /agent/v1/cert`），入站通过 `cert_id` 引用；含到期自动续签调度。CF Token 加密存储，证书私钥仅落节点本地。

**P1（待完成，此前误列入 P2）**

- 监控与统计（节点/用户流量、按日聚合、排行、趋势图）
- 预警通知（邮件 / Telegram Bot）

**P2（待做）**

- 安全强化：HTTPS、2FA

## 许可证

详见仓库 LICENSE（待补充）。
