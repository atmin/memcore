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
| | `stats` | Measures whether memory helps, per week, from the use log and git history. It calls no agent, and writes nothing beyond what every call of memcore writes. So an agent may run it too. See **Stats** below. |
| write | `remember` | Places, checks, indexes and commits a memory, which it reads from standard input with its frontmatter (see [A memory](memory.md)). `--at <dir>` places it at the level of a directory on the path up to home. `--at ~` places a fact about you at the root. When a memory on the path shares most words of the new description, it lists the matches and writes nothing. "Read", "when", "before" and "you" do not count, because most descriptions start with them. So the agent can revise one instead of adding another. `remember --new` writes anyway. It runs `verify` on the new memory before it commits. It also warns when the `orient` of its level passes the 9,500 bytes that the `SessionStart` hook shows in full. |
| | `revise <path or name> --reason <text>` | Replaces a document with the whole new version from standard input, and records the reason in the commit. A memory keeps its name and its sessions. If the last change of the document is still unpushed, the revision becomes a fixup of that commit. It runs `verify` and the secret filter first. |
| | `forget <path or name> --reason <text>` | Removes a document, or the folder of a skill, and records the reason. It refuses while another document links to it. A level left with no document goes too. When the queue created the document, the removal is a fixup, and the fold leaves no commit of either. |
| | `draft <kind> [<topic>]` | Starts any kind of document from its template, as `memcore help draft` lists them. It writes at the level of the working directory, or of `--at`. `revise` then fills it. |
| upkeep | `snapshot` | Commits the new transcripts of Claude Code to the transcripts branch, trimmed and redacted. `--from <dir>` reads an old copy of the projects folder. A call of memcore starts a snapshot in the background when six hours have passed since the last one. |
| | `sleep` | Runs a sleep pass, with one commit per change. The pass runs `verify` on every document. |
| | `verify <path>` | Checks the documents at a path, or any markdown, against the references they name, the glossaries on their path and the voice skill. |
| | `inbox` | Lists what waits on you: the unpushed commits with their reasons and files, and the stale indexes. It also warns of a remote that lost accepted commits. It fetches first, then prints a summary of the newest week of `stats` in three lines. The open contradictions and the guard of the remote wait for later work. |
| yours | `push` | Refuses uncommitted changes, fetches, writes each stale index again, and folds in the fixups. Then it asks for confirmation at your terminal and pushes. The key that only you can use waits for later work (see [Git](git.md)). After the push, it moves the accepted checkout forward, and links and unlinks scripts to match. |
| | `purge <path>`, `purge --text` | Removes a document or a passage from every commit, then force pushes once, with the same confirmation and key as `push`. See [Git](git.md). |
| | `drop <commit>` | Removes a commit of the review queue, with its fixups, after it asks at your terminal. It records your reason, so sleep learns from it. |
| | `recover` | Runs after a fetch finds that the remote lost accepted commits. It asks at your terminal, drops them here, and moves the queue onto the remote (see [Git](git.md)). |
| setup | `init <remote>` | Sets memcore up on a machine: the memory repo and its credentials, its scripts, every installed agent, and onboarding. Today it sets up the memory repo and Claude Code: the snapshot, the import, the hooks and the permissions. It creates the repo when the remote is empty, and clones the remote otherwise. `--dry-run` rehearses it, and `--undo` removes its changes to the settings. The rest waits for later work. See [Install](install.md). |
| | `hook <event>` | Runs a hook of Claude Code, with the hook input on standard input: `session-start`, `prompt`, `session-end` or `tool`. Only Claude Code runs it, so `orient` and `help` leave it out. |

**Verify.** `verify` blocks a write only on exact faults: a dead reference, or a word that a glossary on the path forbids. A forbidden word counts as a whole word or phrase, outside code and links. `verify` only warns on heuristic faults:
- a long sentence or a long paragraph;
- an arrow or a filler word;
- a description that names no moment to read, which the check reads as a missing "when" or "before".

