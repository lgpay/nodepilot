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

首次启动会初始化默认管理员账号 **admin / admin123**（请尽快修改）。

浏览器打开 `http://<管理端IP>:8080`，用 `web/index.html` 或直接调用 API 操作。

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
- 需在各节点预置 `xray` 二进制（`/usr/local/bin/xray`）
- 尚未实现：订阅分组、监控统计报表、预警通知、节点连通性自愈换端口、证书自动申请续签、2FA

## 路线图（P1 / P2）

- 订阅分组与订阅链接（vmess / clash / sip008）
- 节点连通性自检与端口自愈（端口范围内换端口，多次失败下线）
- 监控统计、预警通知（邮件 / Telegram）
- 证书管理（Let's Encrypt + Cloudflare DNS-01）
- 安全强化：HTTPS、密码修改、2FA

## 许可证

详见仓库 LICENSE（待补充）。
