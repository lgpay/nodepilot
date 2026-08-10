# NodePilot — 项目需求文档

> 版本：v0.1（草稿） ｜ 最后更新：2026-08-10

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
| 证书 | lego（DNS-01 / Cloudflare） | 管理端统一申请泛域名证书并分发到节点（节点本地不申请） |

## 4. 功能需求

### 4.1 认证（单用户）— P0
- [x] 单管理员账号（种子初始化，无注册 / 多角色）
- [x] 登录 / 登出（Token）
- [x] 修改管理员密码（`POST /api/v1/auth/change-password`，需校验旧密码 + bcrypt）
- [ ] 2FA（TOTP）：P2 可选

### 4.2 节点管理 — P0（含连通性能力）
- [x] 注册（名称、地址 `ip:port`、地域（自动识别国家+城市并显示中文+国旗）、标签）
- [x] 列表 / 详情 / 启停 / 删除
- [x] 在线状态（心跳判定）
- [ ] 资源看板（CPU / 内存 / xray 状态，agent 已上报但未在 Web 展示）
- [ ] 一键安装命令生成（含预分配节点 token）— Web 暂无，节点 Token 已支持手工录入
- [x] **端口范围设置**：每节点可配置可用端口范围（如 `10000-65535` 或 `10000-20000,30000-40000`），未配置用全局默认
- [x] **连通性自检**（P0/P1）：探测调度器周期测试节点代理端口连通（L1 TCP）
- [ ] 手动连通测试端点 `GET /nodes/{id}/connectivity` — P1 未实现（自动探测调度已具备）
- [x] **自愈**（P1）：端口不通且 agent 在线 → 在端口范围内换端口 + 重发（间隔分钟级，0=不自愈）；多次失败 → 节点下线 + 预警
- [x] 节点整体失联（心跳超时）→ 直接下线，不改端口
- [x] 月流量控制（上限字节；90% 提醒 / 100% 自动关闭节点；0=不限）— `monthly_traffic_bytes` + `GET /nodes/:id/traffic`

### 4.3 入站 / 协议管理 — P0
- [x] 协议：vmess / vless / trojan / shadowsocks / socks / http
- [x] 传输：tcp / ws / grpc；TLS；fallback
- [x] 增删改查、启停；自愈可动态改 `port`

### 4.4 用户 / 客户端管理 — P0
- [x] 每入站下管理 client（UUID 自动安全生成 / 可手动）
- [x] 流量上限、到期时间、启用 / 禁用
- [ ] 连接信息导出（vmess:// 分享链接 / 二维码）：P1 未实现（订阅内已含 vmess/clash 等完整配置）

### 4.5 订阅分组与订阅链接 — P1
- [x] 订阅组：按筛选规则（节点 / 协议 / 标签）动态聚合 client
- [x] 生成订阅链接 `GET /sub/{token}`
- [x] **模式（mode）**：裸订阅（仅节点）/ ACL4SSR（Clash/Loon/Surfboard 附分流规则，V2Ray 格式退化为裸链接）
- [x] **格式（format）**：vmess(V2Ray, base64) / clash(YAML) / surfboard(Clash YAML) / loon(.conf) / sip008(JSON)
- [x] **精确选择入站**：入站支持「名称/别名」字段；订阅分组按 `inbound_ids` **多选具体入站**（按别名勾选），原 `node_ids/protocol/tags` 筛选保留为兜底兼容
- [x] **订阅二维码**：订阅详情页展示订阅链接二维码（`GET /qr/:token` 公开 PNG，go-qrcode 生成），手机客户端扫码导入
- [x] ACL4SSR：内置 17 个 rule-provider（BanAD 等）+ 6 分组 + 15 路由规则；Loon 用内置最小规则（GEOIP,CN,DIRECT / FINAL）
- [x] 订阅内容随配置 / 自愈端口变更自动更新（端口取自 client 所属 inbound 当前 port）

### 4.6 配置下发 — P0（核心）
- [x] 自动（增删改触发）/ 手动下发 `POST /nodes/{id}/config/sync`
- [x] 管理端生成 xray JSON → `PUT /agent/v1/config` → agent 写盘 → 热重载（`POST /config/reload`）
- [x] 版本管理 `GET /nodes/{id}/config/versions`（auto-sync 自动补发）
- [ ] 一键回滚到历史版本 — P0 未实现（版本记录已存，缺回滚接口与按钮）
- [ ] 失败回执标红 — Web 暂未对失败下发版本做红色高亮
- [x] 注：节点月流量控制（见 4.2）由 `/alert` 扫描器驱动；证书随下发自动分发（见 4.9）

