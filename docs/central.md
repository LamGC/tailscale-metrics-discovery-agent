# Central 使用指南

Central 部署在 Prometheus 所在节点，负责通过 Tailscale 发现 Agent 节点并汇总服务列表，对外提供 Prometheus `http_sd` 端点。

---

## 前置条件

### 1. 为 Agent 节点打 ACL Tag

在 Tailscale 管理控制台（或 `tailnet policy` 文件）中为需要被发现的节点设置 ACL Tag：

```json
{
  "tagOwners": {
    "tag:prometheus-agent": ["autogroup:admin"]
  }
}
```

为节点打 Tag（可在节点的 `auth key` 中指定，或在管理控制台手动设置）：

```bash
# 使用预授权 Key 注册节点时指定 Tag
tailscale up --authkey=tskey-auth-xxx --advertise-tags=tag:prometheus-agent
```

### 2. 确认 Central 节点可访问 Agent 节点

Central 会通过 Tailscale IP 向各 Agent 发起 HTTP 请求。在 Tailscale ACL 中需确保此流量被允许：

```json
{
  "acls": [
    {
      "action": "accept",
      "src": ["tag:prometheus-central"],
      "dst": ["tag:prometheus-agent:9001"]
    }
  ]
}
```

---

## 启动 Central

```bash
# 最小配置（使用默认值：发现 tag:prometheus-agent，Agent 端口 9001）
tsd central daemon

# 指定配置文件
tsd central daemon -c /etc/tsd/central.toml
```

启动成功后输出：
```
central: HTTP server listening on :9000
central: management socket at /tmp/tsd-central.sock
```

Central 启动后立即执行一次发现和服务收集，此后每隔 `refresh_interval`（默认 60s）重复执行。

### systemd 服务示例

```ini
[Unit]
Description=tsd Central
After=network.target tailscaled.service

[Service]
ExecStart=/usr/local/bin/tsd central daemon -c /etc/tsd/central.toml
Restart=on-failure
RestartSec=5s

[Install]
WantedBy=multi-user.target
```

---

## 查看运行状态

```bash
# 确认 daemon 运行中
tsd central status

# 列出当前所有 Agent 节点
tsd central discover

# 强制关闭颜色输出（如重定向到文件）
tsd central discover --color=false
```

`discover` 输出示例：
```
HOSTNAME        TAILSCALE IP  PORT  SOURCE  HEALTH        TAGS
web-server-01   100.64.0.5    9001  auto    ok            tag:prometheus-agent
db-server-01    100.64.0.8    9001  auto    timeout       tag:prometheus-agent,tag:db
old-server      100.64.0.9    9001  auto    offline       tag:prometheus-agent
special-host    100.64.0.11   9002  manual  unauthorized
```

健康状态说明：

| 状态 | 颜色 | 含义 |
|------|------|------|
| `ok` | 绿色 | Agent 正常响应 |
| `offline` | 灰色 | Tailscale 节点未上线，无法建立连接 |
| `timeout` | 黄色 | Tailscale 节点在线，但 Agent 端口无响应（未启动或防火墙拦截） |
| `unauthorized` | 红色 | Agent 在线但 Token 不匹配（HTTP 401/403） |
| `unknown` | 默认 | 尚未完成首次查询 |

---

## 配置 Prometheus

### 基本配置（无鉴权）

```yaml
scrape_configs:
  - job_name: tailscale_sd
    http_sd_configs:
      - url: http://localhost:9000/api/v1/sd
        refresh_interval: 60s
```

### 带 Bearer Token（`server.token` 已配置时）

```yaml
scrape_configs:
  - job_name: tailscale_sd
    http_sd_configs:
      - url: http://localhost:9000/api/v1/sd
        refresh_interval: 60s
        authorization:
          credentials: "your-central-token"
```

### 验证 SD 端点

```bash
curl http://localhost:9000/api/v1/sd
```

正常响应示例：
```json
[
  {
    "targets": ["100.64.0.5:9001/proxy/node-exporter/metrics"],
    "labels": {
      "job": "node-exporter",
      "__tsd_service_name": "node-exporter",
      "__tsd_service_type": "proxy"
    }
  },
  {
    "targets": ["100.64.0.5:9001/bucket/my-app/metrics"],
    "labels": {
      "job": "my-app",
      "__tsd_service_name": "my-app",
      "__tsd_service_type": "bucket"
    }
  }
]
```

---

## 手动配置对端

当某个 Agent 节点的端口与 `discovery.agent_port` 不同，或需要管理一个不在 ACL Tag 发现范围内的节点时，可以通过 CLI 手动添加对端。手动对端与 ACL 自动发现的对端并行工作：若同一 IP 同时出现在两处，手动配置的端口优先。

```bash
# 添加手动对端（使用默认端口 9001）
tsd central peer add 100.64.0.11

# 添加使用非标准端口的对端
tsd central peer add 100.64.0.11 --port 9002 --name special-host

# 查看所有手动配置的对端
tsd central peer list

# 删除
tsd central peer remove 100.64.0.11
```

也可以在 `central.toml` 中静态配置，重启后生效：

```toml
[[peer]]
name    = "special-host"
address = "100.64.0.11"
port    = 9002
```

