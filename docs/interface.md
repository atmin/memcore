# Interface

memcore is a command-line tool, since every agent has a shell.

Agents touch memory only through `memcore`: they run no git commands and edit no files. memcore runs every git command itself and owns placement, naming, indexing and committing. The agent brings only the content.

| | Command | Does |
| --- | --- | --- |
| read | `orient` | Shows the indexes from the working directory up to the root. At each level it prints the memories, skills, scripts, conventions and glossary, and the plans and notes as counts. It prints no sibling level. |
| | `show <path>` | Prints a document by its path. It prints a skill as you accepted it, and refuses a path through a symlink. |
| | `help [<command>]` | Shows the input of a command, with one valid example. `memcore <command> --help` does the same. |
| | `version` | Prints the version of memcore, and the commit that it was built from when the build records one. A build from source prints `dev` as its version. |
| | `stats` | Measures whether memory helps, per week, from the use log and git history. It calls no agent, and writes nothing beyond what every call of memcore writes. So an agent may run it too. See **Stats** below. |
| write | `remember` | Places, checks, indexes and commits a memory, which it reads from standard input with its frontmatter (see [A memory](memory.md)). `--at <dir>` places it at the level of a directory on the path up to home. `--at ~` places a fact about you at the root. When a memory on the path shares most words of the new description, it lists the matches and writes nothing. "Read", "when", "before" and "you" do not count, because most descriptions start with them. So the agent can revise one instead of adding another. `remember --new` writes anyway. It runs `verify` on the new memory before it commits. It also warns when the `orient` of its level passes the 9,500 bytes that the `SessionStart` hook shows in full. |
| | `revise <path or name> --reason <text>` | Replaces a document with the whole new version from standard input, and records the reason in the commit. A memory keeps its name and its sessions. If the last change of the document is still unpushed, the revision becomes a fixup of that commit. It runs `verify` and the secret filter first. |
| | `forget <path or name> --reason <text>` | Removes a document, or the folder of a skill, and records the reason. It refuses while another document links to it. A level left with no document goes too. When the queue created the document, the removal is a fixup, and the fold leaves no commit of either. |
| | `draft <kind> [<topic>]` | Starts any kind of document from its template, as `memcore help draft` lists them. It writes at the level of the working directory, or of `--at`. `revise` then fills it. |
| upkeep | `snapshot` | Commits the new transcripts of Claude Code to the transcripts branch, trimmed and redacted. `--from <dir>` reads an old copy of the projects folder. A call of memcore starts a snapshot in the background when six hours have passed since the last one. |
| | `verify <path>` | Checks the documents at a path, or any markdown, against the references they name and the glossaries on their path. |
| | `inbox` | Lists what waits on you: the unpushed commits as the fold at push leaves them, and the stale indexes. The header counts the index commits that `push` adds apart from the commits that the fold leaves. An entry shows the message and the files of its commit after the fold. It counts the fixups that fold in. When the fold drops commits, the list ends with their count. It also warns of a remote that lost accepted commits. It fetches first, then prints a summary of the newest week of `stats` in three lines. |
| | `push` | Refuses uncommitted changes, fetches, and folds in the fixups. It writes each stale index again before and after the fold. Then it asks for confirmation at your terminal and pushes. An agent runs it only when you ask. Without a terminal, it asks nothing and pushes a queue that holds only memories, notes, plans and indexes. For any other file, or a merge, it pushes nothing, and names the commits that you push at your terminal. After the push, it moves the accepted checkout forward, and links and unlinks scripts to match. `--expect <commit>` pushes nothing unless `main` is at that commit before the fetch, so it pushes only the queue that you reviewed. |
| yours | `drop <commit>` | Removes a commit of the review queue, with its fixups, after it asks at your terminal. It records your reason. |
| | `recover` | Runs after a fetch finds that the remote lost accepted commits. It asks at your terminal, drops them here, and moves the queue onto the remote (see [Git](git.md)). |
| | `ui` | Opens the window: a local page to review the queue, drop commits, push, browse the memory repo and search its text. See **The window** below. `--install` writes the application of the window, and `--uninstall` removes it. Both work before `init`. See [Install](install.md). |
| setup | `init <remote>` | Sets up the memory repo and Claude Code: the snapshot, the import, the hooks and the permissions. It creates the repo when the remote is empty, and clones the remote otherwise. `--dry-run` rehearses it, and `--undo` removes its changes to the settings. `--undo` fails when you answer no, and while a hook in the settings still runs memcore. Without a memory repo, `--undo` has nothing to undo. See [Install](install.md). |
| | `hook <event>` | Runs a hook of Claude Code, with the hook input on standard input: `session-start`, `prompt`, `session-end` or `tool`. Only Claude Code runs it, so `orient` and `help` leave it out. |

**The window.** `memcore ui` starts a server in the background, and opens the page in a window:
- The window is a Chromium app window when Chrome, Edge, Brave or Chromium is installed. Otherwise it is a tab of the default browser.
- The server listens on `127.0.0.1` only. Each request of its API carries a token of the run, which the page gets in the address of the window.
- The server stops 10 s after the last window closes, or 60 s after it starts when no window connects.
- A second `memcore ui` opens one more window on the same server. One server runs for each memory repo.

The window has three views:
- **Review** lists the queue as `inbox` does. Entries that change a skill or a script come first. A push confirms in a dialog, and runs `push --expect`, so it pushes only the queue that the window shows. A drop confirms in a dialog, and records your reason.
- **Browse** shows each level, its index and its documents. Mermaid blocks render as diagrams, with pele.
- **Search** finds text in every document, with `git grep`. It keeps no index on disk.