### 4.7 监控与统计 — P1（已实现基础版）
- [x] 节点 / 用户流量（上行 / 下行）：agent 周期查询 xray stats API（`user>>>` 按用户计数）并上报
- [x] 按天聚合（`TrafficStat`，唯一键 node+inbound+client+date，增量累加）
- [x] Web 统计页：今日卡片、按节点 / 按客户端表（含用量百分比）、近 30 天趋势条；`GET /api/v1/stats/overview`
- [ ] 排行 / 更精细趋势图（当前为 CSS 条形，未做图表库）

### 4.8 预警通知 — P1
- [x] 渠道：邮件（SMTP，支持 465 隐式 TLS / 587 STARTTLS）、企业微信（自建应用消息）、Telegram Bot
- [x] 事件：节点离线（心跳超时 / 自愈耗尽）、节点已自愈（换端口恢复）、节点恢复在线、流量超额、客户端到期 / 临近到期（提前 3 天）
- [x] 多渠道同时推送（`notify.Dispatch` 异步扇出到所有「启用」渠道），单渠道失败仅记录日志不阻塞
- [x] 管理方式：通知渠道 CRUD（`GET/POST /notifiers`、`GET/PATCH/DELETE /notifiers/:id`）+ 即时测试（`POST /notifiers/:id/test`）；Web 侧边栏「通知」分组管理
- [x] 流量超额 / 到期由 15 分钟定时扫描器触发，按「client:原因:日期」每日起重去重；节点事件为状态切换时触发（不刷屏）
- 注：企业微信用自建应用消息（corpid + corpsecret + agentid + touser），非群机器人 webhook。

### 4.9 证书管理 — P2（已实现，采用简化后的「管理端统一签发 + 分发」方案）
- [x] 管理端统一经 Cloudflare DNS-01 申请 Let's Encrypt（lego，单个泛域名证书覆盖全网）
- [x] 签发后通过 `PUT /agent/v1/cert` 分发到所有已启用节点（证书文件落节点本地 `/opt/nodepilot-agent/certs/`）
- [x] inbound 通过 `cert_id` 引用（泛域名可多入站复用）
- [x] 到期前 30 天定时自动续签 + 重分发（24h 调度）
- [x] CF Token AES-GCM 加密存储，证书私钥仅存节点本地，跨机不传输

### 4.10 运维 / 安装 — P0/P1
- [ ] 节点一键安装脚本（xray + agent + systemd）— Web/CLI 暂未提供，依赖手工部署
- [ ] 管理端备份 / 恢复（SQLite 导出）— P0 未实现
- [ ] agent 自更新：P2 未实现

### 4.11 Web 控制台 — P0
- [x] 登录页、仪表盘、节点页、入站页、用户页、下发 / 版本页、通知页
- [x] 订阅页（P1）
- [x] 统计页（P1）
- [ ] 设置页 — P0 未实现（`/settings` 端点与系统设置 UI 缺失；通知渠道管理在侧边栏「通知」内）

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

Client(id, inbound_id, uuid, alias,
       traffic_limit_bytes, expire_time, enabled)

SubscriptionGroup(id, name, token, format[vmess|clash|sip008],
                  filters{node_ids[], protocol[], tags[]}, enabled)

ConfigVersion(id, node_id, version, content_json,
              status[applied|failed], applied_at, error)

Certificate(id, domain, cert_path, key_path, ca_path,
            auto_renew, expires_at, cf_email, cf_token_enc, status, last_error)

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
| GET/POST | `/certs` | Admin | 证书列表 / 申请（签发 + 分发） |
| GET/DELETE | `/certs/{id}` | Admin | 证书详情 / 删除 |
| POST | `/certs/{id}/renew` | Admin | 续签并分发 |
| POST | `/certs/{id}/distribute` | Admin | 重分发到所有节点 |
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
| PUT | `/cert` | 接收管理端分发的证书文件（cert_pem/key_pem/ca_pem） |

## 7. 关键业务流程

### 7.1 配置下发闭环
改配置 → 配置生成器从 DB 拼 xray JSON → `PUT /agent/v1/config` → agent 校验写盘 → `POST /config/reload` 热重载 → 回执（成功 / 失败→标红 / 可回滚上一版本重发）。

### 7.2 证书申请与匹配
管理端统一用 lego 走 Cloudflare DNS-01 申请**泛域名**证书 → 产物存管理端 `/opt/nodepilot/certs/` → 经 `PUT /agent/v1/cert` 分发到所有已启用节点（证书落节点本地 `/opt/nodepilot-agent/certs/`）→ DB（`Certificate`）仅存节点端路径与元数据 → inbound 用 `cert_id` 引用 → 生成 xray 时填入 `tlsSettings.certificates`；泛域名证书可多入站复用；管理端到期前 30 天定时续签并自动重分发。CF Token 加密存储；证书私钥仅存节点本地，跨机不传输。

