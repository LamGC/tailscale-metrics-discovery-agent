# Agent 使用指南

Agent 部署在需要被 Prometheus 监控的节点上。它暴露一个 HTTP 端点，Central 定期查询该端点以获取服务列表。

---

## 启动 Agent

```bash
# 使用默认配置（监听 :9001，无鉴权）
tsd agent daemon

# 指定配置文件
tsd agent daemon -c /etc/tsd/agent.toml

# 后台运行（systemd 托管，见下方）
```

启动成功后会输出：
```
agent: HTTP server listening on :9001
agent: management socket at /tmp/tsd-agent.sock
```

### systemd 服务示例

```ini
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

---

## 查看运行状态

```bash
tsd agent status
```

---

## 管理静态服务

静态服务直接将配置的地址上报给 Central，**Prometheus 须能直接访问这些地址**。

```bash
# 添加单个目标
tsd agent service add node-exporter \
  --target 10.100.0.2:9100 \
  --label job=node-exporter \
  --label env=prod

# 添加多个目标（同一服务的多个实例）
tsd agent service add redis-cluster \
  --target 10.100.0.10:9121 \
  --target 10.100.0.11:9121 \
  --label job=redis

# 列出所有已注册服务
tsd agent service list

# 删除
tsd agent service remove node-exporter
```

---

## 管理 Push Bucket

Push Bucket 是命名式的 Pushgateway 容器。每个 Bucket 独立存储推送数据，自动成为一个 Prometheus 抓取目标（`/bucket/<name>/metrics`）。

### 创建 Bucket

```bash
tsd agent bucket add my-app --label job=my-app --label env=prod
tsd agent bucket add batch-jobs --label job=batch
```

### 向 Bucket 推送指标

推送接口兼容 Prometheus Pushgateway API：

```bash
# 基本推送
curl -X PUT http://localhost:9001/push/my-app/job/worker \
  --data-binary @- << 'EOF'
# HELP task_duration_seconds Duration of task
# TYPE task_duration_seconds gauge
task_duration_seconds{step="fetch"} 1.23
task_duration_seconds{step="process"} 4.56
EOF

# 带 instance 分组（不同 instance 互不覆盖）
curl -X PUT http://localhost:9001/push/my-app/job/worker/instance/host-1 \
  --data-binary 'completed_tasks_total 42'

curl -X PUT http://localhost:9001/push/my-app/job/worker/instance/host-2 \
  --data-binary 'completed_tasks_total 18'

# 删除某个 job/instance 组
curl -X DELETE http://localhost:9001/push/my-app/job/worker/instance/host-1
```

同一 Bucket 内，相同 `job`/`instance` 的推送会**替换**上一次数据；不同 `job`/`instance` 互不干扰。

### 查看 Bucket 当前指标

```bash
curl http://localhost:9001/bucket/my-app/metrics
```

### 清空 Bucket

```bash
# 通过 CLI
tsd agent bucket clear my-app

# 或通过 API
curl -X DELETE http://localhost:9001/push/my-app/job/worker
```

### 删除 Bucket

```bash
tsd agent bucket remove my-app
```

---

## 管理 Proxy 端点

Proxy 端点解决以下问题：

1. **localhost 不可公开**：服务只监听 `127.0.0.1`，无法通过 Tailscale 直接访问
2. **透明鉴权**：目标服务需要 Bearer Token 或 HTTP Basic Auth，但不想在 Prometheus 中配置

Agent 代理 Prometheus 的抓取请求，向本地目标发起实际请求（可携带鉴权），将响应透传回 Prometheus。

### 添加 Proxy（无鉴权）

```bash
tsd agent proxy add node-exporter \
  --target http://localhost:9100/metrics \
  --label job=node-exporter
```

### Bearer Token 鉴权

```bash
tsd agent proxy add caddy \
  --target http://localhost:2019/metrics \
  --auth-type bearer \
  --token "my-secret-token" \
  --label job=caddy
```

### HTTP Basic Auth

```bash
tsd agent proxy add private-app \
  --target http://localhost:8080/metrics \
  --auth-type basic \
  --username prometheus \
  --password "s3cr3t" \
  --label job=private-app \
  --label env=prod
```

### 验证 Proxy 是否工作

```bash
curl http://localhost:9001/proxy/node-exporter/metrics
```

### 删除 Proxy

```bash
tsd agent proxy remove node-exporter
```

---

## 查看所有服务

`service list`、`bucket list`、`proxy list` 均显示同一个注册表（所有类型）：

```bash
tsd agent service list
```

输出示例：
```
NAME              TYPE    TARGETS
node-exporter     proxy   100.64.0.2:9001/proxy/node-exporter/metrics
my-app            bucket  100.64.0.2:9001/bucket/my-app/metrics
remote-redis      static  10.0.0.10:9121, 10.0.0.11:9121
```

---

## 服务的 Tailscale IP 自动检测

Agent 启动时会自动从 Tailscale daemon 获取本机的 Tailscale IP，并将其用于 Bucket 和 Proxy 的 SDTarget 地址（`<tailscale-ip>:<port>/bucket/...`）。

如果 Tailscale 未运行，Agent 将回退到使用监听地址（如 `:9001`），此时 Central 可能无法正确拼接可访问的 URL。确保在启动 Agent 之前 tailscaled 已经运行。

---

## 自身指标（Self-Metrics）

Agent 内置 Prometheus 指标端点，暴露自身的运行状态。

### 默认行为

指标端点默认挂载在主端口（`:9001/metrics`）：

```bash
curl http://localhost:9001/metrics
```

### 配置示例

```toml
[self_metrics]
enabled = true
path    = "/metrics"

# 可选：单独监听一个端口，不占用主端口
# listen = ":9102"

# 将自身指标端点加入服务列表，Central 会将其纳入 SD 输出
register_self = true
[self_metrics.labels]
  job = "tsd-agent"
```

### 暴露的指标

| 指标名 | 类型 | 说明 |
|--------|------|------|
| `tsd_agent_services{type="static\|bucket\|proxy"}` | Gauge | 各类型已注册服务数量 |
| Go 标准指标 (`go_*`, `process_*`) | — | Go 运行时与进程级别指标 |

### 使用 `register_self` 让 Prometheus 自动发现 Agent 指标

开启后，`/api/v1/services` 会额外返回一个指向 Agent 自身指标端点的 SDTarget。Central 在下次刷新时将其纳入 SD 列表，Prometheus 自动发现并抓取，无需手动配置 `scrape_configs`。

---

## nodeAttrs ACL 鉴权

Agent 支持通过 Tailscale ACL `nodeAttrs` 自动验证请求方身份（基于 ACL Tag），无需配置 Bearer token 即可实现安全访问控制。详见 [nodeAttrs 集中配置](node-attrs.md)。

---

## 配置文件 vs 运行时 CLI

| 操作方式 | 持久化 | 说明 |
|----------|--------|------|
| 配置文件（`agent.toml`） | 是，重启后生效 | `[[static]]`、`[[bucket]]`、`[[proxy]]` 节 |
| CLI（`tsd agent service/bucket/proxy add`） | 否，仅当前 daemon 生命周期内有效 | 通过管理 socket 动态修改运行时状态 |

**最佳实践**：生产环境使用配置文件声明固定服务；临时调试或动态管理使用 CLI。
