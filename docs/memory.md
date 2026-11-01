# A memory

A memory is one file that holds one fact. It lives at `memories/<name>.md` at its level. Its frontmatter has four keys:
- `name`: the topic, in lower case with hyphens. A name is unique in the memory repo, so a `[[name]]` link is never ambiguous.
- `description`: one line of 25 words at most that says when to read the memory. It is the memory's line in the index.
- `type`: `user`, `feedback`, `project` or `reference`.
- `sessions`: the sessions the fact came from, each as `<agent>:<id>`. memcore fills it from the agent's environment: for Claude Code, from `CLAUDE_CODE_SESSION_ID`. It stays empty when no agent sets an id.

The body states the fact. A feedback memory ends with **Why:** and **How to apply:**. A memory links to another memory with `[[name]]`.

For example, a feedback memory looks like this:

```markdown
---
name: tests-before-code
description: Read before you write code: the tests of a step come before its code.
type: feedback
sessions:
  - claude:d4d1d99a-9439-4b7d-ae7a-25f063f909c9
---

Write the tests of a step before its code.

**Why:** A test written after the code proves what the code does, not what it should do.
**How to apply:** Name the tests in the plan, and commit them in the step with their code.
```
