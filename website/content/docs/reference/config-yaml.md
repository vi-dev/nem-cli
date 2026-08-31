---
title: config.yaml
weight: 3
---

`nem`'s global configuration, at `~/.nem/config.yaml` (`$NEM_HOME/config.yaml`).

## Example

```yaml
catalogs:
  - name: official
    type: oci
    ref: ghcr.io/vi-dev/nem-official-catalog:v2
  - name: dev
    type: dir
    path: /home/me/src/my-catalog

hosts:
  - host: registry.corp.example
    ca: /etc/nem/corp-ca.pem
  - host: dev-registry.corp.example:5000
    plainHTTP: true
```

## `catalogs:`

A list of configured catalogs, in lookup-precedence order — the first
catalog in the list that has a package wins. `nem catalog` commands
(`add`, `remove`, `reorder`, `enable`/`disable`) rewrite this list;
hand-editing it works too.

| Field | Meaning |
|---|---|
| `name` | Required, unique — the handle `nem catalog` commands use |
| `type` | `oci` or `dir` |
| `ref` | `oci` only — a registry reference: a moving tag, a frozen release tag, or a digest |
| `path` | `dir` only — an absolute path to a local catalog directory |
| `disabled` | Optional, default `false` — a disabled catalog keeps its precedence slot but is skipped by lookups |

## `hosts:`

Per-host connection settings, applied when nem talks to an OCI registry.

| Field | Meaning |
|---|---|
| `host` | Required — exact match, including any port |
| `ca` | Absolute path to a private CA bundle (PEM) |
| `plainHTTP` | Use HTTP instead of HTTPS |
| `insecure` | Use HTTPS but skip certificate verification |

Exactly one of `ca`, `plainHTTP`, or `insecure` is required per entry. If
the same host appears in more than one entry, the last one wins. Loopback
registries (`localhost`, `127.0.0.0/8`, `::1`) default to plain HTTP unless
an entry names them explicitly.

## Loading

A missing `config.yaml` is the normal state before the first run — the
first command that needs the full config writes the built-in default (the
`official` catalog, no `hosts:`), and it never overwrites a file that's
already there.

`hosts:` is read leniently: a missing file yields no settings, an
unparsable file yields no settings plus a warning, and an individual entry
that fails validation is dropped with its own warning while the rest of
the list still applies. None of this fails the command. Commands that read
`catalogs:`, by contrast, parse the whole file strictly — an unknown key
or an invalid entry anywhere fails the command with a parse error.
