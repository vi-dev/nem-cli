---
title: nem.lock
weight: 2
---

The machine-written lockfile recording the fully resolved package closure.
Sits next to `nem.toml` (global twin: `~/.nem/nem.lock`). Commit it — the
file itself carries a "machine-written by nem — do not edit" header as a
reminder.

## Example

```toml
# machine-written by nem — do not edit
version = 2

[[package]]
name = 'go'
version = '1.27.0'
catalog = 'official'
direct = true
platforms = ['darwin/arm64', 'darwin/amd64', 'linux/arm64', 'linux/amd64']
digest = 'sha256:963ae56…'
on_path = true

[[package]]
name = 'brotli'
version = '1.2.0'
catalog = 'official'
direct = false
platforms = ['darwin/arm64', 'linux/arm64', 'linux/amd64']
digest = 'sha256:c4bdeef…'
on_path = false
on_loader_path = true
```

`go` is a directly declared tool, needed on all four platforms, on `PATH`.
`brotli` is a dependency nobody declared directly (`direct = false`) — here
it's a linked library, so it's absent from `PATH` (`on_path = false`) but
present on the loader path (`on_loader_path = true`), and it isn't needed
on `darwin/amd64` at all, so that platform is missing from its list.

## Covers all four platforms

The lock records the closure for **all four supported platforms** —
`darwin/arm64`, `darwin/amd64`, `linux/arm64`, `linux/amd64` — in a single
file, so macOS and Linux teammates share one lock. Each entry's `platforms`
list is the subset of those four that actually needs the package; not every
entry needs every platform.

## Fields

`version = 2` at the top of the file is a format marker. Each `[[package]]`
block has:

| Field | Meaning |
|---|---|
| `name` | Package name |
| `version` | Exact resolved version |
| `catalog` | Catalog the package resolved from |
| `direct` | Whether a manifest declared this package directly, vs. pulled in as a dependency |
| `platforms` | The subset of the four supported platforms that need this package |
| `digest` | The catalog's manifest digest for this package at this version; omitted for `dir` catalogs |
| `on_path` | Whether the package's binaries join `PATH` |
| `on_loader_path` | Whether the package's libraries join the loader path |

Entries are sorted by name, and writes are atomic.

## Semantics

Only `nem use`, `nem unuse`, and `nem lock` resolve and rewrite this file.
`nem sync` reads it verbatim and installs the current platform's subset —
no resolution happens on that path. If `nem.toml` has drifted from what
`nem.lock` covers, `sync` prints an advisory warning rather than failing;
the lock stays authoritative until the next `nem lock`.

Each entry's `digest` pins the exact package manifest bytes at the time it
was locked. If the catalog's entry for that name and version no longer
matches — for example because a moving tag got republished — `sync` fails
with a hint to re-lock, instead of silently installing something different
from what was verified. The full integrity chain runs lock digest → package
manifest → artifact SHA-256, so every step from the lockfile down to the
bytes on disk is pinned.
