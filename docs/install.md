# Install

Two commands set up memcore on a machine:

```
curl -fsSL https://memco.re/install | sh
memcore init git@github.com:you/memory.git
```

## The executable

The install script downloads the single executable for your operating system and processor into `~/.local/bin`. It needs no `sudo`, and it verifies the checksum. memcore is one file, so the script stays a few lines that you can read before you run it. To update, run the same line again. memcore needs only git and an agent that you already use.

## `memcore init <remote>`

Setup needs only the remote, because every memory repo has one. You can run `init` again safely. The steps marked as later work do not exist yet. It:
- **Sets up the memory repo.** If the remote is empty, `init` creates the memory repo in `~/memory`. If not, it clones the remote. So the same command sets up your first machine and every later one.
- **Makes the credentials.** Later work: `init` makes a write key bound to your presence, or to a passphrase that only you know, with the strongest guard available, and a read-only credential for the background fetch. You add both to the remote. See [Git](git.md).
- **Links the scripts.** Later work: `init` makes the checkout of the accepted history and links its scripts onto `~/.local/bin`, next to memcore. So every machine runs the same commands.
- **Snapshots the transcripts.** `init` first commits every transcript of Claude Code to the transcripts branch. So the transcripts are safe before anything else can fail. See [Transcripts](transcripts.md).
- **Imports auto-memory.** `init` imports each file of the auto-memory of Claude Code as a memory, at the level of its project. It makes one commit per project. It skips `MEMORY.md`, the index of auto-memory, so a fact that only that index holds stays out. A file that fails a check stays out, and `inbox` lists it with its reason. When every file is in, or you accepted the rest at the terminal, `init` turns auto-memory off.
- **Connects your agents.** For Claude Code, `init` adds three hooks to `~/.claude/settings.json`, and denies some tools and commands. See [Interface](interface.md). Before it changes the settings, it shows the diff and asks once. It keeps a copy of the original under `.git/memcore/init/`. For other agents, `init` will write the one instruction into the user-level `AGENTS.md` of each.
- **Onboards you.** Later work: `init` runs [onboarding](onboarding.md) over the transcripts already on the machine. Onboarding runs your agent headless, so `init` asks before it spends the tokens of your agent.
- **Shows your inbox.** `init` ends with the report of `memcore inbox`, without a fetch. Later, the report starts with the corrections you repeated most.

A second run adds only what is new: new transcripts, new files of auto-memory, and a hook that you removed. Two flags help you try `init` and leave it:
- `memcore init --dry-run <remote>` builds the memory repo at `MEMCORE_REPO`, and prints the change of the settings without making it. It asks nothing, so an agent may run it too.
- `memcore init --undo` removes what `init` added to the settings, and keeps every later change. It names an addition that you changed since, and leaves it.

## Day to day

Agents run `memcore` themselves. You run `memcore inbox` to see what waits for your review, and `memcore push` to accept it.

Nothing runs between sessions: there is no service and no scheduled job. Snapshots, fetches and sleep passes start the way `git gc --auto` does:
1. An agent calls `memcore`.
2. If enough time has passed since the last snapshot or the last fetch, or enough sessions since the last sleep pass, `memcore` starts that work in the background.
3. `memcore` answers the agent at once, without waiting for that work.

A snapshot also starts at the end of each session of Claude Code, through its `SessionEnd` hook.
