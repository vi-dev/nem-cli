---
title: Managing environments
weight: 2
---

The mechanics of declaring, sharing, and inspecting the tools and
environment variables a project needs.

## Declaring tools

```shell
nem use [<catalog>:]<pkg>[@<version>]...
```

`nem use` resolves each package, installs it, and records the result in two
files: the version in `nem.toml`, the exact resolved closure — with
SHA-256 digests — and transitive dependencies in `nem.lock`.

If `@<version>` is not specified, `nem` resolves the newest version that
fits the environment's other tools. 
If `<catalog>:` is not specified, `nem` searches for the tool in all catalogs, in order, until it finds a match.

`nem unuse <pkg>...` removes declarations from `nem.toml` and re-resolves
`nem.lock`. It never deletes installed tools — other projects on the same
machine may still be using them.

## Updating tools

```shell
nem update [<pkg>...]
```

`nem update` (alias `up`) re-resolves declared tools — all, or just the
ones named — to their catalog's latest, exactly as if you re-ran `nem use`
for each. `-g` targets the global manifest; `--dry-run` reports the plan
without writing anything. It never downgrades and never re-syncs mirrors —
a stale mirror earns a warning to run `nem catalog update` first. Each
tool settles on the newest version its dependents allow; a pick below the
declared version is refused, and conflicting declarations fail the update
with the clashing requirements.

## Project vs. global scope

By default `nem` works at project scope, where declared tools and environment are scoped to the project directory tree.
`nem` finds a project's manifest by walking up from the current directory to the nearest `nem.toml`. 

`nem` also supports a global scope, where declared tools and environment are available everywhere.
The global `nem.toml` lives at `$NEM_HOME/nem.toml`, and every command that touches a manifest 
addresses it by adding `--global` / `-g` flag.

```shell
nem use -g go@1.27.0   # declare a tool globally, not just for this project
```

When both scopes declare the same tool or environment variable, the project's declaration wins — see
[the full precedence order](#what-wins) below.

## Hand-editing

`nem.toml` is meant to be edited by hand too, not only through `nem use`.
After editing it, run `nem lock` to regenerate `nem.lock` and install:

```shell
nem lock
```

Every tool must be pinned to the exact version string as it appears in the
catalog — there's no `"latest"` keyword and no version ranges in
`nem.toml`; a version `nem` can't find in the catalog is a lock error. If a
dependency needs a different version than a tool you pinned directly,
that's a pin-conflict error, not a silent override. `nem` never installs a
version that contradicts what the manifest declares.

## Environment variables

Alongside tools, `nem.toml` can declare environment variables.

### Declaring

Environment variables live in the `[env]` table, in either the project or
the global `nem.toml`:

```toml
[env]
KUBECONFIG = '$HOME/.kube/config'
AWS_PROFILE = 'dev'
```

Names must match `^[A-Za-z_][A-Za-z0-9_]*$` and must not be on the reserved list.

Edit `nem.toml` directly to change `[env]` — unlike a `[tools]` change, no
`nem lock` is needed. The shell hook applies the edit on the next
directory change, or re-run `nem env` to apply it immediately.

### Expansion

Values support POSIX-style parameter expansion, performed by `nem` itself at
composition time — never by the shell:

- `$VAR` and `${VAR}` expand; a variable that isn't set expands to the
  empty string.
- `$$` produces a literal `$`.
- There's no command substitution and no `~` — use `$HOME` instead.

Because `nem` does the expanding, the scripts it emits carry values that are
already expanded and safely quoted. Manifest content can never inject shell
code, even if a variable's value contains something that looks like one.

### Layering on originals

For a variable `nem` manages, `$VAR` inside an `[env]` value resolves to the
value the shell had *before* nem touched it — not `nem`'s own composed value.
That makes it safe to extend rather than replace:

```toml
[env]
FOO = '$FOO:extra'
```

This layers onto the shell's original `$FOO` every time, instead of
appending to `nem`'s own previous output — so repeated evaluations of the
hook stay idempotent rather than growing the value on every `cd`.

### What wins

When the same variable is set in more than one place, precedence runs
lowest to highest:

1. Global manifest's package exports
2. Project manifest's package exports
3. Global `nem.toml` `[env]`
4. Project `nem.toml` `[env]`

`nem status` shows which package exported a variable, or `nem.toml` for a manifest `[env]` entry.

### PATH and loader path

The composed `PATH` is built deterministically: project tools before global
ones, direct tools before their dependencies, all of that ahead of the
original `PATH` — deduplicated, so nothing appears twice.

When the resolved environment includes a library package, `nem` also composes a
platform loader-path variable the same way — `DYLD_LIBRARY_PATH` on macOS,
`LD_LIBRARY_PATH` on Linux. A project that only uses
command-line tools never gets these variables touched at all.

### Reserved names

Some names are reserved, checked case-insensitively, because `[env]`
values are eval'd straight into your shell. A reserved name is accepted
into `nem.toml`, but `nem` drops that entry at composition time with a
warning, so it never reaches your shell. The denylist, by category:

- **Shell control** the shell itself relies on: `PATH`, `PS1`, `IFS`, and
  similar.
- **zsh's tied arrays**, which zsh keeps in sync with special variables:
  `FPATH`, `MANPATH`, `CDPATH`, and others.
- **libc loader controls**: `LOCPATH`, `NLSPATH`, and similar.
- **Reserved prefixes**: `NEM_*`, `LD_*`, `DYLD_*` — the one exception is
  `nem`'s own composed loader variable described above, which `nem` writes
  directly rather than through a package or manifest export.
- **Reserved suffix**: `*_SET`, which collides with the hook's own
  save/restore bookkeeping (see
  [Shell integration](../shell-integration/)).

## Sharing

Commit both `nem.toml` and `nem.lock`. Teammates and CI run:

```shell
nem sync
```

Which installs exactly what the lockfile pins and warns if `nem.toml` has drifted from `nem.lock`, 
for example a declared tool the lock doesn't cover yet.

```
WARN nem.toml declares tree@2.3.2, which nem.lock does not cover — run `nem lock`
```

## Inspecting

| Command                                  | Shows                                                                 |
|------------------------------------------|-----------------------------------------------------------------------|
| `nem status` (`-g` for the global scope) | Declared tools and composed environment variables                     |
| `nem which <tool>...`                    | Where a tool resolves in the composed environment                     |
| `nem info [<catalog>:]<pkg>`             | A package's details and available versions                            |
| `nem search [query]`                     | Catalog packages matching a query, omit `query` to list every package |

## Reclaiming disk space

To clean up safely removable garbage, e.g. leaked build staging, leaked downloads,
partial installs, run:

```shell
nem clean
```

A bare `nem clean` is safe to run unattended. Use `--dry-run` to preview what would be deleted without actually deleting anything.

Run `nem clean --help` for more information.
