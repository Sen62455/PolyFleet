# ADR 0006: Typed VLESS Reality Data Plane with sing-box

- Status: Superseded by promotion to PolyFleet `v1.3.0`
- Date: 2026-08-12
- Scope: `experimental/vless-reality-singbox`; not part of the HyFleet stable
  product contract until the promotion gates in this ADR pass
- Extends: ADR 0001, ADR 0003, ADR 0004, and ADR 0005
- Supersedes: only the Hysteria2-only and S-UI-only limitations described below;
  all other security and consistency decisions in those ADRs remain in force

> Historical note: this ADR defined the isolated experiment and its promotion
> gates. Commit `0bcb1fa` completed the usage, online-state, targeted-disconnect,
> quota, and node-budget gates. PolyFleet `v1.3.0` promotes that exact typed
> profile while retaining the security and ownership boundaries below.

## Context

HyFleet deliberately presents itself as a small Hysteria2 control plane. Its
clarity comes from a narrow product scope, one outbound Agent per node,
eventually consistent desired state, independent per-node credentials, and a
root helper that cannot execute arbitrary commands.

There is now a concrete need to test a second managed data plane: VLESS over
direct TCP with Reality, implemented by a separate sing-box process. Adding it
by broadening every Hysteria2 branch or by turning the existing
`standalone_sing_box` observer into a generic configuration editor would make
ownership and security ambiguous. It would also make an experimental feature
look as complete as the mature native Hysteria2 path.

This decision therefore defines one intentionally narrow protocol profile and
the architectural boundaries needed to test it without changing the stable
project promise. It creates extension points for later protocols, but does not
commit HyFleet to becoming a general-purpose proxy panel.

## Decision

### 1. Product and branch boundary

The first implementation lives on `experimental/vless-reality-singbox`. The
stable branch continues to describe HyFleet as Hysteria2-focused until this ADR's
promotion gates pass and a separate product-scope decision is accepted.

The experimental feature is identified as
`sing_box_vless_reality`. It is not an alias for, or an in-place upgrade of,
`standalone_sing_box`:

- `standalone_sing_box` remains an observation and bounded-operations adapter
  for an existing, externally owned sing-box installation.
- `sing_box_vless_reality` owns one dedicated sing-box service and its entire
  generated configuration.
- An enrolled Node continues to represent one Agent installation and one data
  plane. Its adapter is not switched after enrollment. Testing on a host that
  already runs Hysteria2 uses a second isolated experimental Agent, state
  directory, operations socket, service unit, and node record.
- The laboratory deployment starts on an unused high TCP port. Moving it to TCP
  443 is a separate, deliberate cutover after connection and rollback tests.
  An existing UDP 443 Hysteria2 service may continue in parallel.

This separation prevents the experiment from adopting, rewriting, or deleting
an existing Hysteria2, S-UI, or sing-box configuration.

### 2. Exact MVP protocol profile

The managed profile is limited to:

- one inbound per Node;
- VLESS users identified by independent UUID credentials;
- direct TCP transport;
- Reality TLS;
- the fixed VLESS flow `xtls-rprx-vision`;
- one locally generated X25519 Reality key pair;
- one locally generated full-length random Reality short ID;
- a validated DNS server name and a validated Reality handshake destination;
- a fixed handshake port of 443; and
- a fixed, tested client fingerprint selected by the renderer rather than an
  unrestricted per-user option.

The public listen host and port, server name, and handshake DNS name are typed,
bounded settings. No API accepts a raw sing-box JSON document, arbitrary route,
command, filesystem path, systemd unit, environment entry, or command-line
argument.

The initial contract target is sing-box v1.13.18 on Linux `amd64` and `arm64`.
Every supported sing-box release must be named in the compatibility matrix and
covered by configuration-check, startup, reconciliation, and subscription
fixtures. An unknown version is read-only/incompatible and cannot receive a
mutating apply.

### 3. Adapter and capability model

Adapter identity answers which data plane owns the node. Optional behavior is
advertised and implemented as separate capabilities instead of being implied by
the adapter name. The conceptual boundaries are:

