# Contributing to PolyFleet

PolyFleet deliberately targets small personal fleets and low-resource VPS hosts.
Changes should preserve that scope and the existing privilege boundary.

## Development setup

Install Go 1.26, Node.js 22, and pnpm 11.16. Then run:

```bash
go mod verify
go test ./...
go vet ./...
pnpm --dir web install --frozen-lockfile
pnpm --dir web typecheck
pnpm --dir web test
pnpm --dir web build
```

Go code must pass `gofmt`; shell files must pass `bash -n`. Update focused tests
for every behavioral change. Changes to installers, backup formats, enrollment,
credentials, subscriptions, or the root helper require an explicit failure-path
test and a documentation update.

## Pull requests

- Keep changes focused and explain user-visible behavior and compatibility.
- Do not commit real IP addresses, domains tied to a private fleet, credentials,
  subscription URLs, certificates, database files, master keys, or unredacted
  configurations.
- Use synthetic fixtures and RFC 2606 example domains.
- Preserve backward compatibility between the current Server and previous stable
  Agent unless a coordinated upgrade is documented.
- Do not add arbitrary remote execution, mandatory external databases, or large
  runtime dependencies without an ADR and measured need.

Report security issues through [SECURITY.md](SECURITY.md), not a pull request or
public issue. Contributions are licensed under Apache-2.0.
