<!--
SPDX-FileCopyrightText: 2026 Deutsche Telekom AG

SPDX-License-Identifier: CC0-1.0
-->

# Contributing

All contributors must follow the [Code of Conduct](CODE_OF_CONDUCT.md).

## Checks

Install these tools to run the local checks:

- [REUSE](https://reuse.software/dev/#install) 6.0.0 or newer
- [rumdl](https://rumdl.dev/getting-started/installation/) 0.3.0 or newer

Run all repository checks:

```bash
make check
```

Format Markdown files and fix lint issues where possible:

```bash
make markdown-fmt
```

The Markdown rules are in [`.rumdl.toml`](.rumdl.toml). Run `make help` to see all targets.

## Git hooks

The repository includes an optional [Lefthook](https://lefthook.dev/) configuration. Install these additional tools
before you enable the hooks:

- [Lefthook](https://lefthook.dev/#how-to-install-lefthook) 2.0.4 or newer
- [Gitleaks](https://github.com/gitleaks/gitleaks#installing) 8.20.0 or newer
- [committed](https://github.com/crate-ci/committed#install)

The hooks use tools from `PATH`. They do not install or upgrade tools. CI remains the authoritative validation gate.

Enable the hooks once per clone:

```bash
make hooks
```

| Hook | Checks |
| --- | --- |
| `pre-commit` | Check REUSE compliance of the repository, scan staged content with Gitleaks and lint staged Markdown files with rumdl. The jobs run in parallel. The hooks do not change files. |
| `commit-msg` | Validate Conventional Commits with `committed`, except during merges and rebases. |

To bypass the hooks for one command, set `LEFTHOOK=0` or use `--no-verify`. CI still runs its required checks.

```bash
LEFTHOOK=0 git commit ...
```

## Commit messages

Use [Conventional Commits](https://www.conventionalcommits.org/). Allowed types:

`feat`, `fix`, `build`, `chore`, `ci`, `docs`, `perf`, `refactor`, `revert`, `style` and `test`.

Scopes are optional.

## Licensing

Every file needs an SPDX header with copyright and license information, as required by [REUSE](https://reuse.software/).
If a file format does not support comments, add an annotation to [`REUSE.toml`](REUSE.toml).

| Content | License |
| --- | --- |
| Source code and configuration with substance | `Apache-2.0` |
| Small repository configuration files, such as `.gitignore` | `CC0-1.0` |
| Documentation in `docs/` | `CC-BY-4.0`, set in `REUSE.toml`. These files need no SPDX header. |
