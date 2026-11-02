# Install

Two commands set up memcore on a machine:

```
curl -fsSL https://memco.re/install | sh
memcore init git@github.com:you/memory.git
```

## The executable

The install script downloads the single executable for your operating system and processor into `~/.local/bin`. It needs no `sudo`, and it verifies the checksum. memcore is one file, so the script stays a few lines that you can read before you run it. To update, run the same line again. memcore needs only git and an agent that you already use.

## `memcore init <remote>`

Setup needs only the remote, because every memory repo has one. You can run `init` again safely. The status line of the [README](../README.md) says which of these steps exist today. It:
- **Sets up the memory repo.** If the remote is empty, `init` creates the memory repo in `~/memory`. If not, it clones the remote. So the same command sets up your first machine and every later one.
- **Makes the credentials.** `init` makes a write key bound to your presence, or to a passphrase that only you know, with the strongest guard available, and a read-only credential for the background fetch. You add both to the remote. See [Git](git.md).
- **Links the scripts.** `init` makes the checkout of the accepted history and links its scripts onto `~/.local/bin`, next to memcore. So every machine runs the same commands.
- **Connects your agents.** `init` finds the agents on the machine and writes the one instruction into the user-level `AGENTS.md` of each agent. For Claude Code, it also writes a `CLAUDE.md` that imports that file. When you run `init` again, it finds agents that you installed since.
- **Onboards you.** `init` runs [onboarding](onboarding.md) over the transcripts already on the machine. Onboarding runs your agent headless, so `init` asks before it spends the tokens of your agent.
- **Shows your inbox.** `init` ends with `memcore inbox`, which starts with the corrections you repeated most.

## Day to day

Agents run `memcore` themselves. You run `memcore inbox` to see what waits for your review, and `memcore push` to accept it.

Nothing runs between sessions: there is no service and no scheduled job. Snapshots, fetches and sleep passes start the way `git gc --auto` does:
1. An agent calls `memcore`.
2. If enough time has passed since the last snapshot or the last fetch, or enough sessions since the last sleep pass, `memcore` starts that work in the background.
3. `memcore` answers the agent at once, without waiting for that work.
