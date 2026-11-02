# Interface

memcore is a command-line tool, since every agent has a shell.

Agents touch memory only through `memcore`: they run no git commands and edit no files. memcore runs every git command itself and owns placement, naming, indexing and committing. The agent brings only the content.

| | Command | Does |
| --- | --- | --- |
| read | `orient` | Shows the indexes from the working directory up to the root. At each level it prints the memories, skills, scripts, conventions and glossary, and the plans and notes as counts. It prints no sibling level. |
| | `show <path>` | Prints a memory, plan, note or skill. |
| | `search <query>` | Searches memories, plans, notes and skills, from this level up by default. |
| | `recall <query>` | Finds passages in past transcripts on the transcripts branch. |
| | `history <path>` | Shows earlier versions of a document. |
| write | `remember` | Places, checks, indexes and commits a memory, which it reads from standard input with its frontmatter (see [A memory](memory.md)). When a memory on the path shares most of the words of the new description, it lists the matches and writes nothing. "Read", "when", "before" and "you" do not count, because most descriptions start with them. So the agent can revise one instead of adding another. `remember --new` writes anyway. It runs `verify` on the new memory before it commits. |
| | `revise <path>` | Replaces a document's content, read from standard input, and records the reason. If the document's last change is still unpushed, the revision becomes a fixup of that commit. It then runs `verify` on the document. |
| | `forget <path>` | Removes a document, with the reason. |
| | `draft <kind> <topic>` | Starts a plan, a note or a script from its template, at the right level. `revise` then fills it. |
| upkeep | `snapshot` | Commits new transcripts to the transcripts branch, trimmed and redacted. |
| | `sleep` | Runs a sleep pass, with one commit per change. The pass runs `verify` on every document. |
| | `verify <path>` | Checks the documents at a path, or any markdown, against the references they name, the glossaries on their path and the voice skill. |
| | `inbox` | Lists what waits on you: the unpushed commits and the open contradictions. It fetches first, and it shows which guard protects the remote. |
| yours | `push` | Fetches, folds in the fixups, then pushes. It asks for confirmation at your terminal and signs with the key that only you can use (see [Git](git.md)). After the push, it moves the accepted checkout forward, and links and unlinks scripts to match. |
| | `purge <path>`, `purge --text` | Removes a document or a passage from every commit, then force pushes once, with the same confirmation and key as `push`. See [Git](git.md). |
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
