# Architecture Decision Records

ADRs capture decisions that constrain implementation. Accepted ADRs are changed
by adding a superseding ADR, not by silently rewriting the original rationale.

| ADR | Decision | Status |
| --- | --- | --- |
| [0001](0001-control-plane-and-agent.md) | Separate control plane and outbound Agent | Accepted |
| [0002](0002-go-vue-sqlite-stack.md) | Go, Vue, embedded UI, and SQLite WAL | Accepted |
| [0003](0003-polling-and-eventual-consistency.md) | HTTPS polling and desired-state reconciliation | Accepted |
| [0004](0004-agent-side-adapters.md) | HY2 data-plane adapters run Agent-side | Accepted |
| [0005](0005-credentials-and-accounting.md) | Encrypted credentials and idempotent traffic outbox | Accepted |
| [0006](0006-experimental-vless-reality-sing-box.md) | Isolated, typed VLESS Reality data plane backed by sing-box | Superseded by PolyFleet v1.3.0 promotion |

Template fields for future ADRs: title, status, context, decision, consequences,
alternatives, and supersession relationship.
