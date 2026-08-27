# Development Stages

Each stage is independently reviewable and ends with tests and an explicit exit
gate. A stage does not mutate production nodes until its failure and rollback
behavior has been tested locally or on the designated test node.

## Phase 0: Design and inventory (`v0.0`)

Deliver product boundary, ADRs, architecture, threat model, domain model, Agent
protocol draft, deployment budget, inventory, and phase gates.

Exit gate: Phase 0 review accepted and the live non-secret inventory confirmed.

## Phase 1: Foundation (`v0.1`)

Deliver Go module/repository layout, embedded migrations, admin bootstrap/login,
session/CSRF baseline, node CRUD, one-time Agent enrollment, heartbeat, desired
poll skeleton, host metrics, structured logging, CI, and release builds.

Tests: API/store unit tests, enrollment replay, auth rate limit, Agent restart,
protocol schema tests, measured resource baseline.

Exit gate: one non-production/test Agent reliably appears online/offline with
correct host metrics; no proxy configuration is changed.

## Phase 2: Native Hysteria2 users (`v0.2`)

Deliver global user CRUD, independent generated encrypted credentials per
assignment, native desired snapshots with verifiers, Agent cache, native HTTP
auth, expiry/enable enforcement, adapter health, and controlled migration tooling
for LisaHost.

Tests: valid/invalid/expired/disabled auth, controller outage, Agent restart,
snapshot replay/rollback, constant-time verifier path, cross-node credential
isolation, redaction.

Exit gate: LisaHost supports a test/global user without per-user core restarts;
the existing client remains usable when preservation is requested; rollback to
the original auth config is documented and tested.

## Phase 3: Traffic and online state (`v0.3`)

Deliver native Traffic Stats collection, durable baselines/outbox, source epochs,
idempotent controller ingestion, node/global totals, online list, kick, expiry and
global quota evaluation, metric charts, and retention jobs.

Tests: duplicate/out-of-order batch, Agent crash around outbox commit, core reset,
controller restart, unknown user quarantine, quota propagation delay.

Exit gate: injected duplicate/restart scenarios preserve exact totals and a
limited user is denied on every online assigned test node within the documented
consistency window.

## Phase 4: Unified subscriptions (`v0.4`)

Deliver subscription token lifecycle, endpoint eligibility, URI/Base64/Clash
Meta/sing-box renderers, caching headers without secret leakage,
endpoint-specific credentials, and selected/all-assignment credential rotation
workflow for applied native Hysteria2 assignments.

Tests: format fixtures, escaping, revoked token, disabled/pending node exclusion,
desired-versus-applied credential cutover, per-endpoint password/endpoint
rotation, subscription log redaction.

Exit gate: a working native Hysteria2 assignment renders in all four formats,
desired credentials remain withheld until applied, and rotating/revoking a Token
behaves predictably. Standalone sing-box membership remains gated on its Adapter.

## Phase 5: S-UI adapter and DMIT onboarding (`v0.5`)

Implementation status: complete in `v0.5.0-dev`; real DMIT acceptance remains an
operator deployment step.

Deliver S-UI compatibility probe, read-only discovery/import, explicit client
adoption, local ownership mapping, managed create/update/disable/delete, status,
online and traffic integration, node/version-bound credential-material retrieval,
and DMIT onboarding.

Tests: real supported S-UI container/version, incompatible version, API outage,
manual unmanaged clients, ID/name changes, repeated reconciliation, ownership
deletion guard, stale/cross-node credential reference denial, no-store headers,
secret non-persistence/redaction, and sentinel secrets in discovery responses.

Exit gate: the same global test user is applied to all three nodes, traffic and
status are visible, and every field of every unmanaged S-UI client remains
unchanged.

## Phase 6: Bounded operations and recovery (`v0.6`)

Implementation status: complete in `v0.6.0-dev`; real three-node acceptance
remains an operator deployment step.

Deliver offline desired-state and operation-result catch-up, retry controls,
typed restart/probe/log/backup operations, configuration backup metadata,
restricted helper execution, active alerts, and documented node recovery.

Tests: partitions, stale operations, repeated restart request, bounded log output,
backup consistency, helper idempotency, restart rollback, alert lifecycle, and
Agent result Outbox recovery.

Exit gate: an offline node catches up safely, a failed apply preserves prior
state, a failed restart can restore the most recent node-local configuration,
and active faults raise alerts that resolve after recovery.

## Phase 7: Public release (`v1.0`)

Deliver native and Docker installation, systemd hardening, upgrade/rollback
documentation, amd64/arm64 releases, checksums/signatures/SBOM, compatibility
matrix, security policy, contribution guide, screenshots, and clean-host E2E.

Exit gate: a new supported VPS can install from the release documentation; CI and
security gates pass; no real fleet secret or identifying inventory is in Git.

## Phase 8: Native convergence and host monitoring (`v1.1`)

Implementation status: in post-v1 development; production migration remains an
operator-controlled, one-host-at-a-time acceptance step.

Deliver replacement-native-node migration for existing S-UI and standalone
sing-box hosts, a compact fleet overview, full per-node host details, bounded
30-day metric history, dedicated filtered operation history, and heartbeat
isolation from database reads and long-running node operations. Keep legacy
adapters intact as migration rollback paths.

Tests: an empty operation-history read on a single SQLite connection, concurrent
heartbeats while another read connection is occupied, heartbeats during a blocked
operation executor, metric aggregation/retention bounds, operation filtering and
pagination, Linux collector compilation, and desktop/mobile browser flows.

Exit gate: all intended hosts run native Hysteria2 for 24 hours with plausible
host metrics, users and subscriptions contain the expected nodes, a controlled
restart affects only its target, and the old Beszel/S-UI/sing-box management
layers can be stopped without losing required capability. Uninstall remains a
separate, recoverable operator decision.

## Post-v1 candidates

Subscription operations, bounded traffic reports, encrypted Telegram/webhook
notifications, VPS asset metadata, filtered pagination, and bounded batch node
operations are implemented by migration `0014_operations_layer.sql`. Remaining
candidates include multi-admin RBAC/TOTP, approved Ansible jobs, PostgreSQL,
more protocols, device limits, and larger-fleet push optimization. Each requires
a new ADR and measured need.
