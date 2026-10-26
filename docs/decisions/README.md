# Decision Records

This directory records the decisions of this project. A decision can be about the architecture, the code, the tooling or
the way we work. Each record tells what we decided, which options we considered and why we chose one of them.

The records use [MADR 4.0.0](https://adr.github.io/madr/) (Markdown Architectural Decision Records). See
[0001](0001-record-decisions-in-the-repository.md) for the reasons.

## Files

- One file per decision: `NNNN-short-title.md`, for example `0007-package-the-extproc-as-a-sidecar.md`.
- `NNNN` is the next free number, with four digits. Numbers are never reused. If two pull requests use the same number,
  the pull request that merges second takes the next free number.
- The title states the decision or the problem, in a few words.
- Start from the [MADR template](https://github.com/adr/madr/blob/4.0.0/template/adr-template.md). Replace its
  frontmatter as described below. Remove optional sections that you do not need, see [Content](#content).

## Frontmatter

Each record starts with YAML frontmatter:

```yaml
---
status: proposed
decision-makers:
  - first.last@telekom.de
  - first.last@telekom.de
consulted:
  - first.last@telekom.de
tags: [chart]
---
```

| Field | Content |
| --- | --- |
| `status` | The state of the decision. See [Status](#status). |
| `decision-makers` | The email addresses of the people who make the decision. |
| `consulted` | Optional. The email addresses of the people whose opinion we ask before the decision (two-way communication). |
| `informed` | Optional. The email addresses of the people that we keep up to date (one-way communication). |
| `tags` | Optional. The components or topics that the decision is about, for example `chart`, `extproc`, `tooling`. |

The MADR template also has a `date` field. We do not use it, because Git records when a status changed:

```bash
git log --format='%cs %s' -G '^status:' -- docs/decisions/0001-record-decisions-in-the-repository.md
```

## Status

| Status | Meaning |
| --- | --- |
| `proposed` | The decision is open. The record describes the problem and the options. It can also recommend an option. |
| `accepted` | The team agreed on the decision. |
| `rejected` | The team considered the proposal and decided against it. |
| `deprecated` | The decision no longer applies, and no other decision replaces it. |
| `superseded by NNNN` | A newer decision replaces this one. |

## Process

1. Open a pull request with a new record in status `proposed`. We can merge a proposed record, so that open decisions
   are visible on `main`.
2. Discuss the decision in the pull request or in the team. Add the result to the record.
3. When the team agrees, set the status to `accepted` or `rejected`. The approval of this pull request confirms the
   decision.
4. Do not rewrite an accepted decision. To change it, write a new record and set the old one to `superseded by NNNN`.

To list all open decisions:

```bash
grep -l '^status: proposed' docs/decisions/[0-9]*.md
```

## Content

Keep records short. People must actually read them, and agents must still get enough context from them. Write down the
decision, the options and the reasons, and nothing more. Remove optional sections of the template that add nothing to
the record. Do not fill sections just because the template has them.

Write each record for the team and other internal contributors first. Make the decision and its reasons clear to them.
Use internal terms and link to internal sources, such as issues or wiki pages, when this makes the record clearer.

This is a public repository, so readers without internal access can also see the records. Explain the context for them
where this takes little effort. Do not include credentials, internal hostnames or customer data.
