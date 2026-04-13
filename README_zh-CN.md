# cfwg

一个简洁、高效的 Cloudflare WARP SOCKS5 Docker 代理。

## 运行前提

在部署前，请确保宿主机满足以下要求：

*   **Linux Docker 环境**：支持 `/dev/net/tun`。
*   **容器权限**：必须具备 `CAP_NET_ADMIN` 权限。
*   **数据持久化**：挂载卷用于存放 WARP 状态数据。
*   **网络连接**：宿主机可正常访问 Cloudflare 及 WARP API。

若需使用 IPv6，请确认宿主机与 Docker 已启用 IPv6 网络支持。

## 快速开始

### Docker Compose（推荐）

创建 `docker-compose.yml` 文件：

```yaml
services:
  cfwg:
    image: ghcr.io/crazy0x70/cfwg:latest
    container_name: cfwg
    restart: unless-stopped
    cap_drop:
      - ALL
    cap_add:
      - NET_ADMIN
    devices:
      - /dev/net/tun:/dev/net/tun
    sysctls:
      net.ipv6.conf.all.disable_ipv6: "0"
    tmpfs:
      - /tmp
      - /run
    ports:
      - "127.0.0.1:1080:1080/tcp"
      - "127.0.0.1:1080:1080/udp"
    environment:
      PROXY_PUBLIC_HOST: 127.0.0.1
      PROXY_PUBLIC_PORT: 1080
    volumes:
      - ./data:/var/lib/warp-socks
```

启动服务：

```bash
mkdir -p ./data
docker compose up -d
```

查看状态：

```bash
docker compose ps
docker compose logs -f
```

## 验证

### 服务健康检查
容器内置健康检查接口 `http://127.0.0.1:9090/readyz`。你可以将此端口映射至宿主机进行检查：

```bash
curl http://127.0.0.1:9090/healthz
curl http://127.0.0.1:9090/readyz
curl http://127.0.0.1:9090/status
```

### 代理验证
使用追踪接口验证流量是否通过 WARP 路由：

```bash
curl --proxy socks5://127.0.0.1:1080 -H 'Host: cloudflare.com' http://1.1.1.1/cdn-cgi/trace -4
curl --proxy socks5://127.0.0.1:1080 -g -H 'Host: cloudflare.com' 'http://[2606:4700:4700::1111]/cdn-cgi/trace'
```

若输出中包含 `warp=on`，则表示流量已正确经由 WARP 代理。

## 配置项

### 运行时环境变量

| 变量 | 默认值 | 说明 |
| :--- | :--- | :--- |
| `WARP_BACKEND` | `legacy` | WARP 后端模式 |
| `WARP_STATE_DIR` | `/var/lib/warp-socks` | 状态文件 `state.json` 目录 |
| `WARP_RUNTIME_DIR` | `/var/lib/warp-socks/runtime` | 运行时文件目录 |
| `SOCKS5_CONFIG_PATH` | `/var/lib/warp-socks/runtime/socks5.json` | 生成的 SOCKS5 配置路径 |
| `PROXY_PUBLIC_HOST` | `127.0.0.1` | UDP Associate 返回的公网地址 |
| `PROXY_PUBLIC_PORT` | `1080` | UDP Associate 返回的公网端口 |
| `HEALTHCHECK_URL` | `http://127.0.0.1:9090/readyz` | 健康检查及监听地址 |
| `WARP_TUN_DEVICE_PATH` | `/dev/net/tun` | TUN 设备路径 |

### 认证与协议变量

*注意：以下变量区分大小写（均为小写）。*

| 变量 | 说明 |
| :--- | :--- |
| `proxy-stack` | 设置为 `4`、`6` 或留空 (双栈) |
| `warp-license` | 可选：WARP+ License 密钥 |
| `uname` | SOCKS5 用户名 |
| `upwd` | SOCKS5 密码 |

认证凭据（`uname` 和 `upwd`）必须同时提供，仅配置其中一个将导致配置错误。

## 数据持久化

强烈建议挂载持久化目录，以避免频繁重新注册设备：

```yaml
volumes:
  - ./data:/var/lib/warp-socks
```

`state.json` 文件保存了设备标识、WireGuard 密钥、对端信息、分配的 IP 地址及 License 信息。

## API 接口参考

服务提供了以下 HTTP 端点：

*   `/healthz`：进程存活检查（Liveness）。
*   `/readyz`：隧道就绪检查（Readiness）。
*   `/status`：返回当前的运行状态快照。

## 常见问题排查

*   **容器启动但代理不通**：检查宿主机 `/dev/net/tun` 是否存在，是否添加了 `NET_ADMIN` 权限，并确认卷挂载路径及网络连通性。
*   **认证失败**：确认 `uname` 与 `upwd` 是否成对配置。
*   **IPv6 配置**：配置 `proxy-stack` 为 `4` 或 `6`，或者保持空值（默认为双栈）。请勿使用字符串 `dual`。
*   **确认 WARP 状态**：使用 `docker exec cfwg ip -6 addr show dev wgcf` 或检查 `state.json` 文件查看网络详情。