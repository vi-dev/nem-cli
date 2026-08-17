<p align="center">
  <img src=".github/nem-icon.svg" alt="nem logo" width="110" height="110">
</p>

<h1 align="center">nem</h1>

<p align="center">
  Reproducible development environments for your projects.
</p>

<p align="center">
  <a href="https://github.com/vi-dev/nem-cli/actions/workflows/ci.yml"><img src="https://github.com/vi-dev/nem-cli/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <a href="https://github.com/vi-dev/nem-cli/releases/latest"><img src="https://img.shields.io/github/v/release/vi-dev/nem-cli?sort=semver" alt="Latest release"></a>
  <a href="go.mod"><img src="https://img.shields.io/github/go-mod/go-version/vi-dev/nem-cli" alt="Go version"></a>
  <a href="LICENSE"><img src="https://img.shields.io/github/license/vi-dev/nem-cli?color=blue" alt="License: MPL-2.0"></a>
</p>

> [!IMPORTANT]
> `nem` is in active development and is provided **as is**, without warranty of
> any kind. Expect breaking changes and rough edges — feedback and
> contributions are very welcome!

`nem` gives each project a reproducible set of developer tools and environment
variables. Declare what you need in a `nem.toml` file — `nem` fetches,
installs, and puts it on your `PATH`, identically for every teammate, agent,
and CI pipeline.

- **Reproducible environments** — commit `nem.toml` and everyone gets the same
  tools and variables, on demand, per directory.
- **Verified downloads** — runs without root, and every artifact is
  SHA-256-verified before it is installed.
- **Air-gapped friendly** — distribute tool catalogs and packages through any
  OCI registry, audited like any other artifact.

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

The script downloads the release archive, verifies it against the release's
`checksums.txt`, and installs the binary. It is customized with environment
variables:

| Variable | Default | Meaning |
|---|---|---|
| `NEM_VERSION` | latest release | a release tag such as `v0.1.0`, or `unstable` |
| `NEM_INSTALL_DIR` | `~/.local/bin` | install destination |
| `GITHUB_TOKEN` | unset | optional; raises the GitHub API rate limit |

Set them on `bash`, not on `curl` — `NEM_VERSION=… curl … | bash` scopes the
variable to the wrong process and silently installs the latest stable:

```shell
# A specific release
curl -fsSL https://raw.githubusercontent.com/vi-dev/nem-cli/main/install.sh | NEM_VERSION=v0.1.0 bash

# Rolling build of main — replaced on every merge, not a supported release
curl -fsSL https://raw.githubusercontent.com/vi-dev/nem-cli/main/install.sh | NEM_VERSION=unstable bash
```

Prebuilt binaries for Linux and macOS are also attached to every
[GitHub release](https://github.com/vi-dev/nem-cli/releases).

> [!NOTE]
> On first run, `nem` configures the official package catalog,
> [`ghcr.io/vi-dev/nem-official-catalog`](https://github.com/vi-dev/nem-official-catalog).
> Turn it off with `nem catalog disable official`, or manage your own catalogs
> with `nem catalog add`.

## Quick start

```shell
nem activate      # hook nem into your shell (zsh, bash)
exec $SHELL

cd ~/code/my-project
nem use kubectl   # records the resolved version in nem.toml and installs it
kubectl version --client
```

Every command supports `--help`. The main ones:

| Command | Purpose |
|---|---|
| `nem use [<catalog>:]<pkg>[@<version>]...` | Declare and install tools |
| `nem sync` | Install locked tools missing on this machine |
| `nem status` | Show declared tools and composed environment variables |
| `nem search <query>` | Search catalogs for packages |
| `nem which <tool>...` | Show where a tool resolves in the composed environment |
| `nem env` / `nem exec` | Print or run a command in the composed environment |
| `nem catalog` | Manage catalogs: add, list, remove, update, reorder, enable, disable, build, lint, publish |

## Container image

Multi-arch images are published to
[`ghcr.io/vi-dev/nem-cli`](https://github.com/vi-dev/nem-cli/pkgs/container/nem-cli)
on every release, signed with cosign and with SBOMs attached:

```shell
docker run --rm ghcr.io/vi-dev/nem-cli:latest --help
```

Stable releases are tagged `vX.Y.Z`, `vX.Y`, `vX`, and `latest`; the
`unstable` tag tracks `main`. The image carries a build toolchain so
`nem catalog build` works inside it.

## Contributing

Please open an issue to discuss substantial changes before sending a pull
request. `make check` runs the vet and test suite; `make hooks` sets up the
tracked git hooks so commit subjects are checked locally instead of failing
in CI.

## Security

See [`SECURITY.md`](SECURITY.md).

## License

[MPL-2.0](LICENSE).
