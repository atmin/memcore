# Architecture

memcore is one Go executable. It runs the installed git and the agent that you already use, so it ships no model and no git of its own.

## Parts

1. **Commands.** The `memcore` command line. Its output, such as the output of `orient`, is plain text that an agent reads.
2. **Store.** The memory repo, its indexes and the transcripts branch. memcore changes the store only through the installed git.
3. **Readers.** One adapter for each agent's transcript format, such as the JSONL files of Claude Code. Each adapter converts its format into one trimmed conversation format.
4. **Thinking.** Extraction, merges, promotions and contradiction checks. memcore runs your agent headless for this work, for example with `claude -p` or `codex exec`.
5. **Guards.** Redaction, `verify`, the write lock, and the confirmation and the key behind `push` and `purge`.

## Git

memcore runs the installed git. It needs rebase with autosquash, git plumbing, and your credential helpers and SSH keys, so a git library is not enough. Everyone who runs coding agents already has git.

The future encrypted remote uses the remote-helper protocol of git. For a custom URL scheme, git calls a program named `git-remote-<scheme>`. memcore is that program under a second name, so memcore stays one executable.

## Thinking

The agent that you already use does the thinking, run headless. It sees only redacted text. This choice ships no model, and the judgment comes from a frontier model.

The choice has three costs:
- A background sleep pass spends the tokens of your agent.
- If you use several agents, one provider can see the sessions of another provider.
- Redacted text leaves your machine.

Redaction stays local. It uses patterns and entropy for secrets, and possibly a small local model for names and personal data.

## Concurrency

Several sessions and worktrees can call memcore at the same time. Reads take no lock. Each write takes one short lock on the memory repo. A sleep pass works in its own git worktree, then lands with a short rebase under the lock, so it never blocks agents for long. A fetch lands the same way. A purge holds the lock for its whole run.

## State and search

- Search is a text search over the working tree, plus `git grep` on the transcripts branch. memcore keeps no search index on disk.
- Local state lives under `.git/memcore/`, and memcore never commits it. It holds the time of the last snapshot and the checkout of the accepted history, a worktree of the remote-tracking branch.
