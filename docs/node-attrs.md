# Tailscale nodeAttrs 集中配置

tsd 支持通过 Tailscale ACL `nodeAttrs` 集中管理 Central 和 Agent 的配置，实现"配好 ACL 即就绪"的零本地配置体验。

---

## 概述

通过在 Tailscale ACL 策略的 `nodeAttrs` 中设置自定义能力（`custom:tsd-*`），管理员可以：

- **Central**：自动获取要发现的 Agent ACL Tag 和端口，无需本地配置 `discovery.tags` / `discovery.agent_port`
- **Agent**：自动获取授权的 Central ACL Tag，通过 Tailscale WhoIs 验证请求方身份，无需配置 Bearer token

---

## ACL 策略配置

### 完整示例

```json
{
  "tagOwners": {
    "tag:prometheus-central": ["autogroup:admin"],
    "tag:prometheus-agent": ["autogroup:admin"],
    "tag:metrics-agent": ["autogroup:admin"]
  },
  "nodeAttrs": [
    {
      "target": ["tag:prometheus-central"],
      "attr": [
        "custom:tsd-agent-tag=tag:prometheus-agent",
        "custom:tsd-agent-tag=tag:metrics-agent",
        "custom:tsd-agent-port=9001"
      ]
    },
    {
      "target": ["tag:prometheus-agent", "tag:metrics-agent"],
      "attr": [
        "custom:tsd-central-tag=tag:prometheus-central",
        "custom:tsd-agent-port=9001"
      ]
    }
  ],
  "acls": [
    {
      "action": "accept",
      "src": ["tag:prometheus-central"],
      "dst": ["tag:prometheus-agent:9001", "tag:metrics-agent:9001"]
    }
  ]
}
```

### 能力键说明

| 能力键前缀 | 读取方 | 含义 |
|-----------|--------|------|
| `custom:tsd-agent-tag=TAG` | Central | 该 Central 应发现哪些 ACL Tag 的 Agent（可设置多条） |
| `custom:tsd-agent-port=PORT` | Central / Agent | Agent 监听端口 |
| `custom:tsd-central-tag=TAG` | Agent | 哪些 Central ACL Tag 有权访问该 Agent（可设置多条） |

---

## 有效性规则

nodeAttrs 必须满足以下条件才被视为有效，否则整体回退到本地配置文件：

| 角色 | 必要条件 | 缺失时行为 |
|------|---------|-----------|
| Central | `custom:tsd-agent-port` + 至少 1 个 `custom:tsd-agent-tag` | 使用 `central.toml` 中的 `discovery.tags` 和 `discovery.agent_port` |
| Agent | `custom:tsd-agent-port` + 至少 1 个 `custom:tsd-central-tag` | 仅使用 Bearer token 鉴权 |

---

## Central 行为

当 nodeAttrs 有效时，Central 自动：
- 用 `custom:tsd-agent-tag` 的值**覆盖** `discovery.tags`
- 用 `custom:tsd-agent-port` 的值**覆盖** `discovery.agent_port`

### 多 Tag 匹配

配置多个 `custom:tsd-agent-tag` 后，同一节点可能命中多个 Tag。`tsd central discover` 会合并显示所有命中的 Tag：

```
HOSTNAME        TAILSCALE IP  PORT  SOURCE  HEALTH  TAGS
server1         100.64.0.5    9001  auto    ok      tag:prometheus-agent, tag:metrics-agent
db-server       100.64.0.8    9001  auto    ok      tag:prometheus-agent
```

---

## Agent 鉴权行为

当 nodeAttrs 有效时，Agent 的认证流程为：

1. **WhoIs 验证**（优先）：通过 Tailscale `WhoIs` API 获取请求方的 ACL Tag，检查是否与 `custom:tsd-central-tag` 匹配
   - 匹配 → 允许访问
2. **Bearer token 兜底**（当 WhoIs 失败或 Tag 不匹配时）：
   - Token 已配置且匹配 → 允许访问
   - Token 已配置但不匹配 → 拒绝 (401)
   - Token 未配置 且 `allow_anonymous=true` → 允许访问
   - Token 未配置 且 `allow_anonymous=false`（**默认**）→ 拒绝 (401)

### allow_anonymous 配置项

当 ACL Tag 鉴权已启用（读取到有效 nodeAttrs）时，`allow_anonymous` 控制是否允许未授权节点访问：

```toml
# agent.toml
[server]
# ACL Tag 未匹配且无 token 时是否允许访问
# 默认 false：必须通过 ACL Tag 或 Bearer token 中的至少一种验证
# 设为 true：恢复为完全开放访问（ACL Tag 只作"快速通道"）
allow_anonymous = false
```

- **默认值 `false`**：配好 ACL Tag 后，未授权节点必须提供 Bearer token 才能访问
- **设为 `true`**：保留开放访问，ACL Tag 只是一种快速通道，不要求强制鉴权
- **仅在 nodeAttrs 有效时生效**：若 ACL Tag 未启用，此选项无效

### 安全默认

建议流程：
1. Central 和 Agent 都配好 Tailscale ACL nodeAttrs
2. Agent 的 `allow_anonymous = false`（默认）
3. **无需配置 Bearer token**，仅通过 ACL Tag 完成安全访问控制
4. 如需兼容非 Tailscale 来源的访问，可配置 Bearer token 或改为 `allow_anonymous = true`

---

## 自动重载

nodeAttrs 在以下时机自动刷新，无需重启：

| 事件 | Central | Agent |
|------|---------|-------|
| 启动 | 读取 nodeAttrs | 读取 nodeAttrs |
| tailscaled 重连 | 自动重载 | 自动重载 |
| ACL 策略更新 | 通过 WatchIPNBus 检测，自动重载 | 通过 WatchIPNBus 检测，自动重载 |
| 读取失败 | 保留上次成功值，log warning | 保留上次成功值，log warning |

---

## 配置文件开关

nodeAttrs 自动配置默认启用。如需关闭：

```toml
# central.toml
[discovery]
node_attrs = false   # 默认 true

# agent.toml
[server]
node_attrs = false   # 默认 true
```

设为 `false` 后完全忽略 nodeAttrs，行为与未配置 nodeAttrs 时完全一致。该开关支持热重载。

---

## 日志示例

Central 启动后成功读取 nodeAttrs：
```
central: nodeAttrs: agent tags=[tag:prometheus-agent tag:metrics-agent], port=9001
```

Agent 启动后启用 ACL Tag 鉴权：
```
agent: ACL-based auth enabled, allowed central tags: [tag:prometheus-central]
```

nodeAttrs 读取失败（保留上次值）：
```
central: failed to read nodeAttrs: tailscale status: ... (retaining previous)
```
