---
name: review
description: Use when a plan or a commit needs a review. It runs a reviewer with no context, preferably from another provider, and records the findings.
---

# Review

A reviewer with no context finds what the author cannot see. An agent from another provider finds the most, so prefer one.

## Choose the reviewer

1. If an agent from another provider is installed, use it. For example, Codex reviews the work of Claude, and Claude reviews the work of Codex.
2. If no such agent is installed, run a new headless session of your own agent.

## Run the review

The reviewer only reads, and it changes nothing. Keep the findings in an untracked file in the git directory of the repo:

    L=$(git rev-parse --git-dir)/reviews; mkdir -p "$L"

The prompt, `<P>`:

    Review <commit <sha> (git show <sha>) | the plan <path>> against <the plan path> and <the conventions path>. Report findings only, one per line: severity, path::symbol, problem, fix. Change nothing. End with the line END OF REVIEW.

- Codex as the reviewer: `codex exec -s read-only -o "$L/<name>.md" "<P>" </dev/null`. Without `</dev/null`, a run in the background waits for input and never ends.
- Claude as the reviewer: `claude -p "<P>" --tools "Read,Grep,Glob,Bash" --permission-mode dontAsk --allowedTools "Bash(git show:*)" "Bash(git diff:*)" "Bash(git log:*)" --add-dir <plan directory> --setting-sources "" --strict-mcp-config > "$L/<name>.md"`

`<name>` is the sha of the commit, or `plan` for the review of a plan. The headless mode of another agent works the same way.

## Answer

Append an `## Answers` section to the review file. Answer each finding with `fixup <sha>` or the reason that it stands. A review without `END OF REVIEW` is incomplete, so run it again. Ask for a second review only when a fix grew beyond its finding.
