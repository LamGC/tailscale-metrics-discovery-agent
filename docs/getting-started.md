# 快速入门：从零部署到 Prometheus 自动发现

本教程覆盖两种典型部署场景：

- **场景一：ACL Tag 自动发现**（推荐）—— Central 通过 Tailscale ACL Tag 自动找到所有 Agent 节点
- **场景二：手动配置对端**—— 不依赖 ACL Tag，或需要为特定节点覆盖端口

> **前置条件**：所有节点已安装并运行 Tailscale（`tailscaled`），且节点之间在同一 tailnet 中互相可达。

---

## 第一步：安装 tsd

在 **每台**需要部署 Central 或 Agent 的节点上安装 `tsd` 二进制文件。

### 从源码构建（需要 Go 1.22+）

```bash
git clone https://github.com/lamgc/tailscale-service-discovery-agent
cd tailscale-service-discovery-agent
go build -o tsd ./cmd/tsd/
sudo mv tsd /usr/local/bin/tsd
```

### 验证安装

```bash
tsd --help
```

---

## 场景一：ACL Tag 自动发现

Central 通过 Tailscale LocalAPI 持续监听 tailnet 状态，自动发现所有带有指定 ACL Tag 的节点。节点上线时立即加入，下线时自动移除，无需人工维护 IP 列表。

```
  tailscaled
      │  ACL Tag 过滤
      ▼
  Central ──────────────── Agent A（tag:prometheus-agent）
      │                    Agent B（tag:prometheus-agent）
      ▼
  Prometheus（/api/v1/sd）
```

### 1.1 在 Tailscale 管理控制台配置 ACL Tag

