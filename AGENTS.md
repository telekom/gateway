<!--
SPDX-FileCopyrightText: 2026 Deutsche Telekom AG

SPDX-License-Identifier: CC0-1.0
-->

# Gateway Monorepo

This monorepo holds the new API gateway of the
[Open Telekom Integration Platform](https://github.com/telekom/Open-Telekom-Integration-Platform). It is based on
kgateway and the Kubernetes Gateway API, and it will replace the current Kong-based gateway. The new gateway is under
active development. Structure, interfaces and decisions can change at any time.

For the repositories of the current gateway, see the
[repository overview](https://github.com/telekom/Open-Telekom-Integration-Platform/blob/main/docs/repository_overview.md).

Each component gets its own top-level directory when we add it.

## Documentation and decisions

Documentation and decision records are in `docs/`. Before you add, move or change anything in `docs/`, or change
something that a decision covers, read `docs/AGENTS.md`. Encourage the user to persist important decisions and
documentation in `docs/` and keep them up to date.

## Checks

Use the Makefile targets. Run `make help` to see them.

```bash
make check       # REUSE, Markdown and the component lints
make check-all   # also the component tests. Needs Docker.
```

## Conventions

- **License headers:** every file must carry a REUSE-compliant SPDX header. CI verifies this. If a file format does not
  support comments, add an annotation to `REUSE.toml`. Files in `docs/` need no header, because `REUSE.toml` licenses
  them. See the licensing table in `CONTRIBUTING.md`.

  ```text
  SPDX-FileCopyrightText: 2026 Deutsche Telekom AG
  SPDX-License-Identifier: Apache-2.0
  ```

- **Commit messages:** follow [Conventional Commits 1.0.0](https://www.conventionalcommits.org/en/v1.0.0/). Allowed
  types: `feat`, `fix`, `build`, `chore`, `ci`, `docs`, `perf`, `refactor`, `revert`, `style`, `test`. Scopes are
  optional. Never commit a breaking change without discussion.
- **Public content:** this is a public repository. Use generic terms in code, comments, docs and commit messages. Do not
  use internal product names, internal zone names, internal hostnames or customer data.