> **注意**：通过 CLI 动态添加的对端仅在当前 daemon 运行期间有效，重启后失效。生产环境建议写入配置文件。

---

## 鉴权配置

### Central ↔ Prometheus 鉴权

在 `central.toml` 中设置 `server.token`，Prometheus 的 `http_sd_configs` 和 `scrape_configs` 均需携带该 Token：

```toml
# central.toml
[server]
token = "prom-to-central-secret"
```

### Central ↔ Agent 鉴权

在 `central.toml` 中设置 `discovery.agent_token`，同时在每台 Agent 的 `agent.toml` 中设置相同的 `server.token`：

```toml
# central.toml
[discovery]
agent_token = "central-to-agent-secret"

# agent.toml（每台 Agent）
[server]
token = "central-to-agent-secret"
```

---

## 发现机制说明

Central 采用**主动后台刷新**模式：发现和数据收集完全独立于 Prometheus 的查询，在后台持续运行。Prometheus 请求 `/api/v1/sd` 时，Central 直接返回已整理好的缓存，无需等待任何网络操作。

### 实时事件监听（WatchIPNBus）

Central 启动后立即通过 Tailscale `WatchIPNBus` 订阅 tailscaled 的状态变化。当任意节点**上线或离线**时，tailscaled 推送网络图变更，Central 收到后**立即**触发一次完整刷新，无需等到下一个 `refresh_interval`。

若连接中断（如 tailscaled 重启），5 秒后自动重连，期间由周期轮询兜底。

### 后台周期刷新循环（每隔 `refresh_interval`）

1. 调用 Tailscale LocalAPI 获取当前 tailnet 中所有对端节点
2. 过滤出带有 `discovery.tags` 中**任意一个** Tag 的节点
3. 合并手动配置的对端，手动配置的端口优先
4. 对所有 Tailscale **在线**节点并发发起 `GET /api/v1/services` 请求（超时 10s），记录健康状态
5. 汇总来自健康（`ok`）节点的 `[]SDTarget`，原子地替换内存缓存

### Prometheus 查询（随时）

- `GET /api/v1/sd` 直接读取内存缓存并返回，响应延迟极低
- Prometheus 的 `refresh_interval` 控制的是 Prometheus 来取的频率，与 Central 的后台刷新频率**相互独立**

因此建议将 Prometheus `http_sd_configs.refresh_interval` 设置为略大于 Central `discovery.refresh_interval` 的值，以确保 Prometheus 始终能拿到最新数据：

```toml
# central.toml
[discovery]
refresh_interval = "30s"
```

```yaml
# prometheus.yml
http_sd_configs:
  - url: http://localhost:9000/api/v1/sd
    refresh_interval: 35s   # 略大于 Central 的刷新间隔
```

节点离线后，下一次后台刷新时 Tailscale LocalAPI 不再返回该节点，其服务自动从缓存中消失。

---

## 自身指标（Self-Metrics）

Central 内置 Prometheus 指标端点，暴露自身的运行状态，方便监控 Central 是否正常工作。

### 默认行为

指标端点默认挂载在主端口（`:9000/metrics`），可直接访问：

```bash
curl http://localhost:9000/metrics
```

### 配置示例

```toml
[self_metrics]
# 关闭指标端点
# enabled = false

# 指标路径（默认 /metrics）
path = "/metrics"

# 可选：单独监听一个端口，不占用主端口
# listen = ":9102"

# 将 Central 自身加入 SD 输出，让 Prometheus 自动发现并抓取
register_self = true
[self_metrics.labels]
  job = "tsd-central"
```

### 暴露的指标

| 指标名 | 类型 | 说明 |
|--------|------|------|
| `tsd_central_peers{health="ok\|offline\|timeout\|unauthorized\|unknown"}` | Gauge | 各健康状态的 Agent 节点数 |
| `tsd_central_sd_targets_total` | Gauge | 当前缓存的 SD 目标总数 |
| Go 标准指标 (`go_*`, `process_*`) | — | Go 运行时与进程级别指标 |

### 使用 `register_self` 让 Prometheus 自动发现 Central

开启后，`/api/v1/sd` 会额外返回一个指向 Central 自身指标端点的 SDTarget。Prometheus 通过该端点自动抓取 Central 的运行指标，无需手动配置 `scrape_configs`。

---

## 标签说明

tsd 会向每个服务的 SDTarget 自动附加以下内部标签：

| 标签 | 说明 |
|------|------|
| `__tsd_service_name` | 服务在 Agent 中的注册名称 |
| `__tsd_service_type` | 服务类型：`static`、`bucket`、`proxy` |

这些标签以 `__` 开头，在 Prometheus 抓取后会被自动丢弃（不进入时序数据库）。可以在 Prometheus `relabel_configs` 中使用它们进行筛选或重命名：

```yaml
scrape_configs:
  - job_name: tailscale_sd
    http_sd_configs:
      - url: http://localhost:9000/api/v1/sd
    relabel_configs:
      # 只抓取 proxy 和 bucket 类型的服务
      - source_labels: [__tsd_service_type]
        regex: "proxy|bucket"
        action: keep
```