在 [Tailscale 管理控制台](https://login.tailscale.com/admin/acls) 的 ACL 策略文件中声明 Tag：

```json
{
  "tagOwners": {
    "tag:prometheus-agent":  ["autogroup:admin"],
    "tag:prometheus-central": ["autogroup:admin"]
  },
  "acls": [
    {
      "action": "accept",
      "src": ["tag:prometheus-central"],
      "dst": ["tag:prometheus-agent:9001"]
    }
  ]
}
```

> `acls` 中的规则确保 Central 节点可以访问 Agent 节点的 9001 端口。若 tailnet 默认策略为全通，可跳过此步。

### 1.2 为 Agent 节点打 Tag

使用预授权 Key 注册节点时指定 Tag：

```bash
tailscale up --authkey=tskey-auth-xxx --advertise-tags=tag:prometheus-agent
```

或在管理控制台的 Machines 页面手动为已有节点添加 Tag。

### 1.3 在被监控节点上部署 Agent

**最小配置启动**（无需配置文件，使用内置默认值）：

```bash
tsd agent daemon
```

启动后 Agent 监听 `:9001`，等待 Central 查询。

**若需要配置鉴权或自定义端口**，创建 `/etc/tsd/agent.toml`：

```toml
[server]
listen = ":9001"
token  = "my-agent-secret"   # Central 查询时须携带此 Token
```

```bash
tsd agent daemon -c /etc/tsd/agent.toml
```

### 1.4 注册需要监控的服务t

Agent 支持三种服务类型，可通过 CLI 动态添加，或写入配置文件在启动时加载。

**Proxy（最常用）**：代理本地不可公开的服务：

```bash
# node-exporter 只监听 127.0.0.1，通过 proxy 让 Central 可访问
tsd agent proxy add node-exporter \
  --target http://localhost:9100/metrics \
  --label job=node-exporter

# 带 Bearer Token 的本地服务
tsd agent proxy add caddy \
  --target http://localhost:2019/metrics \
  --auth-type bearer \
  --token "caddy-metrics-token" \
  --label job=caddy
```

**Push Bucket**：用于短生命周期任务（cron job、批处理）推送指标：

```bash
tsd agent bucket add batch-jobs --label job=batch
# 推送指标
curl -X PUT http://localhost:9001/push/batch-jobs/job/nightly-backup \
  --data-binary 'backup_duration_seconds 42'
```

**Static**：目标本身在 Tailscale 网络中可直接访问时使用：

```bash
tsd agent service add remote-exporter \
  --target 100.64.0.99:9100 \
  --label job=node-exporter
```

确认注册成功：

```bash
tsd agent service list
```

### 1.5 在 Prometheus 节点上部署 Central

创建 `/etc/tsd/central.toml`：

```toml
[server]
listen = ":9000"

[discovery]
tags             = ["tag:prometheus-agent"]
agent_port       = 9001
refresh_interval = "60s"
agent_token      = "my-agent-secret"   # 与 agent.toml 中的 server.token 一致
```

启动：

```bash
tsd central daemon -c /etc/tsd/central.toml
```

验证发现结果：

```bash
tsd central discover
```

输出示例：
```
HOSTNAME        TAILSCALE IP  PORT  SOURCE  HEALTH  TAGS
web-server-01   100.64.0.5    9001  auto    ok      tag:prometheus-agent
db-server-01    100.64.0.8    9001  auto    ok      tag:prometheus-agent
```

### 1.6 配置 Prometheus

```yaml
scrape_configs:
  - job_name: tailscale_sd
    http_sd_configs:
      - url: http://localhost:9000/api/v1/sd
        refresh_interval: 65s   # 略大于 Central 的 refresh_interval
```

验证 SD 端点：

```bash
curl http://localhost:9000/api/v1/sd | jq .
```

至此，Prometheus 会自动发现并抓取所有已注册服务的指标。

---

## 场景二：手动配置对端

当以下情况之一成立时，使用手动配置：

- Agent 节点没有（或无法设置）ACL Tag
- Agent 使用了非标准端口
- 需要在 Central 与已知 IP 的节点之间建立连接，不依赖 Tailscale tag 发现

手动对端与 ACL Tag 自动发现**并行工作**，可以混用。

### 2.1 通过 CLI 动态添加对端

Central daemon 运行时，使用 `peer add` 命令添加：

```bash
# 使用默认端口（discovery.agent_port）
tsd central peer add 100.64.0.20

# 使用非标准端口并设置别名
tsd central peer add 100.64.0.20 --port 9100 --name "special-host"
```

查看所有手动配置的对端：

```bash
tsd central peer list
```

移除对端：

```bash
tsd central peer remove 100.64.0.20
```

> **注意**：CLI 动态添加的对端仅在当前 daemon 运行期间有效，重启后失效。

### 2.2 在配置文件中静态声明对端

将对端写入 `central.toml`，重启后永久生效：

```toml
[server]
listen = ":9000"

[discovery]
tags       = []        # 不使用 ACL Tag 自动发现，留空即可
agent_port = 9001

# 手动声明对端（可配置多个）
[[peer]]
name    = "web-server-01"
address = "100.64.0.5"
# port 留空则使用 discovery.agent_port

[[peer]]
name    = "special-host"
address = "100.64.0.20"
port    = 9100          # 覆盖默认端口
```

### 2.3 混合模式：自动发现 + 手动覆盖

ACL Tag 自动发现和手动配置可以同时启用。若同一 IP 同时出现在两处，**手动配置的端口优先**：

```toml
[discovery]
tags       = ["tag:prometheus-agent"]
agent_port = 9001

# 此节点已被自动发现，但使用了非标准端口，手动覆盖
[[peer]]
address = "100.64.0.20"
port    = 9100
```

`tsd central discover` 输出中 SOURCE 列会区分来源：

```
HOSTNAME      TAILSCALE IP  PORT  SOURCE  HEALTH  TAGS
web-01        100.64.0.5    9001  auto    ok      tag:prometheus-agent
special-host  100.64.0.20   9100  manual  ok
```

---

## 第二步：配置鉴权（可选）

### Prometheus ↔ Central 鉴权

```toml
# central.toml
[server]
token = "prom-to-central-secret"
```

```yaml
# prometheus.yml
http_sd_configs:
  - url: http://localhost:9000/api/v1/sd
    authorization:
      credentials: "prom-to-central-secret"
```

### Central ↔ Agent 鉴权

```toml
# central.toml
[discovery]
agent_token = "central-to-agent-secret"

# agent.toml（每台 Agent）
[server]
token = "central-to-agent-secret"
```

---

## 第三步：配置为系统服务

### Agent（systemd）

```ini
# /etc/systemd/system/tsd-agent.service
[Unit]
Description=tsd Agent
After=network.target tailscaled.service

[Service]
ExecStart=/usr/local/bin/tsd agent daemon -c /etc/tsd/agent.toml
Restart=on-failure
RestartSec=5s

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now tsd-agent
sudo systemctl status tsd-agent
```

### Central（systemd）

```ini
# /etc/systemd/system/tsd-central.service
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

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now tsd-central
```

---

## 运维参考

### 健康检查

```bash
curl http://localhost:9000/healthz   # Central
curl http://localhost:9001/healthz   # Agent
# 正常返回: {"ok":true}
```

### 查看 Agent 当前注册的服务

```bash
tsd agent service list
```

### 实时查看 Central 发现的节点及健康状态

```bash
tsd central discover
```

健康状态说明：

| 状态 | 含义 |
|------|------|
| `ok` | Agent 正常响应 |
| `offline` | Tailscale 节点未上线，无法连接 |
| `timeout` | Tailscale 节点在线，但 Agent 端口无响应（未启动或被防火墙拦截） |
| `unauthorized` | Agent 在线但 Token 不匹配（HTTP 401/403） |
| `unknown` | 尚未完成首次查询 |

### 验证完整链路

```bash
# 1. Agent 服务列表（Central 看到的原始数据）
curl http://localhost:9001/api/v1/services | jq .

# 2. Central SD 输出（Prometheus 看到的数据）
curl http://localhost:9000/api/v1/sd | jq .

# 3. 确认 Prometheus 已加载 targets
# 访问 Prometheus Web UI → Status → Targets
```

---

## 常见问题

**Q：Central 发现了节点但 health 是 `timeout`？**

Agent daemon 未启动，或防火墙阻断了 Central 到 Agent 9001 端口的流量。检查 Tailscale ACL 规则，确保 Central 节点被允许访问 Agent 的 9001 端口。

**Q：Agent 的 Bucket/Proxy SDTarget 地址用的是 `:9001` 而不是 Tailscale IP？**

`tailscaled` 未运行，Agent 无法自动检测 Tailscale IP，回退到监听地址。确保在 Agent 启动之前 `tailscaled` 已经在运行。

**Q：手动添加的对端重启 Central 后消失了？**

CLI 动态添加（`tsd central peer add`）只在当前 daemon 生命周期内有效。生产环境请将对端写入 `central.toml` 的 `[[peer]]` 节。

**Q：想让 Prometheus 也监控 tsd 自身的运行状态？**

在 `central.toml` 和 `agent.toml` 中开启 `self_metrics.register_self = true`，tsd 自身的 `/metrics` 端点会自动出现在 SD 列表中，Prometheus 无需额外配置。详见 [配置文件参考](configuration.md)。
