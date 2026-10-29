# Documentation

[README.md](README.md) describes the folders of `docs/`. Before you add or change a decision record, read
[decisions/README.md](decisions/README.md).

## Place content

Put each piece of content where its reader looks for it:

| Content | Place |
| --- | --- |
| A decision: the problem, the options, the reasons, and the diagrams that explain it | A record in `decisions/` |
| How a component is built, configured, operated and tested, and the results of proofs of concept | The guide of the component in `developer/`, for example `developer/management-proxy.md` |
| How to install, configure and operate the gateway | `operator/` |

The decision records describe the current design. Before you change something that a record covers, read that record.
Change a proposed record directly. Supersede an accepted record with a new record.

## Keep one source

State each fact in one document. Other documents link to the section that states it. When you move content or rename a
heading:

1. Search `docs/` for links to the old file and the old anchor, and update them.
2. Tell the user about each changed path or anchor. Pages outside the repository, for example the internal wiki, link to
   sections on `main`.

Run `make markdown-lint`. It finds broken relative links.
