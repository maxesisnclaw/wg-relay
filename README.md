# wg-relay

A lightweight UDP relay with optional TCP transport, designed to bridge WireGuard traffic through restrictive networks.

## Why

When a WireGuard peer sits behind a restricted network (e.g., a university campus) and cannot expose UDP ports directly, wg-relay acts as an application-layer proxy:

- **No IP forwarding** — the relay machine creates new packets (no TTL fingerprint)
- **No routing changes** — the relay machine's own traffic is completely unaffected
- **TCP transport** — disguises WireGuard UDP as a normal TCP stream, avoiding ISP UDP throttling
- **Zero dependencies** — single static binary, no runtime needed

## Architecture

```
                        Campus Network (restricted)
                        ┌─────────────────────────┐
  WireGuard Peer        │  Relay Machine           │
  (internal only)       │  (has internet access)   │
  ┌──────────┐   UDP    │  ┌──────────────┐  TCP   │        ┌──────────────┐
  │  Server  │ ────────>│  │  wg-relay    │ ──────>│──────> │  wg-relay    │
  │  (WG)    │          │  │  (client)    │        │        │  (server)    │
  └──────────┘          │  └──────────────┘        │        └──────┬───────┘
                        └─────────────────────────┘               │ UDP
                                                            ┌─────▼────────┐
                                                            │  Router (WG) │
                                                            └──────────────┘
```

## Usage

### Client mode (relay machine)

Windows: double-click `wg-relay.exe` → system tray icon appears.  
Linux: `./wg-relay --headless`

```json
{
    "mode": "client",
    "transport": "tcp",
    "listen_addr": "0.0.0.0",
    "listen_port": 51820,
    "remote_addr": "YOUR_HOME_PUBLIC_IP",
    "remote_port": 51821,
    "auto_start": false
}
```

### Server mode (home network)

```json
{
    "mode": "server",
    "transport": "tcp",
    "listen_addr": "0.0.0.0",
    "listen_port": 51821,
    "forward_addr": "10.50.0.1",
    "forward_port": 51820,
    "auto_start": true
}
```

```bash
./wg-relay --headless --config /etc/wg-relay/config.json
```

### Pure UDP mode

If TCP transport is not needed, set `"transport": "udp"` (default). The relay will forward UDP-to-UDP without framing.

## TCP Framing Protocol

Each UDP datagram is sent over TCP as:

```
[2-byte big-endian length][payload]
```

Max payload: 65535 bytes.

## Building

```bash
# Windows (GUI)
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -ldflags="-s -w -H windowsgui" -trimpath -o wg-relay.exe .

# Linux (CLI)
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -trimpath -o wg-relay .
```

## Download

Pre-built binaries are available on the [Releases](../../releases) page, with SHA-256 checksums.

## License

MIT
