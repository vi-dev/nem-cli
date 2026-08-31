---
title: Catalogs
weight: 3
---

Where packages come from, and how to add, order, and authenticate to more
of them.

## What a catalog is

A catalog is an ordered, named source of package manifests. There are two
types:

- `oci` — an OCI registry reference, mirrored locally. This is how the
  official catalog is distributed.
- `dir` — a local directory laid out as `pkgs/<name>/pkg.yaml`, mainly for
  authoring a catalog before publishing it.

On first run, `nem` configures the `official` catalog for you. It's a
plain entry like any other — removable, reorderable, disable-able — with
no special reserved status.

{{< callout type="info" >}}
`nem`'s official catalog is hosted at
[`ghcr.io/vi-dev/nem-official-catalog`](https://github.com/vi-dev/nem-official-catalog).
{{< /callout >}}

## Lookup order

Catalogs are searched in configuration order, and the first one that has
a package wins. A `[<catalog>:]` prefix on a tool in `nem.toml` (for
example `nem use mycatalog:kubectl`) pins that tool to one specific
catalog, skipping the rest.

A disabled catalog keeps its slot in the order but is skipped by lookups
— disabling one and re-enabling it later doesn't change where it sits
relative to the others.

## Managing catalogs

```shell
nem catalog add <name> <ref>      # --type oci|dir; default: auto-detect
nem catalog list
nem catalog remove <name>
nem catalog reorder <name>...     # every configured catalog, exactly once
nem catalog update [name]         # sync oci catalogs from their remote
nem catalog enable <name>...
nem catalog disable <name>...
```

`nem catalog add` auto-detects the type from the reference unless you
pass `--type`. `reorder` rewrites the whole precedence list at once — it
expects every configured catalog named exactly once. `update` re-syncs
`oci` catalogs against their remote; `dir` catalogs are read live from
disk and need no updating.

All of this is stored in `$NEM_HOME/config.yaml`, and hand-editing the file
works just as well as the commands above. See the
[config.yaml reference](../../reference/config-yaml/) for the full file
format.

## Authentication

OCI registries use standard Docker credentials — `nem` has no
authentication store of its own. Run `docker login <registry>` to set up
access, and `nem` picks up the same credentials.
