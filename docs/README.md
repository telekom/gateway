# Documentation

This directory holds the technical and operator documentation of the gateway, and the records of all decisions.

| Path | Content | Changes |
| --- | --- | --- |
| [`decisions/`](decisions/) | Decision records ([MADR](https://adr.github.io/madr/)): what we decided, the options and the reasons. Diagrams that explain a decision are part of its record. The accepted records describe the current design. | Not rewritten after acceptance. A new record supersedes an old one. |
| [`developer/`](developer/) | Implementation guides, one for each component: packaging, configuration, operation rules and test results. No fixed template. | Updated with the component. |
| [`operator/`](operator/) | Guides to install, configure and operate the gateway. Empty for now. `.gitkeep` keeps the directory in Git. | Updated with each release. |

Files in this directory need no SPDX header. [`REUSE.toml`](../REUSE.toml) licenses them under CC-BY-4.0.