```text
DataPlaneAdapter: Probe, Plan, Apply
UsageCollector:   CollectUsage
OnlineProvider:   CollectOnline
UserKicker:       Kick
Operator:         allowlisted typed operations
```

An adapter need not implement every optional capability. The Server and UI must
display unsupported or unavailable explicitly; they must not substitute zero
traffic, an empty online list, or a successful no-op.

For the first VLESS Reality release:

- probing, deterministic full-user reconciliation, core status, bounded logs,
  configuration backup, restart, and subscription rendering are required;
- per-user usage is experimental and enabled only when the installed sing-box
  build is positively probed for the required V2Ray API capability;
- online presence and kick are unsupported unless a later tested capability is
  added; and
- quota enforcement on this data plane is unavailable without a healthy usage
  capability and must be shown as such.

The managed sing-box configuration sets `log.disabled` to `true` by default.
Connection-level core logs may expose VLESS UUIDs, user identifiers, or other
sensitive material, so `tail_core_log` for this adapter is primarily a bounded
view of systemd lifecycle diagnostics. Disabling sing-box logs does not affect
configuration validation, service and listener health checks, atomic
publication, or rollback.

The subscription renderer consumes typed protocol endpoints. It does not infer
subscription behavior from Agent implementation details or reuse a sing-box
adapter name as a protocol name.

### 4. Desired-state and mixed-version compatibility

Desired snapshot schema v1 is immutable and continues to serve
`native_hysteria2` and `s_ui`. VLESS Reality uses desired snapshot schema v2.
Schema v2 is a discriminated, typed model containing:

- adapter and data-plane kind;
- the bounded VLESS/TCP/Reality settings;
- an opaque pinned local Reality identity generation;
- user IDs, effective enabled/expiry/quota state, and credential references and
  fingerprints; and
- no plaintext VLESS UUID or Reality private key.

The current Agent API major may remain v1 because schema negotiation is explicit,
but enrollment/heartbeat capabilities must include both
`desired_state_v2` and `sing_box_vless_reality` before the Server sends schema
v2. Rollout order is Server first, Agent second.

A new Agent must continue to apply schema v1 for existing adapters. An old Agent,
an Agent without both capabilities, or an Agent that receives an unknown schema
must reject the apply with a stable redacted error, preserve its last applied
state, and continue heartbeat and other compatible work. It must never partially
interpret a newer snapshot.

Desired snapshots remain immutable, monotonically versioned, canonically hashed,
and acknowledged asynchronously as established by ADR 0003. Subscriptions expose
a Reality endpoint only after the exact user credential, settings revision, and
Reality public identity have been acknowledged as applied.

### 5. Logical data model

Protocol is not added as a property of the global User. It belongs to the
user-node credential and rendered endpoint. The logical model uses typed
discriminators rather than more Hysteria2-specific branches:

- a Node has one data-plane profile and adapter;
- an assignment has distinct desired and applied credential references;
- a credential has a protocol kind, initially `hysteria2` or `vless`;
- a VLESS credential is one node-specific UUID;
- a Reality identity belongs to the Node data plane, not to a user; and
- subscription endpoints carry typed protocol-specific public metadata.

The SQLite migration must rebuild or replace constraints that currently permit
only `hysteria2`; it must not relabel existing rows. Existing IDs, credential
ciphertext, applied references, desired snapshots, and subscriptions retain
their meaning. A pre-migration database backup is mandatory because SQLite
schema migrations are forward-only and a software downgrade uses that backup
rather than reverse SQL.

The credential encryption associated data continues to bind credential ID,
user ID, node ID, and protocol. Cross-protocol reinterpretation is therefore
rejected even when other identifiers match.

This schema is extensible, but MVP APIs expose only the two known credential
kinds and the one new data-plane profile. Unknown discriminators fail closed;
they are not preserved as arbitrary executable configuration.

### 6. VLESS credential responsibility

