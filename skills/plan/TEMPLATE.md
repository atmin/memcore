# Plan template

Copy the block below into a new plan. Omit a section rather than fill it with boilerplate.

```markdown
---
name: <topic>
description: Read when you build <the task in one line>.
---

# <the task in one line>

**Task:** <link to the ticket or the issue, if any>
**Design:** <link to the design doc> § <sections>
**Conventions:** <link to the conventions of the repo>
**Repos:** `<path>` · branch `<name>` off `<base>`, with one line for each repo
**Status:** not started | ready for review | <link to the pull request>

## Instructions

Build the steps with the build skill. Stop and report when:
- a change on the base invalidates a fact in Current state, a constraint or a step;
- the acceptance criteria and the steps disagree;
- the work needs a decision that no constraint covers.

## Requirement

Three sentences at most: what must be true when this is done, and for whom.

## Acceptance criteria

1. <a testable statement>. Step <n>.

## Current state

Verified at `<repo>@<short sha>`.

- **Exists:** `<path::symbol>`: <what it already gives>.
- **Missing:** <the gap that the steps fill>.

## Constraints

One line each: **<decision>.** <reason>.

## Steps

1. **<goal>.** <what changes, and where>.
   Pattern: <the convention that it follows>.
   Evidence: `<path::test_name>`, or the check for a change that a test cannot prove.
   Commit: `<one line>`

## Verification

The commands in the conventions of the repo, plus any that this task needs.

## Out of scope

- <thing>: <where it belongs>

## Open questions

None at handover. A builder that stops adds its question here: the question, the options and who answers.

## Done when

- [ ] The evidence of every acceptance criterion passes on the last commit.
- [ ] Every review finding has a fixup or an answer.
- [ ] The ready-for-review report is sent.
```
