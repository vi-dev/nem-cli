# nem

Manage your development environment.

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/vi-dev/nem-cli/main/install.sh | bash
```

Installs the latest release to `~/.local/bin`. Override with `NEM_INSTALL_DIR`.

### Channels

```sh
# Latest stable release (default)
curl -fsSL https://raw.githubusercontent.com/vi-dev/nem-cli/main/install.sh | bash

# Rolling build of main — replaced on every merge, not a supported release
curl -fsSL https://raw.githubusercontent.com/vi-dev/nem-cli/main/install.sh | NEM_VERSION=unstable bash

# A specific release
curl -fsSL https://raw.githubusercontent.com/vi-dev/nem-cli/main/install.sh | NEM_VERSION=v0.1.0 bash
```

The environment variable goes on `bash`, not on `curl` — `NEM_VERSION=… curl … | bash`
would scope the variable to the wrong process and silently install the latest
stable.

| Variable | Default | Meaning |
|---|---|---|
| `NEM_VERSION` | latest release | a tag such as `v0.1.0`, or `unstable` |
| `NEM_INSTALL_DIR` | `~/.local/bin` | install destination |
| `GITHUB_TOKEN` | unset | optional; raises the GitHub API rate limit |

### Container

```sh
docker run --rm ghcr.io/vi-dev/nem-cli:latest --help
```

The image carries a build toolchain so `nem catalog build` works inside it.
The `unstable` tag tracks `main`.
