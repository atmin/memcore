# Install

Build memcore from source, then set it up on a machine with one command:

```
memcore init git@github.com:you/memory.git
```

## The executable

memcore builds from source with Go 1.27. Clone the memcore repo, and run this command in it:

```
CGO_ENABLED=0 go build -o ~/.local/bin/memcore ./cmd/memcore
```

Put `~/.local/bin` on `PATH` before you run `init`. Without memcore on `PATH`, `init` adds no hook. To update, pull the repo and build again. At run time, memcore needs only git.

## `memcore init <remote>`

Setup needs only the remote, because every memory repo has one. You can run `init` again safely. It:
- **Sets up the memory repo.** If the remote is empty, `init` creates the memory repo in `~/memory`. If not, it clones the remote. So the same command sets up your first machine and every later one.
- **Snapshots the transcripts.** `init` first commits every transcript of Claude Code to the transcripts branch. So the transcripts are safe before anything else can fail. See [Transcripts](transcripts.md).
- **Imports auto-memory.** `init` imports each file of the auto-memory of Claude Code as a memory, at its project's level. It makes one commit per project. It skips `MEMORY.md`, the index of auto-memory, so a fact that only that index holds stays out. A file that fails a check stays out, and `inbox` lists it with its reason. When every file is in, or you accepted the rest at the terminal, `init` turns auto-memory off.
- **Connects your agents.** For Claude Code, `init` adds five hooks to `~/.claude/settings.json`, and denies some tools and commands. See [Interface](interface.md). Before it changes the settings, it shows the diff and asks once. It keeps a copy of the original under `.git/memcore/init/`.
- **Shows your inbox.** `init` ends with the report of `memcore inbox`, without a fetch.

The snapshot and the import need no agent. Each file of auto-memory becomes a memory at the level of its project, and keeps its session. `MEMORY.md` is only the index of those files, so `init` skips it, with any fact that it holds alone. A fact that two projects hold stays twice, once at each project.

A second run adds only what is new: new transcripts, new files of auto-memory, and each missing hook. Two flags help you try `init` and leave it:
- `memcore init --dry-run <remote>` builds the memory repo at `MEMCORE_REPO`, and prints the change of the settings without making it. It asks nothing, so an agent may run it too.
- `memcore init --undo` removes what `init` added to the settings, and keeps every later change. It names an addition that you changed since, and leaves it.

## Day to day

Agents run `memcore` themselves. You run `memcore inbox` to see what waits for your review, and `memcore push` to accept it. `push` and a fetch link the accepted scripts onto `~/.local/bin`.

Nothing runs between sessions: there is no service and no scheduled job. Snapshots start the way `git gc --auto` does:
1. An agent calls `memcore`.
2. If six hours have passed since the last snapshot, `memcore` starts a snapshot in the background.
3. `memcore` answers the agent at once, without waiting for the snapshot.

A snapshot also starts at the end of each session of Claude Code, through its `SessionEnd` hook.