The Server generates a cryptographically random RFC-compatible VLESS UUID for
each user-node assignment. Reusing one UUID across Nodes is forbidden.

The Server encrypts the recoverable UUID with the existing versioned
XChaCha20-Poly1305 master-key mechanism and identity-bound associated data. The
UUID is needed to render subscriptions and configure sing-box, so it cannot be
stored only as a verifier.

Schema v2 contains only the credential reference and fingerprint. The existing
credential-material contract is generalized from "S-UI only" to adapters that
explicitly advertise and are authorized for credential material. For a Reality
request, the Server releases exactly one UUID only when all of these match:

- the authenticated node and installation;
- adapter and credential protocol;
- the node's exact current desired version and canonical hash;
- the non-revoked desired credential reference; and
- the occurrence of that reference in the exact schema-v2 snapshot.

Mismatch responses are indistinguishable. Responses remain `no-store`, request
and response bodies are never logged, and the Agent never writes the UUID to its
snapshot cache or local database. The value exists briefly in Agent/helper
memory and necessarily persists in the root-controlled sing-box configuration,
which is credential-bearing state and must be protected and backed up as such.

This section supersedes ADR 0005 only where that ADR says the material endpoint
is exclusive to S-UI. Its per-node isolation, desired/applied cutover, encryption,
redaction, and audit rules continue unchanged.

### 7. Reality identity responsibility

The Reality private key is generated on the node and never leaves it. It is not
generated, escrowed, accepted, displayed, backed up, or logged by the Server.
The root helper owns a root-controlled local identity file. Only the helper and
the dedicated sing-box service account may read the private material needed for
the generated configuration.

The Agent reports only an opaque identity-generation ID, the public key, and the
client-visible short ID. The Server stores those public values so it can render
subscriptions, then pins the generation in desired state. An unexpected local
identity change is `identity_mismatch`, not an automatic replacement.

Initial bootstrap may register the first public identity only for an enrolled,
otherwise-uninitialized Reality node. Subsequent rotation or recovery requires
an explicit administrator action, creates a new node revision, withholds the
endpoint from subscriptions until apply acknowledgement, and invalidates clients
that have not refreshed. Silent key rotation during restart, upgrade, failed
apply, or reinstall is forbidden.

The bounded administrator action is
`POST /api/v1/nodes/{nodeID}/reality/rotate-identity`. It carries the expected
desired key generation and node desired version as optimistic concurrency
guards. The Server accepts it only when the current generation and node revision
are fully applied, increments the desired generation exactly once, creates a new
desired snapshot, and marks the node assignments pending. The previously
applied public key remains visible as historical applied material but is never
subscription-eligible while the desired and applied generations differ. A stale
or repeated request cannot skip an identity generation.

The operator is responsible for a protected node-local/off-host backup of the
Reality identity if continuity after disk loss is required. A lost key without a
backup requires explicit identity replacement and subscription refresh; the
control-plane database alone cannot reconstruct it.

The short ID and public key are public client configuration, not authentication
secrets. They are still omitted from routine diagnostic output to reduce
unnecessary endpoint disclosure.

### 8. Local privilege and ownership boundary

The dedicated sing-box service and its generated configuration are wholly owned
by this adapter. The adapter never adopts or merges an existing monolithic
configuration.

The unprivileged Agent communicates with the root helper over its existing
root-owned Unix-socket boundary. The helper accepts a new bounded, structured
Reality apply request. Its installation fixes:

- the exact sing-box binary;
- the exact systemd unit;
- the normalized configuration, candidate, last-known-good, identity, and
  backup locations; and
- maximum users, request bytes, file bytes, output bytes, and execution time.

None of those values comes from a controller operation string. The helper does
not expose a generic shell, arbitrary `systemctl`, arbitrary file read/write,
raw sing-box configuration endpoint, or path traversal. It rejects symlinks,
special files, ownership/mode mismatches, duplicate UUIDs, duplicate user tags,
invalid ports/hosts/keys, and values outside the MVP profile.

