---
name: plan
description: Use when the person asks for a plan, or before a change that needs more than one commit. It turns a task into small, testable steps that an agent with no context can build.
---

# Plan

A plan gives an agent with no context everything it needs to do one task. A plan names files, functions and tests, because it is deleted when its work merges.

## Rules

1. **No context.** Put everything that the builder needs in the plan, or one link away. That includes the task, the design docs and the conventions. Never assume that the builder saw this conversation.
2. **Criteria define success, and steps define the changes.** Map every acceptance criterion to a step. The builder changes nothing that no step names.
3. **Small steps that stay green.** Each step passes the tests, the type checks and the lint on its own. No step depends on a later step.
4. **Evidence for each step.** Name the tests that prove a step, and write them before its code. For a change that a test cannot prove, name the check instead.
5. **Existing patterns only.** Name the convention that each step follows. Raise a new pattern as an open question, with the complexity that it removes.
6. **Decisions belong in the design docs.** Cite them as constraints. A missing decision is an open question.
7. **The doc update is a step.** When behaviour changes, a step rewrites the affected docs as current truth.
8. **Stable anchors.** Point at code as `path::symbol`, and at a doc as `path` § heading. Never use a line number.
9. **One verified commit.** Check every anchor against the checkout, and state the commit that you checked.

## Procedure

1. Read the task, the design docs and the conventions of the repo.
2. Write the plan from [TEMPLATE.md](TEMPLATE.md). With memcore, start it with `memcore draft plan <topic>`.
3. Ask the person the open questions. A plan goes to a builder only when it has none.
4. Have the plan reviewed with the [review](../review/SKILL.md) skill, and answer each finding in the plan.
5. Hand over the plan. The person approves it before the build starts.
