# Security Policy

## Supported versions

| Version | Security updates |
| --- | --- |
| 1.x | Yes |
| 0.x development releases | No |

Use the newest stable release before reporting a problem. Development tags are
not intended for internet-facing production deployments.

## Report a vulnerability

Do not open a public issue for a suspected vulnerability, leaked credential, or
unredacted configuration. Use GitHub's private vulnerability reporting form:

<https://github.com/Sen62455/PolyFleet/security/advisories/new>

Include the affected version, deployment type, reproduction steps, expected
impact, and whether the issue is already being exploited. Redact VPS addresses,
API tokens, subscription URLs, passwords, certificates, and configuration
secrets. A minimal synthetic reproducer is preferred.

The project aims to acknowledge a report within three business days, provide an
initial assessment within seven business days, and coordinate disclosure after a
fix is available. These are response targets, not a paid bug-bounty commitment.

## Security boundaries

- PolyFleet Server must stay behind HTTPS. The native service listens on loopback;
  the Docker port is also bound to loopback by default.
- Agent connections are outbound. Do not expose S-UI, Agent authentication, core
  traffic APIs, or the operations socket publicly.
- The operations helper accepts a fixed protocol. It cannot run arbitrary shell
  commands, service names, or paths outside the configured core directory.
- S-UI API tokens remain on their node. They must never be committed or sent in
  issue reports.
- Server database backups and the master key are emitted as separate files. Both
  are required to decrypt managed assignment credentials; protect them in
  separate encrypted off-VPS storage.
- Release SHA-256 files detect accidental or malicious asset replacement only
  when the checksum itself is trusted. Stable releases also publish an SBOM and
  keyless Sigstore signatures for verification.

## Secret exposure

Immediately revoke any credential pasted into a public issue, commit, log, or
chat. Rewrite Git history only after revocation; history rewriting is not a
substitute for rotation. Run the repository secret scanner before every release.
