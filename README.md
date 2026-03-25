# tsd — Tailscale Service Discovery

`tsd` 是一个基于 Tailscale ACL Tag 的 Prometheus 服务发现工具。它由两个角色组成，编译为单一二进制文件：

| 角色 | 部署位置 | 职责 |
|------|----------|------|
| **Central** | Prometheus 所在节点 | 通过 Tailscale 发现带有指定 ACL Tag 的节点，从各节点的 Agent 获取服务列表，汇合后对外暴露 Prometheus `http_sd` 端点 |
| **Agent** | 其他 Tailscale 节点 | 暴露本节点的 Prometheus 抓取目标，支持静态配置、Push Bucket 和透明代理三种服务类型 |

```
                      ┌─ 定期刷新（refresh_interval）────────────────────────────┐
                      │                                                         │
                      ▼                                                         ▼
tailscaled ──ACL Tag 过滤──▶ Central（内存缓存） ──GET /api/v1/services──▶ Agent A
                                    │                                  └──▶ Agent B
                                    │
Prometheus ──GET /api/v1/sd──▶ 返回缓存（立即响应）
```

## 功能特性

- **ACL Tag 自动发现**：Central 通过 Tailscale LocalAPI 找到所有带有指定 Tag 的在线节点，无需手动维护 IP 列表
- **三种服务类型**（Agent 侧）：
  - **Static**：直接配置可被 Prometheus 访问的端点
  - **Push Bucket**：命名式 Pushgateway 容器，各 Bucket 独立互不干扰，每个 Bucket 对应一个独立的 Prometheus 抓取目标
  - **Proxy**：Agent 代理抓取本地服务（解决 `localhost` 不可公开的问题），支持 Bearer / Basic 透明鉴权
- **可选 Token 鉴权**：Central ↔ Agent 之间、Prometheus ↔ Central 之间均支持可选的 Bearer Token
- **跨平台管理 API**：Linux / macOS 使用 Unix Domain Socket，Windows 使用 Named Pipe，CLI 通过管理接口控制运行中的 daemon
- **运行时动态管理**：无需重启 daemon 即可通过 CLI 添加 / 删除服务、Bucket、Proxy

## 安装

**从源码构建**（需要 Go 1.22+）：

```bash
git clone https://github.com/lamgc/tailscale-service-discovery-agent
cd tailscale-service-discovery-agent
go build -o tsd ./cmd/tsd/
sudo mv tsd /usr/local/bin/tsd
```

Agent 和 Central 所在节点均需已安装并运行 Tailscale。

## 快速上手

### 1. 在每台被监控节点上部署 Agent

```bash
# 启动 Agent daemon（默认监听 :9001，使用内置默认值，无需配置文件）
tsd agent daemon

# 添加一个 Proxy 端点（代理本机 node-exporter，使其通过 Tailscale 可达）
tsd agent proxy add node-exporter \
  --target http://localhost:9100/metrics \
  --label job=node-exporter

# 确认服务已注册
tsd agent service list
```

### 2. 在 Prometheus 节点上部署 Central

```bash
# 创建配置文件
cat > central.toml << 'EOF'
[discovery]
tags = ["tag:prometheus-agent"]
agent_port = 9001
EOF

# 启动 Central daemon
tsd central daemon -c central.toml

# 查看已发现的 Agent 节点
tsd central discover
```

### 3. 配置 Prometheus

```yaml
scrape_configs:
  - job_name: tailscale_sd
    http_sd_configs:
      - url: http://localhost:9000/api/v1/sd
        refresh_interval: 60s
```

## 命令速查

```
tsd central daemon [-c central.toml]   启动 Central daemon
tsd central status                     查看 Central 运行状态
tsd central discover                   列出当前发现的 Agent 节点

tsd agent daemon [-c agent.toml]       启动 Agent daemon
tsd agent status                       查看 Agent 运行状态

tsd agent service add <name> -t host:port [-l key=value ...]
tsd agent service list
tsd agent service remove <name>

tsd agent bucket add <name> [-l key=value ...]
tsd agent bucket list
tsd agent bucket remove <name>
tsd agent bucket clear <name>

tsd agent proxy add <name> -t http://localhost:PORT/metrics \
    [--auth-type bearer --token TOKEN] \
    [--auth-type basic --username U --password P] \
    [-l key=value ...]
tsd agent proxy list
tsd agent proxy remove <name>
```

## 详细文档

- [快速入门教程](docs/getting-started.md) — 从安装到完整部署，覆盖 ACL Tag 自动发现与手动配置对端两种场景
- [配置文件参考](docs/configuration.md) — Central 和 Agent 的完整 TOML 配置说明
- [Agent 使用指南](docs/agent.md) — 三种服务类型详解、Push Bucket 推送示例
- [Central 使用指南](docs/central.md) — Tailscale ACL Tag 设置、Prometheus 集成

## 许可证

本项目遵循 MIT 许可证开源。

```plaintext
MIT License

Copyright (c) 2026 LamGC

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.

```
