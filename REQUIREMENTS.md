# NodePilot — 项目需求文档

> 版本：v0.1（草稿） ｜ 最后更新：2026-08-09

## 1. 项目概述

- **项目名**：NodePilot
- **定位**：多服务器代理节点集中管理系统。一台管理服务器集中配置 / 下发 vmess 等多协议参数到 N 台节点服务器，节点运行代理服务，管理端统一管控。
- **架构模式**：控制面（管理服务器）+ 数据面（节点）分离。
- **范围假设**：
  - 单管理员模式（无多账号 / 角色）
  - 节点规模 < 10 台
  - 节点 = `xray-core` + `node-agent`；管理端主动调用节点 Agent API 下发（推模式）
  - 含 Web 控制台
- **参考**：vaxilu/x-ui（单节点面板），本项将其扩展为多节点集中管控。

## 2. 总体架构

```
管理员 ─HTTPS─> [管理服务器 Control Plane]
                   Web控制台(Vue3) + 管理API(Go/Gin)
                   + 中心DB(SQLite) + 配置生成器
                   + 下发调度器 + 探测调度器 + 订阅服务
                       │ HTTPS + Bearer Token (推 / 拉)
        ┌──────────────┼──────────────┐
   [节点1]          [节点2]         [节点N]
   node-agent+xray  node-agent+xray  node-agent+xray
```

- **控制面（管理服务器）**：节点 / 入站 / 用户 CRUD、配置生成、下发调度、连通性探测、订阅生成、数据存储。
- **数据面（节点）**：`node-agent` 接收配置 → 管理 `xray` 进程 → 上报心跳 / 流量。
- **数据流**：
  - 下行（推）：管理端生成 xray JSON → `PUT /agent/v1/config` → agent 写盘 → 热重载 → 回执。
  - 上行（报）：节点 agent 周期 `POST /api/v1/nodes/{id}/heartbeat`（状态）与 `/traffic`（流量）。

## 3. 技术选型

| 层 | 选型 | 说明 |
|----|------|------|
| 管理端后端 | Go (Gin / Fiber) | 单二进制易部署，与 x-ui 同栈 |
| 数据库 | SQLite | 小规模零运维 |
| 前端 | Vue3 + Element Plus | 参考 x-ui 交互 |
| 节点 agent | Go | 调用 / 管理 xray-core，单二进制跨平台 |
| 通信 | HTTPS REST + JSON，Bearer Token | 契合被动 API 接收（推）模式 |
| 热重载 | xray api reload（后期）/ systemd restart（初期） | 尽量不中断连接 |
| 证书 | lego（DNS-01 / Cloudflare） | 节点本地申请 Let's Encrypt |

## 4. 功能需求

### 4.1 认证（单用户）— P0
- [ ] 单管理员账号（首次启动初始化密码，无注册 / 多角色）
- [ ] 登录 / 登出（Session / Token）
- [ ] 修改管理员密码
- [ ] 2FA（TOTP）：P2 可选

### 4.2 节点管理 — P0（含连通性能力）
- [ ] 注册（名称、地址 `ip:port`、地域、标签）
- [ ] 列表 / 详情 / 启停 / 删除
- [ ] 在线状态（心跳判定）、资源看板（CPU / 内存 / xray 状态）
- [ ] 一键安装命令生成（含预分配节点 token）
- [ ] **端口范围设置**：每节点可配置可用端口范围（如 `10000-65535` 或 `10000-20000,30000-40000`），未配置用全局默认
- [ ] **连通性自检**（P0/P1）：探测调度器周期测试节点代理端口连通（L1 TCP / 可选 L2 代理握手）
- [ ] **自愈**（P1）：端口不通且 agent 在线 → 在端口范围内换端口 + 重发；多次失败 → 节点下线 + 预警
- [ ] 节点整体失联（心跳超时）→ 直接下线，不改端口

### 4.3 入站 / 协议管理 — P0
- [ ] 协议：vmess / vless / trojan / shadowsocks / socks / http
- [ ] 传输：tcp / ws / grpc；TLS；fallback
- [ ] 增删改查、启停；自愈可动态改 `port`

