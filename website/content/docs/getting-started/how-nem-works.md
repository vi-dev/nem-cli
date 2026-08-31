---
title: How nem works
weight: 3
---

`nem` gives each project its own set of command-line tools and environment
variables, scoped to that directory — plus a global set available everywhere.
Nothing is installed into system directories: tools live under `nem`'s home
and join your `PATH` only while they apply.

## The manifest: `nem.toml`

Each environment is described by a `nem.toml` you keep in your project. It
declares tools and environment variables:

```toml
[tools]
kubectl = '1.36.3'
terraform = '1.15.9'

[env]
AWS_PROFILE = 'dev'
```

You rarely edit it by hand — `nem use` and `nem unuse` maintain it. The
global manifest lives at `~/.nem/nem.toml`; target it with `--global` / `-g`.

## The lockfile: `nem.lock`

Next to the manifest, `nem` writes `nem.lock`: the exact packages the
manifest resolved to, including transitive dependencies, supported platforms,
and the SHA-256 digest of every artifact. `nem sync` installs from the
lockfile, and every download is verified against its digest before it is
installed. Commit both files: `nem.toml` says what you want, `nem.lock` makes
it reproducible.

If you edit `nem.toml` by hand, run `nem lock` to regenerate the lockfile and
install.

## The composed environment

Inside a project directory, `nem` composes the global environment with the
project's — the project's declarations win. `nem status` shows the result;
`nem which <tool>` shows where a specific tool resolves.

The composed environment reaches your shell in one of two ways:

- **Shell hook** — `nem activate` installs a hook (zsh, bash) that applies
  the environment automatically as you change directories.
- **Explicitly** — `nem exec` runs one command in the composed environment,
  and `nem env` prints the shell script that applies it (bash, zsh, or fish)
  — useful in CI and scripts.

## Catalogs

Packages come from catalogs: OCI images that map package names and versions
to downloadable, digest-pinned artifacts. On first run, `nem` configures the
official catalog, `ghcr.io/vi-dev/nem-official-catalog`. `nem catalog add`
registers others — including your own mirror inside an air-gapped network.
`nem search` and `nem info` look across enabled catalogs.

## Where things live

Everything `nem` installs stays under `NEM_HOME` (default `~/.nem`):
installed packages, catalog data, and the global manifest and lockfile.
`nem clean` reclaims disk space; deleting the directory removes everything
`nem` ever installed.