`verify` also warns on a memory, a plan, a note or a skill without a description. Its index line then says so, and no read fails on it.

Agents seldom call `verify` themselves, because the write commands and every sleep pass run it.

**Stats.** `stats` measures whether memory helps, from the use log and git history alone. It reads no transcript, calls no agent and takes no argument. A week is 7 days that end at the time of the call, in UTC. `stats` prints the four newest weeks, newest first. It leaves out a week that ends before the log starts. For each week, it shows:
- **Capture:** the committed lines of `remember`, `revise`, `forget` and `draft`, for memories and for other documents. A fold changes no count.
- **Review:** the queue commits created, pushed and dropped, with the fixups among them. An accepted memory is a memory that a pushed commit adds or modifies, imports included. Each push counts it once, in the week of the push. The precision is the share of accepted memories that are no accepted mistake. The two newest weeks say "so far", because their mistakes can still grow.
- **Use:** the sessions, the reads per session, the reads around memcore, and the five most-read memories. Only reads with a session count here.
- **Context cost:** the words and the estimated tokens that `orient` printed, per session with a print. The header and each level show their words at their last print. Each level also shows its lines against the budget of 40.
- **Friction:** the failed calls of memcore, grouped by command and error. Errors that differ only in a path, a commit or a number fall in one group. The five largest groups show, then the count of the rest.
- **Marks:** the counts of `#again`, `#wrong` and `#stale`.

More rules of `stats`:
- A line without a session still counts for capture, review, friction and marks. A line that does not parse stays out, and `stats` names the count.
- `stats` reads the subject and the changed paths of each logged commit from git. A commit that changes nothing keeps its subject. `stats` names the count of logged commits that git lacks.
- Lines within one second keep the order of the log. So a `revise` in the second of its push, after the push in the log, is an accepted mistake.
- The estimated tokens are the words × 4 ÷ 3. `orient` prints many paths, so the true count is likely higher. The estimate serves to compare weeks and levels.
- Averages round half away from zero. The reads per session keep one decimal, and the words per session keep none.

Then `stats` lists the memories with no logged read in 30 days, 20 at most:
- A memory counts when it is at `HEAD` and older than 30 days.
- Its age comes from the author date of the newest commit that adds it, since a fold keeps author dates.
- Every read counts here, with or without a session.
- A use log younger than 30 days lists nothing, and says so.
- `stats` names the time of the first read around memcore, because an older log holds only the reads of `show`.

