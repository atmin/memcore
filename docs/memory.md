# A memory

A memory is one file that holds one fact. It lives at `memories/<name>.md` at its level. Its frontmatter has four keys:
- `name`: the topic, in lower case with hyphens. A name is unique in the memory repo, so a `[[name]]` link is never ambiguous.
- `description`: one line of 25 words at most that says when to read the memory. It is the memory's line in the index. memcore writes it in double quotes, so a colon inside it stays one value for any YAML reader.
- `type`: `user`, `feedback`, `project` or `reference`.
- `sessions`: the sessions the fact came from, each as `<agent>:<id>`. memcore fills it from the agent's environment: for Claude Code, from `CLAUDE_CODE_SESSION_ID`. memcore ignores sessions in the input. The list stays empty, as `sessions: []`, when no agent sets an id.

memcore reads the frontmatter with a small parser of its own, so it needs no YAML library. The parser accepts `key: value` lines and one list, the `sessions`. A value is plain, or in double quotes with the escapes of YAML. A plain value cannot hold `: ` or ` #`, or start with a sign such as `-`, `'` or `[`. Such a value needs double quotes. memcore refuses any other YAML, such as single quotes or a nested map.

The body states the fact. A feedback memory ends with **Why:** and **How to apply:**. A memory links to another memory with `[[name]]`.

For example, a feedback memory looks like this:

```markdown
---
name: tests-before-code
description: "Read before you write code: the tests of a step come before its code."
type: feedback
sessions:
  - claude:d4d1d99a-9439-4b7d-ae7a-25f063f909c9
---

Write the tests of a step before its code.

**Why:** A test written after the code proves what the code does, not what it should do.
**How to apply:** Name the tests in the plan, and commit them in the step with their code.
```
