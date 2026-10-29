# Documentation

This directory holds the technical and operator documentation of the gateway, and the records of all decisions.

| Path | Content |
| --- | --- |
| [`decisions/`](decisions/) | Decision records: what we decided, which options we considered and why |
| [ADR 0004](decisions/0004-use-an-oauth-proxy-for-cross-cluster-configuration.md) | Proposed cross-cluster architecture and validation responsibilities |
| [ADR 0005](decisions/0005-reuse-an-existing-kubernetes-api-proxy.md) | Proposed reuse-versus-build decision for the Kubernetes API proxy |
| [`diagrams/route-configuration-flow.md`](diagrams/route-configuration-flow.md) | Mermaid diagrams for ADR 0004 and ADR 0005: observation options and the route configuration flow |

We add more sections when we have content for them, for example architecture, operations and development guides.

Files in this directory need no SPDX header. [`REUSE.toml`](../REUSE.toml) licenses them under CC-BY-4.0.
