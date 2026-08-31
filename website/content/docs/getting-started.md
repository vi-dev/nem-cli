---
title: Getting Started
weight: 1
---

`nem` makes your development environment appear the moment you enter a project
directory. Declare the tools and environment variables a project needs in its
`nem.toml` file, and `nem` handles the rest.

{{< callout type="info" >}}
`nem` is in active development. Expect breaking changes and rough edges —
feedback and contributions are very welcome.
{{< /callout >}}

## Installation

Install `nem` using the provided installation script:

```shell
curl -fsSL https://raw.githubusercontent.com/vi-dev/nem-cli/main/install.sh | bash
```

By default, `nem` is installed to `~/.local/bin`. Add this directory to your
`PATH` if it isn't already:

```shell
export PATH="$HOME/.local/bin:$PATH"
```

It's possible to customize the installation using environment variables:

| Variable          | Default        | Meaning                                       |
|-------------------|----------------|-----------------------------------------------|
| `NEM_VERSION`     | latest release | a release tag such as `v0.1.0`, or `unstable` |
| `NEM_INSTALL_DIR` | `~/.local/bin` | install destination                           |
| `GITHUB_TOKEN`    | unset          | optional; raises the GitHub API rate limit    |

Examples:

To install `unstable` version:

```shell
curl -fsSL https://raw.githubusercontent.com/vi-dev/nem-cli/main/install.sh | NEM_VERSION=unstable bash
```

Or install a specific version:

```shell
curl -fsSL https://raw.githubusercontent.com/vi-dev/nem-cli/main/install.sh | NEM_VERSION=v0.1.0 bash
```

Prebuilt binaries for Linux and macOS are also attached to every
[GitHub release](https://github.com/vi-dev/nem-cli/releases).

### Update

`nem` is able to update itself:

```shell
nem self update                   # update to the latest stable release
nem self update --check           # check for updates without installing
nem self update --version v0.1.0  # update to a specific version
```

## Quickstart

Try `nem` in a project directory:

```shell
nem activate      # hook nem into your shell (zsh, bash)
exec $SHELL

cd ~/code/my-project
nem use kubectl   # records the resolved version in nem.toml and installs it
kubectl version --client
```

`kubectl` is now on your `PATH` whenever you are in this directory.

### Step by step

{{% steps %}}

### Hook nem into your shell

```shell
nem activate
exec $SHELL
```

`nem activate` installs a hook block into your shell's startup file (zsh and
bash are supported), and `exec $SHELL` restarts the shell so the hook takes
effect. The hook applies each project's environment automatically as you move
between directories. Use `nem activate --print` to inspect the block instead
of installing it.

### Declare your first tools

```shell
nem use kubectl
nem use go@1.27.0
```

`nem` installs the latest version of `kubectl`, the requested version of `go`, and records
the details in two files in the project directory:

- `nem.toml` — the manifest: what the project wants, e.g. `kubectl = '1.36.3'`
- `nem.lock` — machine-written: the exact resolved packages with SHA-256 digests and other details.

Both of these files should be committed to version control.

{{< callout type="info" >}}
On first run, `nem` configures the official package catalog,
[`ghcr.io/vi-dev/nem-official-catalog`](https://github.com/vi-dev/nem-official-catalog).
Turn it off with `nem catalog disable official`, or add your own catalogs
with `nem catalog add`.
{{< /callout >}}

### Use the tools

```shell
go version
kubectl version --client
```

The tools are on your `PATH` only while you are in this directory (or a
subdirectory). Leave and it disappears; come back and it returns.

### Share the environment

As long as `nem.toml` and `nem.lock` are committed, teammates, CI pipelines, and agents 
can reproduce the same environment by running:

```shell
nem sync
```

Which installs everything the lockfile pins that are missing on their machine — same versions, same digests.

{{% /steps %}}

### Everyday usage

Below are the most common commands you'll use with `nem`. 

For a complete list, run `nem help`.

| Command                                    | Purpose                                                |
|--------------------------------------------|--------------------------------------------------------|
| `nem use [<catalog>:]<pkg>[@<version>]...` | Declare and install tools                              |
| `nem sync`                                 | Install missing locked tools                           |
| `nem status`                               | Show declared tools and composed environment variables |
| `nem search <query>`                       | Search catalogs for packages                           |
| `nem which <tool>...`                      | Show where a tool resolves in the composed environment |
| `nem env` / `nem exec`                     | Print or run a command in the composed environment     |
| `nem catalog`                              | Manage catalogs                                        |
| `nem clean`                                | Reclaim disk space in `NEM_HOME`                       |
| `nem self update`                          | Update nem itself                                      |

## How nem works

`nem` gives each project its own set of command-line tools and environment
variables, scoped to that directory — plus a global set available everywhere.
Nothing is installed into system directories: tools live under `nem`'s home
and join your `PATH` only while they apply.

### The manifest: `nem.toml`

Each environment is described by a [`nem.toml`](../reference/nem-toml/) you keep in your project. It
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

### The lockfile: `nem.lock`

Next to the manifest, `nem` writes [`nem.lock`](../reference/nem-lock/):
the exact packages the manifest resolved to, including transitive
dependencies, supported platforms, and a digest pinning each package's
catalog manifest. `nem sync` installs from the lockfile, and every download
is verified against its digest before it is installed. Commit both files:
`nem.toml` says what you want, `nem.lock` makes it reproducible.

If you edit `nem.toml` by hand, run `nem lock` to regenerate the lockfile and
install.

### The composed environment

Inside a project directory, `nem` composes the global environment with the
project's — the project's declarations win. `nem status` shows the result;
`nem which <tool>` shows where a specific tool resolves.

The composed environment reaches your shell in one of two ways:

- [**Shell hook**](../guides/shell-integration/) — `nem activate` installs a hook (zsh, bash) that applies
  the environment automatically as you change directories.
- **Explicitly** — `nem exec` runs one command in the composed environment,
  and `nem env` prints the shell script that applies it (bash or zsh)
  — useful in CI and scripts.

### Catalogs

Packages come from catalogs: usually OCI images that map package names and
versions to downloadable, digest-pinned artifacts. On first run, `nem`
configures the official catalog, `ghcr.io/vi-dev/nem-official-catalog`.
`nem catalog add` registers others — including your own mirror inside an
air-gapped network.
`nem search` and `nem info` look across enabled catalogs.

### Where things live

Everything `nem` installs stays under `NEM_HOME` (default `~/.nem`):
installed packages, catalog data, and the global manifest and lockfile.
`nem clean` reclaims disk space; deleting the directory removes everything
`nem` ever installed.

## Next steps

To learn more about how to make the best of `nem`, check out our [Guides](../guides) and [Reference](../reference).
