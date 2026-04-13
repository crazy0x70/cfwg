# cfwg

**English** · [简体中文](./README_zh-CN.md)

A lightweight and efficient Cloudflare WARP SOCKS5 proxy powered by Docker.

## Prerequisites

Before deployment, ensure the host environment meets the following requirements:

*   **Linux Docker environment** with kernel `WireGuard` support.
*   The container must be granted `CAP_NET_ADMIN` privileges.
*   A persistent volume for storing **WARP** state data.
*   Host-level network connectivity to **Cloudflare** and the **WARP API**.

For IPv6 support, confirm that both the host and Docker are configured for IPv6 networking.

## Quick Start

### Docker Compose (Recommended)

The repository now ships a ready-to-use `compose.yaml`. It pins the default Docker network name to `cfwg` instead of directory-derived names such as `test_default`.

If you want to inspect or customize it, the current content is:

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
    networks:
      - default

networks:
  default:
    name: cfwg
```

Start the service:

```bash
mkdir -p ./data
docker compose up -d
```

Monitor status:

```bash
docker compose ps
docker compose logs -f
```

The created Docker network will be:

- `cfwg`

## Verification

### Service Health
The container exposes a health check endpoint at `http://127.0.0.1:9090/readyz`. You can map this port to the host to verify the service status:

```bash
curl http://127.0.0.1:9090/healthz
curl http://127.0.0.1:9090/readyz
curl http://127.0.0.1:9090/status
```

### Proxy Functionality
Verify that traffic is routed through WARP using the trace endpoint:

```bash
curl --proxy socks5://127.0.0.1:1080 -H 'Host: cloudflare.com' http://1.1.1.1/cdn-cgi/trace -4
curl --proxy socks5://127.0.0.1:1080 -g -H 'Host: cloudflare.com' 'http://[2606:4700:4700::1111]/cdn-cgi/trace'
```

If the output contains `warp=on`, the connection is correctly routed via WARP.

## Configuration

### Runtime Environment Variables

| Variable | Default | Description |
| :--- | :--- | :--- |
| `WARP_BACKEND` | `legacy` | WARP backend mode |
| `WARP_STATE_DIR` | `/var/lib/warp-socks` | Directory for `state.json` |
| `WARP_RUNTIME_DIR` | `/var/lib/warp-socks/runtime` | Runtime files directory |
| `SOCKS5_CONFIG_PATH` | `/var/lib/warp-socks/runtime/socks5.json` | Generated SOCKS5 config path |
| `PROXY_PUBLIC_HOST` | `127.0.0.1` | Public host returned for UDP Associate |
| `PROXY_PUBLIC_PORT` | `1080` | Public port returned for UDP Associate |
| `HEALTHCHECK_URL` | `http://127.0.0.1:9090/readyz` | Health check and listener address |

### Authentication and Protocol Variables

*Note: The following variables are case-sensitive (lowercase).*

| Variable | Description |
| :--- | :--- |
| `proxy-stack` | Set to `4`, `6`, or leave empty for dual-stack |
| `warp-license` | Optional: WARP+ License key |
| `uname` | SOCKS5 username |
| `upwd` | SOCKS5 password |

Authentication credentials (`uname` and `upwd`) must be provided together. Providing only one will result in a configuration error.

## Data Persistence

Mapping the state directory is highly recommended to prevent device re-registration:

```yaml
volumes:
  - ./data:/var/lib/warp-socks
```

The `state.json` file contains device identity, WireGuard keys, peer information, assigned IP addresses, and license details.

## API Reference

The service provides the following HTTP endpoints:

*   `/healthz`: Liveness probe.
*   `/readyz`: Readiness probe (tunnel status).
*   `/status`: Returns the current runtime snapshot.

## Troubleshooting

*   **Docker fails before the container starts with `/dev/net/tun` not found:** remove the `--device /dev/net/tun:/dev/net/tun` mapping. `cfwg` uses kernel `WireGuard`, not a TUN device node.
*   **Service starts but proxy fails:** Ensure the host kernel supports `WireGuard`, `NET_ADMIN` is enabled, and host-level connectivity to Cloudflare is available.
*   **Authentication errors:** Both `uname` and `upwd` are mandatory if authentication is enabled.
*   **IPv6 configuration:** Set `proxy-stack` to `4` or `6`, or leave empty for dual-stack.
*   **IPv4-only / IPv6-only deployments:** the built-in connectivity probe now tries both the IPv4 and IPv6 Cloudflare trace endpoints by default, so single-stack deployments can still become ready without manual probe overrides.
*   **`docker compose` creates `test_default` or another directory-based network name:** use the bundled `compose.yaml`, or ensure your own compose file sets `networks.default.name: cfwg`.
*   **WARP status:** Use `docker exec cfwg ip -6 addr show dev wgcf` or inspect `state.json` to verify network status.