Review and Browse show the folders of the memory repo in a tree beside the documents:
- In Browse, every folder starts closed, and the window remembers the folders that you open. The folders above the open document open by themselves, and the tree scrolls to it.
- In Review, the tree marks each file that the queue changes with its status across the queue. The folders above a changed file start open. "Changed only" limits the tree to the changed files.
- In Review, a click on a changed file marks the entries that change it, and scrolls to the first. A click on another file shows that document in place of the queue, until "Back to the queue".

The status of a changed file is one of these:
- `A`: the queue adds the file.
- `M`: the queue changes the file.
- `D`: the queue removes the file.
- `AD`: the queue adds the file and then removes it.

The finder opens any file of the memory repo by a fuzzy match of its path. Cmd+P on macOS, Ctrl+P elsewhere, or "Search files by name" in the bar opens it, from every view. The arrows move through the files, and Enter opens one in Browse.

In Review, each file of an entry has three views of its change:
- **Changes**: the word diff of the text, which a changed markdown file shows first. When the word diff shows no change, as for a change of whitespace only, the file shows Patch first.
- **Patch**: the unified diff, which keeps changes of whitespace. Any other changed file shows it first.
- **New version**: the document rendered. An added or a removed file shows it first.

**Verify.** `verify` blocks a write only on exact faults. These are a dead reference, or a word that a glossary on the path forbids. A forbidden word counts as a whole word or phrase, outside code and links. `verify` only warns on heuristic faults in a description. Such a description names no moment to read, or has over 25 words. A description without "when" or "before" names no moment.

`verify` also warns on a memory, a plan, a note or a skill without a description. Its index line then says so, and no read fails on it.

Agents seldom call `verify` themselves, because the write commands run it.

**Stats.** `stats` measures whether memory helps, from the use log and git history alone. It reads no transcript, calls no agent and takes no argument. A week is 7 days that end at the time of the call, in UTC. `stats` prints the four newest weeks, newest first. It leaves out a week that ends before the log starts. For each week, it shows:
- **Capture:** the committed lines of `remember`, `revise`, `forget` and `draft`, for memories and for other documents. A fold changes no count.
- **Review:** the queue commits created, pushed and dropped. The fixups count among the created and the dropped. The commits of an agent's push count among the pushed. An accepted memory is a memory that a pushed commit adds or modifies, imports included. Each push counts it once, in the week of the push. The precision is the share of accepted memories that are no accepted mistake. The two newest weeks say "so far", because their mistakes can still grow.
- **Use:** the sessions, the reads per session, the reads around memcore, and the five most-read memories. Only reads with a session count here.
- **Context cost:** the prints of `orient` per session with a print, and the words and estimated tokens of one print. A subagent can inherit the session id of its agent. So one session id can gather the prints of many agents. The header and each level show their words at their last print. Each level also shows its lines against the budget of 40.
- **Friction:** the failed calls of memcore, grouped by command and error. Errors that differ only in a path, a commit or a number fall in one group. The five largest groups show, then the count of the rest.
- **Marks:** the counts of `#again`, `#wrong` and `#stale`.

More rules of `stats`:
- A line without a session still counts for capture, review, friction and marks. A line that does not parse stays out, and `stats` names the count.
- `stats` reads the subject and the changed paths of each logged commit from git. A commit that changes nothing keeps its subject. `stats` names the count of logged commits that git lacks.
- Lines within one second keep the order of the log. So a `revise` in the second of its push, after the push in the log, is an accepted mistake.
- The estimated tokens are the words × 4 ÷ 3. `orient` prints many paths, so the true count is likely higher. The estimate serves to compare weeks and levels.
- Averages round half away from zero. The reads and the prints per session keep one decimal, and the words per print keep none.

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

**Any agent.** The whole integration is one instruction in `AGENTS.md`<sup>[1](#sources)</sup>. It says: run `memcore orient` at the start of every session and follow it. The output of `orient` teaches the rest: the commands, the rules, the indexes and the skill descriptions.

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

**Voice.** memcore ships the [voice](../skills/voice/SKILL.md) skill, which sets how agents talk to you and write. An agent never thinks to look up how to write, because it writes all the time. So `orient` pins the skill's "Always" and "Talking" parts, and the "Writing" part loads when an agent writes a document.

**Building.** memcore also ships three skills for changes to code: [plan](../skills/plan/SKILL.md), [build](../skills/build/SKILL.md) and [review](../skills/review/SKILL.md). The plan skill turns a task into small steps that an agent with no context can build. After you approve the plan, the build skill builds every step in a worktree. Each step gets one commit and one review. The review skill prefers an agent from another provider as the reviewer.

Where the agent's permissions allow it, `init` also denies the agent direct edits under the memory repo. So `memcore` stays the only way in. It denies `drop`, `recover` and `ui` there too, the commands that only you run. It leaves `push` allowed, because an agent pushes memories, notes and plans when you ask. `init` gets no rule, because a rule cannot tell `--dry-run` apart. memcore refuses an agent for the rest of `init`. Skill formats are standard, but the place where each agent looks for skills is not. memcore serves skills itself, from the checkout of the accepted history, so that location never matters.

memcore ships as a single executable. It runs the installed git, so you install nothing else. See [Architecture](architecture.md).

## Sources

1. AGENTS.md <https://agents.md>
