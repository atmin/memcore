# Interface

memcore is a command-line tool, since every agent has a shell.

Agents touch memory only through `memcore`: they run no git commands and edit no files. memcore runs every git command itself and owns placement, naming, indexing and committing. The agent brings only the content.

| | Command | Does |
| --- | --- | --- |
| read | `orient` | Shows the indexes from the working directory up to the root. At each level it prints the memories, skills, scripts, conventions and glossary, and the plans and notes as counts. It prints no sibling level. |
| | `show <path>` | Prints a document by its path. It prints a skill as you accepted it, and refuses a path through a symlink. |
| | `search <query>` | Searches memories, plans, notes and skills, from this level up by default. |
| | `recall <query>` | Finds passages in past transcripts on the transcripts branch. |
| | `history <path>` | Shows earlier versions of a document. |
| | `help [<command>]` | Shows the input of a command, with one valid example. `memcore <command> --help` does the same. |
| write | `remember` | Places, checks, indexes and commits a memory, which it reads from standard input with its frontmatter (see [A memory](memory.md)). `--at <dir>` places it at the level of a directory on the path up to home. `--at ~` places a fact about you at the root. When a memory on the path shares most words of the new description, it lists the matches and writes nothing. "Read", "when", "before" and "you" do not count, because most descriptions start with them. So the agent can revise one instead of adding another. `remember --new` writes anyway. It runs `verify` on the new memory before it commits. |
| | `revise <path or name> --reason <text>` | Replaces a document with the whole new version from standard input, and records the reason in the commit. A memory keeps its name and its sessions. If the last change of the document is still unpushed, the revision becomes a fixup of that commit. It runs `verify` and the secret filter first. |
| | `forget <path or name> --reason <text>` | Removes a document, or the folder of a skill, and records the reason. It refuses while another document links to it. A level left with no document goes too. When the queue created the document, the removal is a fixup, and the fold leaves no commit of either. |
| | `draft <kind> [<topic>]` | Starts any kind of document from its template, as `memcore help draft` lists them. It writes at the level of the working directory, or of `--at`. `revise` then fills it. |
| upkeep | `snapshot` | Commits new transcripts to the transcripts branch, trimmed and redacted. |
| | `sleep` | Runs a sleep pass, with one commit per change. The pass runs `verify` on every document. |
| | `verify <path>` | Checks the documents at a path, or any markdown, against the references they name, the glossaries on their path and the voice skill. |
| | `inbox` | Lists what waits on you: the unpushed commits with their reasons and files, and the stale indexes. It also warns of a remote that lost accepted commits. It fetches first. The open contradictions and the guard of the remote wait for later work. |
| yours | `push` | Refuses uncommitted changes, fetches, writes each stale index again, and folds in the fixups. Then it asks for confirmation at your terminal and pushes. The key that only you can use waits for later work (see [Git](git.md)). After the push, it moves the accepted checkout forward, and links and unlinks scripts to match. |
| | `purge <path>`, `purge --text` | Removes a document or a passage from every commit, then force pushes once, with the same confirmation and key as `push`. See [Git](git.md). |
| | `drop <commit>` | Removes a commit of the review queue, with its fixups, after it asks at your terminal. It records your reason, so sleep learns from it. |
| | `recover` | Runs after a fetch finds that the remote lost accepted commits. It asks at your terminal, drops them here, and moves the queue onto the remote (see [Git](git.md)). |
| setup | `init <remote>` | Sets memcore up on a machine: the memory repo and its credentials, its scripts, every installed agent, and onboarding. Today it sets up only the memory repo. It creates the repo when the remote is empty, and clones the remote otherwise. The rest waits for later work. See [Install](install.md). |

**Verify.** `verify` blocks a write only on exact faults: a dead reference, or a word that a glossary on the path forbids. A forbidden word counts as a whole word or phrase, outside code and links. `verify` only warns on heuristic faults:
- a long sentence or a long paragraph;
- an arrow or a filler word;
- a description that names no moment to read, which the check reads as a missing "when" or "before".

`verify` also warns on a memory, a plan, a note or a skill without a description. Its index line then says so, and no read fails on it.

Agents seldom call `verify` themselves, because the write commands and every sleep pass run it.

**Any agent.** The whole integration is one instruction in `AGENTS.md`<sup>[1](#sources)</sup>: run `memcore orient` at the start of every session and follow it. The output of `orient` teaches the rest: the commands, the rules, the indexes and the skill descriptions. Claude Code gets a `CLAUDE.md` whose only line imports the `AGENTS.md` (`@AGENTS.md`), and that import works in every version of Claude Code.

**Voice.** memcore ships the [voice](../skills/voice/SKILL.md) skill, which sets how agents talk to you and write. An agent never thinks to look up how to write, because it writes all the time. So `orient` pins the skill's "Always" and "Talking" parts, and the "Writing" part loads when an agent writes a document. The root copy is yours: sleep adapts it from your corrections about writing.

**Building.** memcore also ships three skills for changes to code: [plan](../skills/plan/SKILL.md), [build](../skills/build/SKILL.md) and [review](../skills/review/SKILL.md). The plan skill turns a task into small steps that an agent with no context can build. After you approve the plan, the build skill builds every step in a worktree, with one commit and one review per step. The review skill prefers an agent from another provider as the reviewer.

Where the agent's permissions allow it, `init` also denies the agent direct edits under the memory repo, so `memcore` stays the only way in. It denies `push` and `purge` there too. Skill formats are standard, but the place where each agent looks for skills is not. memcore serves skills itself, from the checkout of the accepted history, so that location never matters.

memcore ships as a single executable. It runs the installed git and the agent that you already use, so you install nothing else. See [Architecture](architecture.md).

## Sources

1. AGENTS.md <https://agents.md>