### 4.4 用户 / 客户端管理 — P0
- [ ] 每入站下管理 client（UUID 自动安全生成 / 可手动）
- [ ] 流量上限、到期时间、启用 / 禁用
- [ ] 连接信息导出（vmess:// 分享链接 / 二维码）：P1

### 4.5 订阅分组与订阅链接 — P1
- [ ] 订阅组：按筛选规则（节点 / 协议 / 标签）动态聚合 client
- [ ] 生成订阅链接 `GET /sub/{token}`
- [ ] 格式支持：vmess（base64）/ clash（YAML）/ sip008（JSON）
- [ ] 订阅内容随配置 / 自愈端口变更自动更新

### 4.6 配置下发 — P0（核心）
- [ ] 自动（增删改触发）/ 手动下发
- [ ] 管理端生成 xray JSON → `PUT /agent/v1/config` → agent 写盘 → 热重载
- [ ] 版本管理 + 一键回滚
- [ ] 失败回执标红

### 4.7 监控与统计 — P1
- [ ] 节点 / 用户流量（上行 / 下行）、按日聚合、排行、趋势图

### 4.8 预警通知 — P1
- [ ] 流量超额、到期、节点离线 / 下线预警
- [ ] 渠道：邮件 / Telegram Bot

### 4.9 证书管理 — P2
- [ ] 节点 agent 本地经 Cloudflare DNS-01 申请 Let's Encrypt
- [ ] inbound 通过 `cert_id` 引用（泛域名可多入站复用）
- [ ] 本地定时自动续签 + 热重载

### 4.10 运维 / 安装 — P0/P1
- [ ] 节点一键安装脚本（xray + agent + systemd）
- [ ] 管理端备份 / 恢复（SQLite 导出）
- [ ] agent 自更新：P2

### 4.11 Web 控制台 — P0
- [ ] 登录页、仪表盘、节点页、入站页、用户页、下发 / 版本页
- [ ] 订阅页（P1）、统计页（P1）、设置页

## 5. 数据模型（中心 DB / SQLite）

```text
Admin(id, username, password_hash)

Node(id, name, address "ip:port", region, tags[],
     token_hash, enabled, status[online|offline],
     connectivity[ok|degraded|offline], agent_version,
     last_heartbeat, port_range TEXT)        -- 如 "10000-65535" 或 "10000-20000,30000-40000"；NULL=用全局默认

Inbound(id, node_id, protocol, port, transport,
        tls{cert_id, enabled}, stream_settings, fallback,
        enabled, port_auto_fixed)

Client(id, inbound_id, uuid, email,
       traffic_limit_bytes, expire_time, enabled)

SubscriptionGroup(id, name, token, format[vmess|clash|sip008],
                  filters{node_ids[], protocol[], tags[]}, enabled)

ConfigVersion(id, node_id, version, content_json,
              status[applied|failed], applied_at, error)

Certificate(id, node_id, domain, cert_path, key_path, ca_path,
            auto_renew, expires_at, cf_email, cf_api_key_enc)

TrafficStat(id, node_id, inbound_id, client_id?, date, up_bytes, down_bytes)

SelfHealLog(id, node_id, attempts, last_action, result, time)

Setting(key="default_port_range", value="10000-65535")
```

## 6. 接口契约

### 6.1 管理端 API（Web UI + 节点调用）— 基址 `/api/v1`

| 方法 | 路径 | 鉴权 | 说明 |
|------|------|------|------|
| POST | `/auth/login` | 公开 | 登录，返回 token |
| POST | `/auth/logout` | Admin | 登出 |
| GET/POST | `/nodes` | Admin | 节点列表 / 注册 |
| GET/PATCH/DELETE | `/nodes/{id}` | Admin | 详情 / 改（含 port_range）/ 删 |
| GET/POST | `/nodes/{id}/inbounds` | Admin | 入站列表 / 新建 |
| PUT/DELETE | `/inbounds/{id}` | Admin | 改 / 删入站 |
| GET/POST | `/inbounds/{id}/clients` | Admin | 用户列表 / 新建 |
| PUT/DELETE | `/clients/{id}` | Admin | 改 / 删用户 |
| POST | `/nodes/{id}/config/sync` | Admin | 手动触发下发 |
| GET | `/nodes/{id}/config/versions` | Admin | 配置版本历史 |
| GET | `/nodes/{id}/connectivity` | Admin | 手动连通测试 |
| GET | `/nodes/{id}/selfheal/log` | Admin | 自愈记录 |
| POST/GET/DELETE | `/subscriptions` | Admin | 建 / 列 / 删分组 |
| GET/PATCH | `/subscriptions/{id}` | Admin | 详情 / 改筛选格式 |
| GET | `/sub/{token}` | token | 对外订阅端点（客户端拉取） |
| POST/GET | `/nodes/{id}/heartbeat` `/nodes/{id}/traffic` | Node Token | 节点上报状态 / 流量 |
| POST/GET | `/nodes/{id}/certs` `/certs/{id}` | Admin | 证书申请 / 列表 / 改 |
| GET/PUT | `/settings/default_port_range` | Admin | 全局默认端口范围 |

