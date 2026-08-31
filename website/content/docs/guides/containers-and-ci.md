---
title: Containers and CI
weight: 4
---

`nem` is meant to be run in a container or a pipeline just as well as on a developer machine.

Running `nem`-managed tools in a container or a pipeline needs none of
the shell hook. Thus `nem sync` and `nem exec` are often enough.

## Images

`nem`'s multi-arch images are published to
[`ghcr.io/vi-dev/nem-cli`](https://github.com/vi-dev/nem-cli/pkgs/container/nem-cli)
on every release, signed with cosign and with SBOMs attached:

```shell
docker run --rm ghcr.io/vi-dev/nem-cli:latest --help
```

Stable releases are tagged `vX.Y.Z`, `vX.Y`, `vX`, and `latest`; the
`unstable` tag tracks `main`. 

The standard image is a fat image carrying a build toolchain, so `nem catalog build` works inside it.

## Rootless variants

Every tag has a `-rootless` companion (`vX.Y.Z-rootless`, …,
`rootless`, `unstable-rootless`).

This is a slim image that runs as user `nem` (uid=1000) and carries no build toolchain. 
It's meant for consuming catalogs — for example as a CI job's base image, or
as the base of a dev container.

## The CI pattern

There's no shell hook to install in a pipeline. Check out the project,
run `nem sync` to install whatever the lockfile pins that's missing on
the runner, then run each step under `nem exec` so it sees the composed
environment:

```shell
nem sync
nem exec -- kubectl apply -f manifests/
nem exec -- go test ./...
```

## Caching

`$NEM_HOME/packages` (default `~/.nem/packages`) holds every package nem has installed. 

It's a good idea to cache that directory between CI runs, so tools aren't
re-downloaded every time — see the [NEM_HOME reference](../../reference/nem-home/) for what lives under it.
