---
title: NEM_HOME
weight: 4
---

All of nem's on-disk state lives under one per-user directory: `$NEM_HOME`,
default `~/.nem`. Set `NEM_HOME` to relocate it — nem writes nothing outside
it except the managed shell-rc block that `nem activate` installs, and
nothing anywhere needs root.

## Layout

```
~/.nem/
├── config.yaml              # nem's configuration: catalog list, host settings
├── nem.toml                 # global manifest
├── nem.lock                 # global lockfile
├── lock                     # internal process lock — never deleted
├── usage.json               # last-resolution stamps; read by `clean --unused`
├── tmp/                     # download staging
├── packages/
│   └── <name>/<version>/    # one immutable tree per installed version
└── catalogs/
    └── <name>/store/        # local mirror of an oci catalog
```

- **`config.yaml`** — see [config.yaml](../config-yaml/).
- **`nem.toml` / `nem.lock`** — the global twins of the project files; see
  [nem.toml](../nem-toml/) and [nem.lock](../nem-lock/).
- **`lock`** — a flock guarding writes to manifests, locks, config, and
  catalog syncs. The file itself is never deleted.
- **`usage.json`** — last-resolution stamps for installed versions;
  consulted by `clean --unused` to decide what's eligible for eviction.
- **`tmp/`** — in-flight downloads, cleaned opportunistically.
- **`packages/<name>/<version>/`** — one installed version, addressed by
  name and exact version.
- **`catalogs/<name>/store/`** — the local mirror of an `oci` catalog's
  index; `dir` catalogs have nothing here, since they're read straight
  from their configured path.

## Install properties

A version counts as installed exactly when its directory exists under
`packages/`. Installs commit by atomic rename, so a half-finished install
is never visible as an installed version — either the whole tree is there,
or nothing is. Interrupted staging left behind by an install that didn't
finish is swept up by later runs.

## Reclaiming space

```shell
nem clean
```

Bare `nem clean` touches only provable garbage — leaked build staging,
leaked downloads, partial installs — and never prompts, so it's safe to
run unattended.

- `--unused <days|hours>` and `--all` go further and evict installed
  package *versions*; `nem sync` reinstalls whatever a project still needs.
  Either one asks for confirmation before deleting unless `-y`/`--yes` is
  given.
- `--dry-run` prints the plan without deleting anything.
- `--grace` (default `1h`) leaves recently-touched reclaimable paths alone.
- `-y, --yes` skips the confirmation prompt for `--unused`/`--all`.

See [Managing environments](../../guides/managing-environments/) for the
full command reference, including the caveat on how `--unused` measures
"unused."
