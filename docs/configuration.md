# 配置文件参考

`tsd` 使用 TOML 格式的配置文件。两个角色各有独立的配置文件，均支持热加载（`POST /reload` 管理接口）。

---

## Central 配置（`central.toml`）

```toml
# ── HTTP 服务 ──────────────────────────────────────────────────────────────
[server]
# Prometheus 访问 /api/v1/sd 所用的监听地址
listen = ":9000"

# 可选：Prometheus 查询 /api/v1/sd 时必须携带的 Bearer Token
# 留空表示不启用鉴权
token = ""

# ── Tailscale 连接 ─────────────────────────────────────────────────────────
[tailscale]
# tailscaled Unix socket 路径，留空则使用系统默认值
# Linux 默认: /var/run/tailscale/tailscaled.sock
# macOS 默认: /var/run/tailscale/tailscaled.sock
socket = ""

# ── 节点发现 ───────────────────────────────────────────────────────────────
[discovery]
# 需要匹配的 Tailscale ACL Tag 列表（满足其一即算命中）
tags = ["tag:prometheus-agent"]

# Agent 监听的 TCP 端口
agent_port = 9001

# Central 重新查询 Tailscale 和各 Agent 的间隔时间
refresh_interval = "60s"

# 可选：查询 Agent /api/v1/services 时携带的 Bearer Token
# 需与 Agent 配置中的 server.token 保持一致
agent_token = ""

# 是否启用 Tailscale nodeAttrs 自动配置（默认 true）
# 设为 true 时，Central 从自身的 Tailscale nodeAttrs 读取 Agent Tag 和端口，
# 覆盖上方的 tags 和 agent_port；设为 false 则完全使用本地配置
node_attrs = true

# ── 管理 API ───────────────────────────────────────────────────────────────
[management]
# 管理 socket 路径
# Linux/macOS: Unix Domain Socket 文件路径
# Windows: Named Pipe 路径，格式 \\.\pipe\<name>
socket = "/tmp/tsd-central.sock"

# ── 自身指标 ───────────────────────────────────────────────────────────────
[self_metrics]
# 是否暴露 Central 自身的 Prometheus 指标端点
enabled = true

# 指标端点路径（默认 /metrics）
path = "/metrics"

# 可选：指标端点单独监听的地址
# 留空则挂载到 server.listen（主端口）上
# listen = ":9102"

# 是否将 Central 自身的指标端点加入 /api/v1/sd 输出
# 开启后 Prometheus 会自动发现并抓取 Central 的指标
register_self = false

# 附加到自注册 SDTarget 的标签（仅 register_self = true 时有效）
# [self_metrics.labels]
#   job = "tsd-central"
```

### 字段说明

| 字段 | 默认值 | 说明 |
|------|--------|------|
| `server.listen` | `:9000` | HTTP 监听地址，支持 `host:port` 或 `:port` |
| `server.token` | `""` | 留空不鉴权；设置后 Prometheus 需在请求头中携带 `Authorization: Bearer <token>` |
| `tailscale.socket` | `""` | 留空使用平台默认路径 |
| `discovery.tags` | `["tag:prometheus-agent"]` | ACL Tag 列表，节点满足其中任意一个即被发现 |
| `discovery.agent_port` | `9001` | 所有 Agent 使用同一端口（当前设计） |
| `discovery.refresh_interval` | `60s` | 支持 Go duration 格式：`30s`、`2m`、`1h` |
| `discovery.agent_token` | `""` | 对应 Agent 的 `server.token` |
| `discovery.node_attrs` | `true` | 是否从 Tailscale nodeAttrs 自动读取发现配置，详见 [nodeAttrs 文档](node-attrs.md) |
| `management.socket` | `/tmp/tsd-central.sock` | CLI 通过此 socket 与 daemon 通信 |
| `self_metrics.enabled` | `true` | 是否暴露 `/metrics` 端点 |
| `self_metrics.path` | `/metrics` | 指标端点路径 |
| `self_metrics.listen` | `""` | 留空则挂载到主端口；设置后单独监听（如 `:9102`） |
| `self_metrics.register_self` | `false` | 是否将自身指标端点加入 SD 输出，供 Prometheus 自动发现 |
| `self_metrics.labels` | `{}` | 附加到自注册 SDTarget 的额外标签 |

---

## Agent 配置（`agent.toml`）