Generated files are owned by the least-privileged account/group needed by the
service and are never world-readable. Temporary files are created in the target
directory with restrictive modes so atomic replacement cannot cross a
filesystem or follow a link. Configuration backups contain credentials and the
Reality private key; they remain local, access-controlled, bounded, and are
never placed in operation results or uploaded to the Server. Results contain
only redacted status and non-secret metadata such as size and digest.

Keeping sing-box as a separate process also preserves the licensing and failure
boundary. HyFleet does not import sing-box's GPL-licensed Go implementation into
its Apache-2.0 binaries. Bundling or redistributing a sing-box binary, and adding
any statistics/protobuf dependency, requires an explicit license and release
review; installing a separately distributed binary is the default experiment.

### 9. Reconciliation, validation, and rollback

Applying the same desired version and hash is a no-op. Applying a new version is
ordered as follows:

1. Validate schema, node, adapter, identity generation, typed settings, bounds,
   and canonical hash.
2. Retrieve only the exact current VLESS credential material and build the
   candidate through the structured local helper request.
3. If usage collection is active, durably sample the final counters before any
   restart.
4. Write a restrictive candidate file and run the pinned
   `sing-box check` command against it.
5. Preserve the current file as the last-known-good configuration, then fsync
   and atomically replace the managed configuration.
6. Restart only the fixed dedicated unit and verify process state, expected
   listener, and a bounded health deadline.
7. On failure, atomically restore the last-known-good file, restart the same
   unit, verify recovery, and report `failed` with `rolled_back=true` and a
   redacted stable error.
8. Acknowledge `applied` only after the new service is healthy and the local
   applied version/hash is durably recorded.

Candidate validation failure never stops the current service. A restart or
health failure never promotes the desired credential or identity for
subscription rendering. If rollback also fails, the node is degraded, the
endpoint remains withheld, and no automated destructive retry or identity
rotation occurs.

A controller outage leaves an already running sing-box service and its last
valid configuration untouched. It delays changes, material retrieval, and
reports, but is not placed on the connection path.

### 10. Usage accuracy

sing-box V2Ray API statistics are capability-gated. The Agent must positively
probe the running binary/configuration for the required API before enabling
collection. Merely detecting a sing-box process is insufficient.

When enabled, counters use the existing durable baseline, source epoch, immutable
batch, and exactly-once controller ingestion design. A planned configuration
restart first captures and persists the final delta, then starts a new source
epoch. An unplanned core crash can lose increments not yet sampled, and a reset
cannot reconstruct them.

Accordingly, Reality-node usage in the MVP is operational telemetry, not
billing-grade accounting. The API and UI must expose unavailable, reset, stale,
and estimated/incomplete states instead of presenting exact totals. This
limitation must be resolved by a tested upstream contract before this adapter can
inherit the mature Hysteria2 traffic claim.

### 11. Subscription contract

The URI/Base64, Mihomo/Clash, and sing-box renderers gain a typed VLESS Reality
endpoint. Each output contains the assignment UUID and the applied public host,
port, server name, public key, short ID, direct-TCP transport, Reality security,
fixed flow, and tested client fingerprint using that format's proper escaping.

Renderers include the endpoint only when:

- the Node, User, and assignment are enabled and otherwise eligible;
- the Node reports a compatible adapter/core contract;
- desired and applied credential references match;
- the applied settings and pinned Reality identity generation match; and
- the latest apply acknowledgement covers the rendered endpoint metadata.

No desired-only UUID, unacknowledged public key, private key, management address,
or helper/configuration path enters a subscription. Existing Hysteria2 endpoints
and formats remain byte-for-byte compatible unless a separate renderer change is
accepted and tested.

## Non-goals

The experimental MVP does not provide:

- arbitrary sing-box inbound, outbound, route, DNS, or experimental JSON;
- VLESS transports other than direct TCP;
- WebSocket, HTTPUpgrade, gRPC, QUIC, multiplexing, fallback, or layer-4 port
  sharing;
