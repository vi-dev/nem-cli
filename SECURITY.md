# Security Policy

`nem` is pre-1.0 and provided as is, without warranty. Reports are handled on
a best-effort basis, and only the latest release receives fixes.

## Reporting a vulnerability

**Do not open a public GitHub issue for security reports.** Use GitHub's
[private vulnerability reporting][gh-advisories] for this repository.

## In scope

- **Path traversal or symlink escape** during archive extraction or package
  installation — any write outside `nem`'s staging or install directories.
- **SHA-256 verification bypass** during artifact fetch.
- **Credential leakage** — registry credentials sent to a host they were not
  configured for.

## Out of scope

- Malicious `pkg.yaml` from a catalog the user has explicitly added. Package
  install and build steps run unsandboxed with the invoking user's
  privileges — adding a catalog is a trust decision.
- Compromise of an upstream release or server that a catalog points to.
- Denial of service via pathologically large catalogs or manifests.

## Disclosure

We coordinate disclosure with reporters and credit them in any resulting
advisory unless they prefer otherwise.

[gh-advisories]: https://docs.github.com/en/code-security/security-advisories/guidance-on-reporting-and-writing/privately-reporting-a-security-vulnerability
