---
title: nem.toml
weight: 1
---

The manifest declaring a directory's tools and environment variables. Meant
to be committed. `nem` finds it by walking up from the current directory to
the nearest `nem.toml`; the global twin lives at `~/.nem/nem.toml`.

## Example

```toml
[tools]
go = '1.27.0'
"dev:kubectl" = '1.36.3'

[env]
KUBECONFIG = '$HOME/.kube/config'
```

This is the same shape `nem use` writes: bare version strings, single
quotes. `go` has no catalog prefix, so it resolves from the first catalog
(in configuration order) that carries it. `"dev:kubectl"` pins the package
to the catalog named `dev` — TOML requires quoting a prefixed key.

## `[tools]`

- A key is `[<catalog>:]<name>`. Without a prefix, lookup walks the
  configured catalogs first-match-wins, in configuration order. With a
  prefix, lookup uses that catalog only, and no other.
- A package name appears at most once, prefixed or not — so `nem unuse
  <pkg>` is never ambiguous about which entry to remove.
- Values are the exact version string as it appears in the catalog. There's
  no `"latest"` keyword and no ranges.
- Parsing is strict: an unknown table or key — a typo like `[tool]` — is an
  error, not a silent skip.
- Only direct intent lives here. The resolved closure, including
  dependencies, lives in [nem.lock](../nem-lock/).

## `[env]`

Names must match `^[A-Za-z_][A-Za-z0-9_]*$` and must not be on nem's
reserved-name list; a value supports `$VAR`/`${VAR}` expansion (unset
expands to empty) and `$$` for a literal `$`, performed by nem itself, not
the shell. See [Managing environments](../../guides/managing-environments/#environment-variables)
for the full expansion and precedence story.

## How versions resolve

`nem use`, `nem unuse`, and `nem lock` re-resolve the whole manifest every
time — there's no sticky state carried over from the last resolution.
Leaving off a version means the first entry of the package's catalog
`versions` list. Across platforms and dependents, one version per package
wins: the highest version required anywhere. A tool you pinned directly is
never silently overridden by a dependency that wants something else — that's
a pin-conflict error instead. `nem sync` never resolves; stability between
manifest changes comes from `nem.lock`, not from the resolver remembering
anything.
