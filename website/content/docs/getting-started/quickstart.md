---
title: Quickstart
weight: 2
---

Try `nem` in a project directory:

```shell
nem activate      # hook nem into your shell (zsh, bash)
exec $SHELL

cd ~/code/my-project
nem use kubectl   # records the resolved version in nem.toml and installs it
kubectl version --client
```

`kubectl` is now on your `PATH` whenever you are in this directory.

## Step by step

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

### Declare your first tool

```shell
nem use kubectl
```

`nem use` resolves the latest `kubectl` from your catalogs, installs it, and
records it in two files in the project directory:

- `nem.toml` — the manifest: what the project wants, e.g. `kubectl = '1.36.3'`
- `nem.lock` — machine-written: the exact resolved packages with SHA-256
  digests

Commit both. Ask for a specific version with `nem use kubectl@1.35.0`, or a
specific catalog with `nem use mycatalog:kubectl`.

### Use the tool

```shell
kubectl version --client
```

The tool is on your `PATH` only while you are in this directory (or a
subdirectory). Leave and it disappears; come back and it returns.

### Share the environment

Teammates and CI run:

```shell
nem sync
```

which installs everything the lockfile pins that is missing on their machine —
same versions, same digests.

{{% /steps %}}

## Everyday commands

Every command supports `--help`. The main ones:

| Command | Purpose |
|---|---|
| `nem use [<catalog>:]<pkg>[@<version>]...` | Declare and install tools |
| `nem sync` | Install locked tools missing on this machine |
| `nem status` | Show declared tools and composed environment variables |
| `nem search <query>` | Search catalogs for packages |
| `nem which <tool>...` | Show where a tool resolves in the composed environment |
| `nem env` / `nem exec` | Print or run a command in the composed environment |
| `nem catalog` | Manage catalogs |
| `nem clean` | Reclaim disk space in `NEM_HOME` |
| `nem self update` | Update nem itself |
