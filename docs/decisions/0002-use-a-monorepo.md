---
status: accepted
decision-makers:
  - niklas.beilmann@telekom.de
  - mika.juznik@telekom.de
  - luis.pflamminger@telekom.de
  - michael.fedorenko@telekom.de
consulted:
  - christian.ingenhaag@telekom.de
tags: [repository]
---

# Use One Repository for All Gateway Components

## Context and Problem Statement

The new gateway consists of several components, for example a Helm chart that deploys kgateway, external processing
(ExtProc) services in the request path and an API that applies gateway configuration. The current Kong-based gateway
keeps each component in its own repository, with its own CI pipeline, tooling and release.

Do we keep the components of the new gateway in one repository or in one repository per component?

## Decision Drivers

- The components depend on each other. In the current gateway, a change in
  [gateway-jumper](https://github.com/telekom/gateway-jumper) always needs a separate pull request that bumps the chart.
  A single pull request should be able to introduce a self-contained change.
- The [control plane](https://github.com/telekom/controlplane) of the Open Telekom Integration Platform already uses one
  repository for all of its components.
- The team is small. Duplicated tooling, CI setup and documentation cost time in every repository.
- To keep central documentation and agent skills, the team uses a workspace repository that bundles all component
  repositories as Git submodules. This is a workaround that a monorepo does not need.
- Each component must still be buildable, testable and releasable on its own.

## Considered Options

- One repository for all components (monorepo)
- One repository per component, as for the current gateway

## Decision Outcome

Chosen option: "one repository for all components".

Each component gets its own top-level directory when we add it.

## More Information

- [Repositories of the current gateway](https://github.com/telekom/Open-Telekom-Integration-Platform/blob/main/docs/repository_overview.md)
