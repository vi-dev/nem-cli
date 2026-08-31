---
title: Installation
weight: 1
---

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
`checksums.txt`, and installs the binary. Customize it with environment
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

## Updating

An installed `nem` updates itself:

```shell
nem self update             # latest stable release
nem self update --check     # only report whether an update is available
nem self update --version v0.1.0
```

The update is checksum-verified and the new binary is test-run before it
replaces the old one.

{{< callout type="info" >}}
On first run, `nem` configures the official package catalog,
[`ghcr.io/vi-dev/nem-official-catalog`](https://github.com/vi-dev/nem-official-catalog).
Turn it off with `nem catalog disable official`, or manage your own catalogs
with `nem catalog add`.
{{< /callout >}}