### 7.3 订阅生成
`GET /sub/{token}` → 校验 token → 按 `SubscriptionGroup.filters` 聚合 client → 逐 client 生成 vmess:// → 按 `format`×`mode` 编码：
- `vmess`=base64 串（`mode=acl4ssr` 时退化为裸链接）；
- `clash`/`surfboard`=YAML（surfboard 复用 Clash 结构）；`mode=acl4ssr` 时附加 ACL4SSR 的 17 个 rule-provider + 6 分组 + 15 路由规则；
- `loon`=.conf（`[Proxy]`/`[Proxy Group]`，`mode=acl4ssr` 时附加内置最小规则 `GEOIP,CN,DIRECT` + `FINAL,NodePilot`）；
- `sip008`=JSON（`mode=acl4ssr` 退化为裸 servers）。
订阅端口取自 client 所属 inbound 当前 `port`（自愈改端口后自动同步）；xray stats/email 键固定为 client UUID，别名仅作展示（ps）。
- **精确选择**：入站表新增 `name`（别名）；订阅 `filters` 支持 `inbound_ids`（多选具体入站），UI 按节点分组勾选入站（标签显示别名或 protocol:port）；未指定 inbound_ids 时回退 `node_ids/protocol/tags`。
- **二维码**：`GET /qr/:token`（公开，与 `/sub/:token` 同安全模型）用 go-qrcode 编码 `scheme://host/api/v1/sub/<token>` 返回 PNG；订阅详情页 `<img>` 展示。

### 7.4 连通性自检与自愈
探测调度器周期测节点代理端口连通（L1 TCP / 可选 L2 代理握手）：
- **节点整体失联**（心跳超时）→ 直接下线，不改端口。
- **仅端口不通且 agent 在线** → 进入自愈：连续失败达阈值（如 3 次）触发；读取节点 `port_range`（空则用 `default_port_range`）；排除本节点其他 inbound 已占用端口得到候选集；从中选新端口 → 更新 `inbound.port` → 自动下发 → 热重载 → 重新探测；恢复则标记 ok 并同步订阅端口、通知「已自愈」；候选耗尽或达最大尝试次数（如 5）仍失败 → 节点 offline / 禁用 + 预警。

### 7.5 预警通知
`notify.Dispatch(title, body)` → 读 `notification_channels` 中所有 `enabled` 渠道 → 异步扇出。无外部依赖（SMTP 用 `net/smtp`+`crypto/tls`；企微/TG 用 `net/http`）。
- **渠道（Send 接口实现）**：
  - `email`：`smtp_port==465` 走 `tls.Dial` 隐式 TLS，否则 `smtp.Dial`+`StartTLS`；配置 `smtp_host/smtp_port/smtp_user/smtp_pass/from/to[]`。
  - `wecom`：自建应用消息，先 `gettoken` 取 `access_token`（内存缓存 ~7000s）再 `message/send`；配置 `corpid/corpsecret/agentid/touser/toparty/totag`。
  - `tg`：Bot API `sendMessage`（Markdown，失败去 parse_mode 重试）；配置 `bot_token/chat_id`。
- **触发点**：`probe.go` 在节点离线（`notifyOffline`）、自愈成功（`notifyHealed`）、状态切回 ok（`notifyRecovery`，每恢复仅一次）调用；`alert.go` 的 15 分钟扫描器处理流量超额（`TrafficStat` 累计 vs `Client.TrafficLimitBytes>=0`）与客户端到期/临近到期，按 `client:原因:日期` 每日起重去重防刷屏。

## 8. 非功能需求

- **规模**：< 10 节点，SQLite 单管理端即可，无需消息队列 / 高可用。
- **安全**：跨机通信 HTTPS + Token 鉴权；证书私钥仅存节点本地；CF API Key 加密存储；最小权限。
- **可用性**：管理端单点可接受；自愈避免误判（仅端口级故障改端口，节点级失联不下改端口）；端口范围过小会导致候选不足，建议保留足够余量。
- **易用性**：Web 控制台可视化配置。

## 9. 里程碑

- **MVP (P0)**：单管理员登录 → 注册节点（含端口范围）→ 建入站 + 用户 → 自动下发 + 热重载 → 状态回传 → 连通性测试 + 失败下线。
- **P1**：订阅分组、监控统计、预警、节点自愈换端口、客户端导出。
- **P2**：证书自动申请续签、2FA、agent 自更新。