**Reads around memcore.** The `PostToolUse` and `PostToolUseFailure` hooks log the documents that a call of `Bash`, `Read` or `Grep` reads. So the reads also count what an agent reads around `memcore show`. A document is a regular file of the working tree, outside any `.git` folder, with no symlink on its path. The hooks find the documents by these rules:
- `Read` reads its `file_path`, and `Grep` reads its `path` when the path is a file. A relative path starts at `cwd`.
- `Bash` reads the files that `cat`, `head`, `tail`, `sed`, `awk`, `grep`, `rg` and `wc` name. A table holds the short options of each. A long option or a letter outside the table makes the command read nothing, as with `sed -i`. A pattern, the text after `<<<`, a heredoc and a destination after `>` read nothing.
- The hooks read a command as a shell does, in a simple form. They know quotes, `#` comments, heredocs, and a `\` that continues a line. They know the redirections `<`, `>`, `>>`, `>&`, `<&`, `&>`, `&>>` and `|&`, with a number before them. `cd` sets the folder of the later commands, unless a `|` joins it or a single `&` ends its list. Backticks and `(` run a subshell, so a `cd` inside them changes no folder after them.
- Only the input of `PostToolUse` and `PostToolUseFailure` counts. A failed call counts only for `Bash`, and only when its error starts with `Exit code `. That error means that the command ran.

The hooks read only this simple form, so `stats` calls these reads "around memcore". Their limits:
- a variable other than `$HOME`, such as a loop variable, counts nothing;
- globs, scripts and `git show <rev>:<path>` count nothing either;
- a literal path inside a loop counts once, however often the loop runs;
- `~user`, `cd -` and a `cd` of several words resolve to nothing, as does a later relative path;
- the hooks cannot tell which side of `&&` or `||` ran, so they count both.

**Any agent.** The whole integration is one instruction in `AGENTS.md`<sup>[1](#sources)</sup>: run `memcore orient` at the start of every session and follow it. The output of `orient` teaches the rest: the commands, the rules, the indexes and the skill descriptions.

**Claude Code.** Claude Code needs no instruction, because `init` adds five hooks to its settings:
- The `SessionStart` hook runs `orient` for the directory of the session, at every start, resume, clear, compaction and fork. Claude Code adds the output to the context, so a session sees `orient` without an instruction. The hook also logs the start of the session. The line in the use log holds the words that the hook printed, and the levels that it shortened. Claude Code passes a hook output over 10,000 characters only as a file and a preview. So the hook prints at most 9,500 bytes:
  - When the full `orient` fits, the hook prints it unchanged.
  - Otherwise, the first line names each level that the print shortens, and tells the agent to run `memcore orient`. Only the left-out text depends on an agent that follows this instruction.
  - A short print first leaves out the descriptions of the largest levels, one level at a time. Each path stays, with its `[unreviewed]` label.
  - When the paths of every level do not fit, the print replaces levels with one line each, the largest first. The line counts the documents of the level.
  - When even the header and the first line do not fit, the hook prints one fixed line. That line tells the agent to run `memcore orient`.
- The `UserPromptSubmit` hook finds your marks: `#again`, `#wrong` and `#stale`. It logs each mark, and tells the agent to find the memory and revise it, or to remember the fact. It logs no word of your prompt.
- The `SessionEnd` hook starts a snapshot in the background, and returns at once.
- The `PostToolUse` and `PostToolUseFailure` hooks run after each call of `Bash`, `Read` or `Grep`, by the matcher `Bash|Read|Grep`. They log the documents that the call read, as **Reads around memcore** above describes. They log no text of the call, always exit 0 and print nothing. A call that reads no document logs no line and starts no git process.

Each hook runs memcore by its full path, so it does not depend on the `PATH` of Claude Code. When the memory repo is not `~/memory`, each hook also sets `MEMCORE_REPO`. The `SessionStart` hook passes it on to the commands of the agent, through the environment file of the session.

**Voice.** memcore ships the [voice](../skills/voice/SKILL.md) skill, which sets how agents talk to you and write. An agent never thinks to look up how to write, because it writes all the time. So `orient` pins the skill's "Always" and "Talking" parts, and the "Writing" part loads when an agent writes a document. The root copy is yours: sleep adapts it from your corrections about writing.

**Building.** memcore also ships three skills for changes to code: [plan](../skills/plan/SKILL.md), [build](../skills/build/SKILL.md) and [review](../skills/review/SKILL.md). The plan skill turns a task into small steps that an agent with no context can build. After you approve the plan, the build skill builds every step in a worktree, with one commit and one review per step. The review skill prefers an agent from another provider as the reviewer.

Where the agent's permissions allow it, `init` also denies the agent direct edits under the memory repo. So `memcore` stays the only way in. It denies `push`, `drop` and `recover` there too, the commands that only you run. `init` gets no rule, because a rule cannot tell `--dry-run` apart. memcore refuses an agent for the rest of `init`. Skill formats are standard, but the place where each agent looks for skills is not. memcore serves skills itself, from the checkout of the accepted history, so that location never matters.

memcore ships as a single executable. It runs the installed git and the agent that you already use, so you install nothing else. See [Architecture](architecture.md).

## Sources

1. AGENTS.md <https://agents.md>
