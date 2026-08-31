---
title: Air-gapped networks
weight: 5
---

How to use `nem` in an air-gapped network with no route to the public internet.

## The idea

`nem` use OCI registry to distribute catalogs and packages.
The official catalog itself is published in the [ghcr.io/vi-dev/nem-official-catalog](https://ghcr.io/vi-dev/nem-official-catalog) registry.

That means you can replicate a catalog and its packages into a registry inside your air-gapped network, 
and consume it exactly the same way you'd consume any other catalog.

## The workflow

{{% steps %}}

### Connected side: mirror the catalog

```shell
nem catalog mirror ghcr.io/vi-dev/nem-official-catalog:v2 registry.corp.example/nem/catalog:v2
```

`nem catalog mirror` replicates a catalog and its archives, byte for
byte, into a registry you own. `--dry-run` previews what would be copied.

A catalog usually doesn't publish all of its packages as archives in its OCI registry,
but rely on the client to fetch the packages directly from upstream sources instead.
Thus you need another step to fill your mirror with the missing archives.

### Connected side: fill your mirror

```shell
nem catalog fill registry.corp.example/nem/catalog:v2
```

`nem catalog fill` downloads the catalog's checksum-pinned upstream
artifacts and publishes them as archives beside your copy of the index.
It runs against your mirror rather than the source because publishing
archives needs push access to the registry. Use `--pkg <name>`
(repeatable) to limit the run to specific packages, and `--dry-run` to
preview the plan without downloading or publishing anything.

### Inside: consume the mirrored catalog

```shell
nem catalog add corp registry.corp.example/nem/catalog:v2
nem use kubectl
nem sync
```

Add the mirrored registry as a catalog, then use `nem` as normal — `nem
use`, `nem sync`, and everything else. Archives resolve from the
mirrored registry automatically because their location derives from the
catalog reference.

{{% /steps %}}

## Idempotent by design

Both `fill` and `mirror` are safe to interrupt and re-run. A re-run
recognizes what's already done and skips it, and heals anything missing
or stale rather than redoing it from scratch. As new upstream versions
land, re-running `mirror` and then `fill` on the connected side keeps
the internal side current.