### 6.2 节点 Agent API — 基址 `https://{node}:{port}/agent/v1`（Bearer Node Token）

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/health` | 存活探测 |
| GET | `/status` | 节点资源 + xray 状态 |
| GET | `/config` | 当前生效配置 + 版本 |
| PUT | `/config` | 接收下发的 xray config JSON |
| POST | `/config/reload` | 热重载 xray |
| GET | `/traffic` | 当前流量明细 |
| GET | `/cert` | 列出节点证书及剩余天数 |
| POST | `/cert/issue` | 申请证书（domain + CF 凭据） |
| POST | `/cert/renew` | 续签指定证书 |

## 7. 关键业务流程

### 7.1 配置下发闭环
改配置 → 配置生成器从 DB 拼 xray JSON → `PUT /agent/v1/config` → agent 校验写盘 → `POST /config/reload` 热重载 → 回执（成功 / 失败→标红 / 可回滚上一版本重发）。

### 7.2 证书申请与匹配
节点 agent 本地用 lego 走 Cloudflare DNS-01 申请 → 证书存节点本地 `/root/cert/<domain>/` → DB 仅存路径与元数据 → inbound 用 `cert_id` 引用 → 生成 xray 时填入 `tlsSettings.certificates`；泛域名证书可多入站复用；本地定时续签 + reload。证书私钥仅存节点本地，跨机不传输。

### 7.3 订阅生成
`GET /sub/{token}` → 校验 token → 按 `SubscriptionGroup.filters` 聚合 client → 逐 client 生成 vmess:// / vless:// / trojan:// / ss:// → 按 `format` 编码（vmess=base64 串；clash=YAML；sip008=JSON）→ 返回。订阅端口取自 client 所属 inbound 当前 `port`（自愈改端口后自动同步）。

### 7.4 连通性自检与自愈
探测调度器周期测节点代理端口连通（L1 TCP / 可选 L2 代理握手）：
- **节点整体失联**（心跳超时）→ 直接下线，不改端口。
- **仅端口不通且 agent 在线** → 进入自愈：连续失败达阈值（如 3 次）触发；读取节点 `port_range`（空则用 `default_port_range`）；排除本节点其他 inbound 已占用端口得到候选集；从中选新端口 → 更新 `inbound.port` → 自动下发 → 热重载 → 重新探测；恢复则标记 ok 并同步订阅端口、通知「已自愈」；候选耗尽或达最大尝试次数（如 5）仍失败 → 节点 offline / 禁用 + 预警。

## 8. 非功能需求

- **规模**：< 10 节点，SQLite 单管理端即可，无需消息队列 / 高可用。
- **安全**：跨机通信 HTTPS + Token 鉴权；证书私钥仅存节点本地；CF API Key 加密存储；最小权限。
- **可用性**：管理端单点可接受；自愈避免误判（仅端口级故障改端口，节点级失联不下改端口）；端口范围过小会导致候选不足，建议保留足够余量。
- **易用性**：Web 控制台可视化配置。

## 9. 里程碑

- **MVP (P0)**：单管理员登录 → 注册节点（含端口范围）→ 建入站 + 用户 → 自动下发 + 热重载 → 状态回传 → 连通性测试 + 失败下线。
- **P1**：订阅分组、监控统计、预警、节点自愈换端口、客户端导出。
- **P2**：证书自动申请续签、2FA、agent 自更新。
