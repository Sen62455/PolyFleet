# PolyFleet compatibility matrix

## Hosts

| Component | Supported host | Architectures | Supervisor |
| --- | --- | --- | --- |
| Server, native | Ubuntu 24.04 LTS; Debian 12 and 13 | amd64, arm64 | systemd |
| Agent | Ubuntu 24.04 LTS; Debian 12 and 13 | amd64, arm64 | systemd |
| Server, container | Linux with Docker Engine and Compose v2 | amd64, arm64 | Docker |

Native CI performs clean-host installation and backup/restore drills on Ubuntu
24.04 and Debian. Other systemd distributions may work but are not release
gates. The bootstrap installer intentionally rejects unsupported distributions.

The Docker image contains only PolyFleet Server. Agent containers are unsupported
because safe operation requires local systemd, core configuration paths, and a
root-owned Unix socket helper.

## Core adapters

| Adapter | Tested contract | User management | Traffic/subscription |
| --- | --- | --- | --- |
| Native Hysteria2 | Hysteria2 v2 HTTP auth and traffic APIs | Managed | Managed |
| S-UI | S-UI v1.5.3 through versions below v1.6.0, `/apiv2` | Explicit adoption | Managed after adoption |
| Standalone sing-box | `sing-box.service`, fixed file or directory configuration | Observation only | Not yet managed |
| VLESS Reality | Pinned compatibility sing-box `1.13.18-hyfleet-utls1.8.7-api2`, direct TCP, Reality, `xtls-rprx-vision` | Managed | Subscription, per-user traffic, active connections, quotas, and targeted disconnect managed |

Standalone sing-box supports core status, restart, bounded logs, alerts, and
node-local configuration backup. A configured directory may contain at most 512
regular files/directories, be at most 16 levels deep, and contain at most 8 MiB
of uncompressed data. Symbolic links and special files are rejected.

The VLESS Reality adapter is supported from PolyFleet `v1.3.0`. Its data-plane
contract is pinned to the regular executable `/usr/bin/sing-box`, version
`1.13.18-hyfleet-utls1.8.7-api2`, the unit
`hyfleet-sing-box-reality.service`, the generated configuration
`/etc/sing-box/hyfleet-reality.json`, and the root-only identity
`/var/lib/hyfleet-agent-ops/reality-hyfleet-sing-box-reality.json`. The
PolyFleet release ships the exact binary, and the installer verifies the
host-architecture SHA-256 from `deploy/sing-box-reality.sha256`, root ownership,
ELF architecture, and the exact version line. Official sing-box `1.13.18` and
all other builds fail installation and configuration apply closed.

The accepted binary is built from sing-box commit
`45ca32dcb966f07f97fc888fe8586e359dbe8405`, changing only the selected
`github.com/metacubex/utls` module from `v1.8.4` to `v1.8.7`. This contains the
upstream Reality server buffer correction needed by the tested handshake target.
`scripts/build-sing-box-reality.sh` pins Go `1.26.5`, the dependency checksums,
build tags, linker version, and both Linux architectures. Identical inputs can
therefore be independently checked against the committed hashes; this is not a
claim that different Go releases produce byte-identical binaries.

This profile manages one VLESS inbound over direct TCP with Reality and fixed
flow `xtls-rprx-vision`. It supports managed users, subscriptions, bidirectional
per-user accounting, global and assignment quota enforcement, online connection
snapshots, and targeted disconnect through a fixed loopback API. WebSocket,
gRPC, HTTPUpgrade, arbitrary sing-box JSON, and externally managed inbounds are
not part of the contract.

## Runtime telemetry

Detailed process and service telemetry requires a native Linux Agent with a
readable procfs mounted at `/proc` and a working systemd/systemctl interface. The
packaged Agent unit uses `ProtectProc=default` and `ProcSubset=all` so the Agent
can read the bounded process facts needed for monitoring. Hosts that hide other
processes through stricter procfs or service sandbox settings cannot provide the
complete process view without an operator-approved unit override.

Process and systemd collection degrade independently. If either source is
temporarily unavailable, enrollment, heartbeat, desired-state convergence, and
the other telemetry section continue. The API reports a stable section error
and keeps that section's last successful values and sample time. The Agent scans
at most 4096 PIDs, reports at most 16 leading processes, and reports at most 128
systemd services per snapshot.

## Deployment contracts

- Server native layout: `/etc/hyfleet` and `/var/lib/hyfleet`.
- Agent native layout: `/etc/hyfleet`, `/var/lib/hyfleet-agent`,
  `/var/lib/hyfleet-backups`, and `/var/lib/hyfleet-agent-ops`.
- Reality core: non-root account `hyfleet-singbox`, configuration
  directory `/etc/sing-box` owned by `root:hyfleet-singbox` with mode `0750`,
  and runtime directory `/var/lib/hyfleet-singbox`.
- HTTPS is mandatory between Agent and Server.
- Existing S-UI clients remain read-only until explicitly adopted.
- Forward restore into the same or a newer stable Server is supported. Downgrade
  across a database migration requires the pre-upgrade database snapshot.

## HyFleet runtime compatibility

PolyFleet `v1.3.0` preserves the deployed HyFleet runtime ABI: existing binary
and systemd names, `/etc/hyfleet`, `/var/lib/hyfleet*`, `HYFLEET_*`,
`X-HyFleet-*`, and the local Reality API path. Public source, release assets and
the container image use the PolyFleet name. Do not rename compatibility paths on
an existing installation.