```toml
# ── HTTP 服务 ──────────────────────────────────────────────────────────────
[server]
# Agent 监听地址，需与 Central 的 discovery.agent_port 一致
listen = ":9001"

# 可选：Central 查询 /api/v1/services 时必须携带的 Bearer Token
token = ""

# 是否启用 Tailscale nodeAttrs 自动鉴权（默认 true）
# 设为 true 时，Agent 从自身的 Tailscale nodeAttrs 读取授权的 Central ACL Tag，
# 通过 WhoIs 验证请求方身份；设为 false 则仅使用 Bearer token 鉴权
node_attrs = true

# 当 ACL Tag 鉴权已启用但未提供 Bearer token 时，是否允许匿名访问（默认 false）
# 仅在 nodeAttrs 自动配置读取到有效配置时生效；否则此项无效
# false：ACL Tag 不匹配的请求必须提供匹配的 Bearer token，否则 401（安全默认）
# true：恢复为完全开放访问，ACL Tag 仅作访问优化而非强制门禁
allow_anonymous = false

# ── 管理 API ───────────────────────────────────────────────────────────────
[management]
socket = "/tmp/tsd-agent.sock"

# ── 自身指标 ───────────────────────────────────────────────────────────────
[self_metrics]
enabled = true
path    = "/metrics"
# listen = ":9102"   # 留空则挂载到主端口 :9001
register_self = false
# [self_metrics.labels]
#   job = "tsd-agent"

# ── 静态服务（可配置多个） ──────────────────────────────────────────────────
# 直接告知 Central 某个外部可达端点，Prometheus 将直接抓取该地址。
# 适合目标本身在 Tailscale 网络中可达的情况。
[[static]]
name = "node-exporter-remote"
targets = ["10.0.0.5:9100"]
[static.labels]
  job = "node-exporter"
  env  = "prod"

# ── Push Bucket（可配置多个） ───────────────────────────────────────────────
# 每个 Bucket 是一个独立的 Pushgateway 容器，拥有独立的 /metrics 端点。
# Bucket 内部按标准 job/instance 分组，互不覆盖。
# Prometheus 抓取目标自动设为：<agent-tailscale-ip>:<port>/bucket/<name>/metrics
[[bucket]]
name = "my-app"
[bucket.labels]
  job = "my-app"
  env = "prod"

# ── Proxy 端点（可配置多个） ────────────────────────────────────────────────
# Agent 代理抓取本地服务，Prometheus 抓取目标为：
# <agent-tailscale-ip>:<port>/proxy/<name>/metrics
[[proxy]]
name = "caddy"
target = "http://localhost:2019/metrics"
[proxy.auth]
  type  = "bearer"   # none | bearer | basic
  token = "my-caddy-token"
[proxy.labels]
  job = "caddy"

[[proxy]]
name = "private-app"
target = "http://localhost:8080/metrics"
[proxy.auth]
  type     = "basic"
  username = "prometheus"
  password = "secret"
[proxy.labels]
  job = "private-app"
  env = "prod"
```

### 服务类型说明

#### `[[static]]` — 静态服务

| 字段 | 必填 | 说明 |
|------|------|------|
| `name` | 是 | 服务唯一名称 |
| `targets` | 是 | 目标地址列表，格式 `host:port` |
| `labels` | 否 | 附加到 SDTarget 的标签 |

直接将 `targets` 列表传给 Prometheus，**Prometheus 须能直接访问这些地址**。适合目标本身就在 Tailscale 网络中可达的情况。

#### `[[bucket]]` — Push Bucket

| 字段 | 必填 | 说明 |
|------|------|------|
| `name` | 是 | Bucket 唯一名称 |
| `labels` | 否 | 附加到 SDTarget 的标签 |

Bucket 启动后自动注册为一个 Prometheus 抓取目标（`/bucket/<name>/metrics`）。本地进程通过 Pushgateway 兼容 API 推送指标：

```
PUT  /push/<bucket>/job/<job>[/instance/<instance>]
POST /push/<bucket>/job/<job>[/instance/<instance>]
```

同一 Bucket 内，不同 `job`/`instance` 组合的数据**互不覆盖**；相同组合的推送会**替换**上一次数据（与标准 Pushgateway 行为一致）。

#### `[[proxy]]` — Proxy 端点

| 字段 | 必填 | 说明 |
|------|------|------|
| `name` | 是 | Proxy 唯一名称 |
| `target` | 是 | 上游 metrics URL |
| `labels` | 否 | 附加到 SDTarget 的标签 |

**`[proxy.auth]` 字段：**

| 字段 | 说明 |
|------|------|
| `type` | `none`（默认）、`bearer`、`basic` |
| `token` | Bearer Token（`type = "bearer"` 时使用） |
| `username` | 用户名（`type = "basic"` 时使用） |
| `password` | 密码（`type = "basic"` 时使用） |

Proxy 端点注册为 `/proxy/<name>/metrics`，当 Prometheus 发起抓取时，Agent 向上游发起请求（可携带鉴权信息）并将响应透传回来。

### Agent `[self_metrics]` 字段说明

| 字段 | 默认值 | 说明 |
|------|--------|------|
| `self_metrics.enabled` | `true` | 是否暴露 `/metrics` 端点 |
| `self_metrics.path` | `/metrics` | 指标端点路径 |
| `self_metrics.listen` | `""` | 留空则挂载到主端口；设置后单独监听（如 `:9102`） |
| `self_metrics.register_self` | `false` | 是否将自身指标端点加入 `/api/v1/services` 输出，Central 会将其纳入 SD 列表供 Prometheus 发现 |
| `self_metrics.labels` | `{}` | 附加到自注册 SDTarget 的额外标签 |

---

## 管理 socket 路径默认值

| 平台 | Central | Agent |
|------|---------|-------|
| Linux / macOS | `/tmp/tsd-central.sock` | `/tmp/tsd-agent.sock` |
| Windows | `\\.\pipe\tsd-central` | `\\.\pipe\tsd-agent` |

如果 daemon 以非 root 用户运行，建议将 socket 路径设置在用户可写的目录下（如 `~/.local/run/`）。