- protocols other than existing Hysteria2 and this one VLESS Reality profile;
- multiple managed data planes or protocols under one Node record;
- adoption or mutation of an existing sing-box/S-UI installation;
- controller custody of the Reality private key;
- automatic DNS, certificate, firewall, camouflage-site, or VPS provisioning;
- guaranteed online-user state, kick, device/IP limits, or billing-grade usage;
- user-supplied UUIDs, weak/shared credentials, or credential discovery/import
  from arbitrary local or remote configuration; or
- a generic protocol-builder UI.

## Promotion gates

The experiment may be proposed for a stable branch or a new repository only
after all of the following are demonstrated and documented:

- database migration from the latest stable release preserves every existing
  Hysteria2 and S-UI record, and downgrade recovery from a pre-migration backup
  is rehearsed;
- mixed Server/Agent versions prove schema-v1 compatibility and schema-v2
  fail-closed behavior;
- cross-node, cross-protocol, stale-version, wrong-hash, revoked, and missing
  credential-material requests are denied without information leakage;
- sentinel tests prove UUIDs and private keys do not enter snapshots, Agent
  state, logs, errors, metrics, audit payloads, or operation results;
- candidate validation, atomic replacement, restart timeout, health failure,
  rollback success, and rollback failure paths have integration tests;
- each subscription format connects with tested clients to the pinned sing-box
  contract and never renders desired-only material;
- a controller outage, Agent restart, core restart, network partition, credential
  rotation, identity recovery, and counter reset have been exercised;
- the isolated laboratory instance coexists with the host's existing production
  data plane and does not alter its files, service, ports, or Agent state;
- resource use remains within documented small-VPS budgets during idle,
  reconciliation, restart, and sustained usage reporting; and
- license review and an updated compatibility matrix, threat model, operator
  runbook, backup/recovery procedure, and product-scope statement are complete.

## Consequences

- HyFleet can test a useful second protocol without weakening the stable
  Hysteria2 contract or converting the existing observer into a config editor.
- A dedicated process and fully owned configuration make validation and rollback
  tractable, but consume additional memory and require another systemd unit.
- Node-specific UUIDs and node-local Reality keys limit credential blast radius,
  at the cost of two distinct backup responsibilities.
- Schema v2 and capability negotiation add protocol complexity, but avoid
  reinterpreting schema v1 or pretending unsupported features work.
- Full-config restart is deterministic and easy to recover, but briefly
  interrupts VLESS sessions and weakens traffic precision.
- A single data plane per Node keeps the first change bounded. Running several
  protocols on one host requires multiple Agent instances for now.

## Future evolution

Future protocols must add a typed data-plane profile, typed subscription
endpoint, explicit credential kind, tested capability set, and their own threat
and compatibility review. They must not add arbitrary executable configuration
to a generic JSON bucket.

If operating multiple data planes on one host becomes a real requirement, the
domain should evolve from the current one-to-one Node model to a `Host` that owns
one Agent installation and multiple `DataPlane` records. That migration should
be a separate ADR. It must not be approximated by overloading one Node's adapter
string, credential, desired version, service unit, or traffic epoch.

## Rejected alternatives

- **Modify `standalone_sing_box` in place:** changes an observation-only
  compatibility promise into destructive ownership and risks rewriting unknown
  production configuration.
- **Add scattered `if protocol == ...` branches:** preserves the current surface
  briefly but couples Agent apply, credentials, accounting, subscriptions, and
  UI into a fragile pseudo-generic panel.
- **Accept arbitrary sing-box JSON from the Server:** broadens the root boundary,
  defeats typed validation and ownership, and makes security review unbounded.
- **Link sing-box into HyFleet:** couples release, memory, crash, and licensing
  boundaries and turns HyFleet into a proxy implementation rather than a control
  plane.
- **Generate or store Reality private keys centrally:** simplifies restore but
  unnecessarily exposes every node identity to a control-plane compromise and
  backup leak.
- **Model multiple protocols on one current Node immediately:** requires a Host
  and DataPlane migration across enrollment, desired versions, operations,
  traffic epochs, and UI; it is substantially larger than this experiment.
