---
status: proposed
decision-makers:
  - niklas.beilmann@telekom.de
  - christian.ingenhaag@telekom.de
  - mika.juznik@telekom.de
  - luis.pflamminger@telekom.de
  - michael.fedorenko@telekom.de
tags: [process]
---

# Record Decisions in the Repository with MADR

## Context and Problem Statement

We will make many decisions about the architecture, code, tooling and the way we work. People who join later must be
able to find these decisions and understand why we made them.

Where do we record decisions, and which format do the records use?

## Decision Drivers

- Decisions change with the code. A change to a decision should get the same pull request review as a change to the
  code.
- Everyone who reads the code must also be able to read the decisions. An internal wiki is not visible to external
  contributors and is not reviewed.
- A record must name the options we considered and why we chose one.
- Open decisions must be easy to find.
- Recording a decision must be cheap, or we will not do it.
- Agents must be able to read and write the records easily. Working with Confluence through its MCP server is annoying
  and requires setup. Git is easier.

## Considered Options

- Where:
  - In an internal wiki
  - In the repository under `docs/decisions/`
  - In the repository under `docs/adrs/`
- Format:
  - [MADR 4.0.0](https://adr.github.io/madr/): structured, with optional sections and a status in YAML frontmatter
  - No fixed format

## Decision Outcome

Chosen options: "in the repository under `docs/decisions/`" and "MADR 4.0.0". We prefer `docs/decisions/` to
`docs/adrs/`, because the records cover all decisions, not only architecture decisions. We use the upstream MADR
template directly and change its frontmatter:

- An optional `tags` field groups decisions by component or topic.
- `consulted` and `informed` are optional.
- There is no `date` field, because Git records when the status changed.

[`README.md`](README.md) describes the files, the frontmatter, the status values and the process.
