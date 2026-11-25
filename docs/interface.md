# Interface

memcore is a command-line tool, since every agent has a shell.

Agents touch memory only through `memcore`: they run no git commands and edit no files. memcore runs every git command itself and owns placement, naming, indexing and committing. The agent brings only the content.

| | Command | Does |
| --- | --- | --- |
| read | `orient` | Shows the indexes from the working directory up to the root. At each level it prints the memories, skills, scripts, conventions and glossary, and the plans and notes as counts. It prints no sibling level. Before the indexes, it lists the commands that an agent runs, with one sentence each. It names the commands that only you run in one line. |
| | `show <path>` | Prints a document by its path. It prints a skill as you accepted it, and refuses a path through a symlink. |
| | `help [<command>]` | Shows the input of a command, with one valid example. `memcore <command> --help` does the same. |
| | `version` | Prints the version of memcore, and the commit that it was built from when the build records one. A build from source prints `dev` as its version. |
| | `stats [<path>]` | Measures whether memory helps, per week, from the use log and git history. With a path, it counts the reads of each memory, note and plan under that path instead. It calls no agent, and writes nothing beyond what every call of memcore writes. So an agent may run it too. See **Stats** below. |
| write | `remember` | Places, checks, indexes and commits a memory, which it reads from standard input with its frontmatter (see [A memory](memory.md)). `--at <dir>` places it at the level of a directory on the path up to home. `--at ~` places a fact about you at the root. `--topic <topic>` places it in a topic folder of the memories folder. When a memory on the path shares most words of the new description, it lists the matches and writes nothing. "Read", "when", "before" and "you" do not count, because most descriptions start with them. So the agent can revise one instead of adding another. `remember --new` writes anyway. It runs `verify` on the new memory before it commits. It also warns when the `orient` of its level passes the 9,500 bytes that the `SessionStart` hook shows in full. |
| | `revise <path or name> --reason <text>` | Replaces a document with the whole new version from standard input, and records the reason in the commit. A memory keeps its name and its sessions. If the last change of the document is still unpushed, the revision becomes a fixup of that commit. It runs `verify` and the secret filter first. |
| | `forget <path or name> --reason <text>` | Removes a document, or the folder of a skill, and records the reason. It refuses while another document links to it. A level left with no document goes too, and so does a topic folder. It removes the index of a topic folder only when the folder holds nothing else. When the queue created the document, the removal is a fixup, and the fold leaves no commit of either. |
| | `move <path or name> <new name or path> --reason <text>` | Renames a memory, a note or a plan, or moves it to a folder of its kind at any level. A move carries the document whole, with its sessions. A promoted lesson is rewritten, so it is a new memory through `remember --at`. The same commit sets the name in the frontmatter. It rewrites every link to the document, and the relative links inside it. For a memory it rewrites every `[[name]]` too. A topic folder or a level that the move empties goes. It refuses a taken name or an existing path. It also refuses a missing topic folder, and a move that adds a fault. Its commit is never a fixup. |
| | `draft <kind> [<name>]` | Starts any kind of document from its template, as `memcore help draft` lists them. It writes at the level of the working directory, or of `--at`. `draft topic memories/<topic> --description <text>` starts a topic folder, and `draft note <name> --topic <topic>` starts a note in one. `revise` then fills it. |
| upkeep | `snapshot` | Commits the new transcripts of Claude Code and Codex to the transcripts branch, trimmed and redacted. `--from <dir>` reads only an old copy of the projects folder of Claude Code. A call of memcore starts a snapshot in the background when six hours have passed since the last one. |
| | `verify <path>` | Checks the documents at a path, or any markdown, against the references they name and the glossaries on their path. It also warns on the name of a memory that moved or went. |
| | `inbox` | Lists what waits on you: the unpushed commits as the fold at push leaves them, and the stale indexes. The header counts the index commits that `push` adds apart from the commits that the fold leaves. An entry shows the message and the files of its commit after the fold. It counts the fixups that fold in. When the fold drops commits, the list ends with their count. It also warns of a remote that lost accepted commits. It fetches first, then prints a summary of the newest week of `stats` in three lines. With the automatic push on, it says so. Each entry of a commit of the queue then ends with its state. The state is ripe, the time after which memcore pushes it, or the reason that it is held. A last section lists the commits that memcore pushed automatically since your last push. `inbox` shows a likely secret in a message or a path as a placeholder. |
| | `push` | Refuses uncommitted changes, fetches, and folds in the fixups. It writes each stale index again before and after the fold. Then it asks for confirmation at your terminal and pushes. An agent runs it only when you ask. Without a terminal, it asks nothing and pushes a queue that holds only memories, notes, plans and indexes. For any other file, or a merge, it pushes nothing, and names the commits that you push at your terminal. After the push, it moves the accepted checkout forward, and links and unlinks scripts to match. `--expect <commit>` pushes nothing unless `main` is at that commit before the fetch, so it pushes only the queue that you reviewed. `--auto` is the automatic push, which memcore starts itself in the background, at most once an hour. It asks nothing, and pushes nothing unless you set `memcore.autopush`. The one exception is an earlier automatic push that has not ended: it sends those commits again. Then it pushes the ripe memories, notes and plans, and leaves every other commit in the queue. See [Automatic push](git.md#automatic-push). |
| | `reflect` | Runs one reflect pass: your agent, headless, lists your corrections in the new transcripts, and judges them against memories chosen from the path of the session. Only you run a pass, at a terminal. `--budget <dollars>` and `--sessions <n>` limit it. `reflect --list` prints the open corrections, and `reflect --answer <id>` records what happened to one. An agent may run both. See **Reflect** below. |
| yours | `drop <commit>` | Removes a commit of the review queue, with its fixups, after it asks at your terminal. It records your reason. |
| | `recover` | Runs after a fetch finds that the remote lost accepted commits. It asks at your terminal, drops them here, and moves the queue onto the remote (see [Git](git.md)). |
| | `ui` | Opens the window: a local page to review the queue, drop commits, push, browse the memory repo and search its text. See **The window** below. `--install` writes the application of the window, and `--uninstall` removes it. Both work before `init`. See [Install](install.md). |
| setup | `init <remote>` | Sets up the memory repo and Claude Code: the snapshot, the import, the hooks and the permissions. It creates the repo when the remote is empty, and clones the remote otherwise. `--dry-run` rehearses it, and `--undo` removes its changes to the settings. `--undo` fails when you answer no, and while a hook in the settings still runs memcore. Without a memory repo, `--undo` has nothing to undo. See [Install](install.md). |
| | `hook <event>` | Runs a hook of Claude Code, with the hook input on standard input: `session-start`, `prompt`, `session-end` or `tool`. Only Claude Code runs it, so `orient` and `help` leave it out. |

**The window.** `memcore ui` starts a server in the background, and opens the page in a window:
- The window is a native window of glaze, with no browser. On macOS, its menu gives Cmd+W, Cmd+Q, Cmd+H, Cmd+M and the edit shortcuts. Cmd+Q stops the server at once.
- A link out of a document opens in the default browser. Only `http`, `https` and `mailto` links open.
- When glaze cannot open a window, as on a Linux without WebKitGTK, the window falls back to a Chromium app window, or to a tab of the default browser. `MEMCORE_UI_WINDOW=chrome` forces that fallback.
- The server listens on `127.0.0.1` only. Each request of its API carries a token of the run, which the page gets in the address of the window.
- The server stops 10 s after the last window closes, or 60 s after it starts when no window connects.
- A second `memcore ui` opens one more window on the same server. One server runs for each memory repo.
- memcore.app runs its own copy of memcore. With no server running, the application is the server, so the Dock and Cmd+Tab show memcore. A click on its Dock icon with no window open opens a window, until the server stops.

**The icon.** `internal/ui/static/icon.svg` is the one source of the icon: a 16 x 16 pixel wordmark, MEM above CORE. It has black letters on white in light mode, and white letters on black in dark mode. The window, its favicon and memco.re use the file itself. The application icon draws its pixels as whole pixels: full bleed at 16 and 32 px, and inset on a white tile with round corners from 64 px.

The window has three views:
- **Review** lists the queue as `inbox` does. Entries that change a skill or a script come first. Each entry is a section that folds, with its arrow or with Enter. Its head counts the added and the removed lines. "Seen" folds the entry, and a seen entry starts folded. A push confirms in a dialog, and runs `push --expect`, so it pushes only the queue that the window shows. A drop confirms in a dialog, and records your reason.
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

`verify` warns, too, when a document names a memory that moved or went. git history gives these former names, and the warning names the path of a moved memory now. A name that a memory holds now never warns. A name counts as a whole word in prose or inline code, outside code blocks, links and `[[name]]`. Notes and indexes are not checked, because a note records what was true at its date. A name of one word is too common to check, so only a name with a hyphen counts.

Agents seldom call `verify` themselves, because the write commands run it.

**Stats.** `stats` measures whether memory helps, from the use log, git history and the records of the reflect passes. It reads no transcript and calls no agent. A week is 7 days that end at the time of the call, in UTC. `stats` prints the four newest weeks, newest first. It leaves out a week that ends before the log starts. For each week, it shows:
- **Capture:** the committed lines of `remember`, `revise`, `forget` and `draft`, for memories and for other documents. A fold changes no count.
- **Review:** the queue commits created, pushed and dropped. The fixups count among the created and the dropped. The commits of an agent's push and of an automatic push count among the pushed. An accepted memory is a memory that a pushed commit adds or modifies, imports included. Each push counts it once, in the week of the push. The precision is the share of accepted memories that are no accepted mistake. The two newest weeks say "so far", because their mistakes can still grow.
- **Use:** the sessions, the reads per session, the reads around memcore, and the five most-read memories. Only reads with a session count here. The sessions are those of each agent that sets a session id, Claude Code and Codex. A `codex exec` run counts too, because it uses memory.
- **Context cost:** the prints of `orient` per session with a print, and the words and estimated tokens of one print. It counts the same sessions as Use. A subagent can inherit the session id of its agent. So one session id can gather the prints of many agents. The header and each level show their words at their last print. Each level also shows its lines against the budget of 40.
- **Friction:** the failed calls of memcore, grouped by command and error. Errors that differ only in a path, a commit or a number fall in one group. The five largest groups show, then the count of the rest.
- **Marks:** the counts of `#again`, `#wrong` and `#stale`.
- **Misses:** the capture, retrieval and application misses and the stale memories, from the records of the reflect passes. A correction counts in the week of its own time, for a session of either agent. A correction with the verdict "before memory" or "unrelated" counts as no miss. Then come the judged sessions of Claude Code, of the sessions of Claude Code with their first logged start in the week. The use log cannot tell a session of Codex with you from a `codex exec` run, so these two counts leave out Codex. A week without a judged session says so.

More rules of `stats`:
- A line without a session still counts for capture, review, friction and marks. A line that does not parse stays out, and `stats` names the count.
- `stats` reads the subject and the changed paths of each logged commit from git. A commit that changes nothing keeps its subject. `stats` names the count of logged commits that git lacks.
- Lines within one second keep the order of the log. So a `revise` in the second of its push, after the push in the log, is an accepted mistake.
- The estimated tokens are the words × 4 ÷ 3. `orient` prints many paths, so the true count is likely higher. The estimate serves to compare weeks and levels.
- Averages round half away from zero. The reads and the prints per session keep one decimal, and the words per print keep none.
- A read before a `move` counts under the path that the document has now. A dropped move gives the reads of its new path back to the old path.

Then `stats` lists the memories with no logged read in 30 days, 20 at most:
- A memory counts when it is at `HEAD` and older than 30 days.
- Its age comes from the author date of the commit that added it, since a fold keeps author dates. A rename that git detects keeps the age, so a move keeps it. A path that a later commit adds again starts a new age.
- Every read counts here, with or without a session.
- A use log younger than 30 days lists nothing, and says so.
- `stats` names the time of the first read around memcore, because an older log holds only the reads of `show`.

`stats <path>` prints only the reads of each memory, note and plan under a path of the memory repo, such as `work/shop`. `.` covers the whole repo. It counts the whole use log, every read with a session or without one. A read counts under the path that its document has now, as in the weeks. The most-read document comes first, and ties sort by path. An unread document prints 0.

**Reflect.** `memcore reflect` runs one reflect pass, in the foreground. A pass never starts by itself, proposes no change and commits nothing. An agent in a session with you turns its corrections into changes, by the learn skill below. Only you run a pass, because it has two costs: it sends redacted transcripts to the provider of your agent, and it spends the limits of your plan.

A pass judges each file of the transcripts branch that fits all of these:
- It is the transcript of a main session of Claude Code or Codex, or a version beside it. A subagent gets its words from the agent, so a pass skips its file. A `codex exec` run is no session with you, because another agent writes its prompt. So a pass skips a transcript whose header holds `"source":"exec"`.
- The use log holds a start of the session. Only then memcore knows what the session saw.
  - A session of Claude Code starts at its first `SessionStart` hook. Its level and its directory come from that line.
  - A session of Codex has no hook, so it starts at its first call of memcore, such as the `memcore orient` of the instruction. Its level is the level of that call, and its directory comes from the header of the transcript.
  - An older version of memcore logs no session of Codex. So a session of Codex with only calls of such a version has no start, and no pass judges it.
- No record holds this version of the file. So a transcript that grew is judged again, and a correction of one session counts once.
- Its last line is older than one hour, so the session has most likely ended.

A pass works on the oldest session first, in two stages:
1. **Extract.** The agent lists your corrections in the transcript. A transcript over 300,000 bytes goes in parts. memcore keeps a correction only when its quote matches your text in one line. The record holds the time and the words of that line, and the one-sentence rule of the agent.
2. **Judge.** For each correction, memcore finds the latest session event at or before that time that names a commit. The judge gets the corrections for each commit together. If the path of the session holds five memories or fewer at that commit, the judge gets them all. Otherwise, memcore chooses memories without a model.
   - For each correction, BM25 ranks the memories by the words that the quote and the summary of the correction share with the description and the body of each memory. memcore chooses up to five memories with scores above zero.
   - memcore also includes every memory on that path that the session read at or before the group's latest correction.

   The judge gets each chosen memory once, in path order. It judges the group even when the list is empty. It can find that a chosen memory holds the fact or contradicts it. It can also find that none holds the fact, or that the correction concerns no memory.

If the choice misses a memory that holds the fact, the pass can report a capture miss. The learn skill checks memory again before making a change.

memcore turns each answer into a verdict:
- **capture miss:** no memory held the fact.
- **retrieval miss:** a memory held the fact, and the session did not read it before the correction.
- **application miss:** the session read that memory before the correction, and acted against it.
- **stale memory:** a memory says otherwise.
- **unrelated:** the correction concerns only the task of that session.
- **before memory:** memcore saw the session only after the correction. So memcore does not know what memory held then, and no agent judges the correction. It counts as no miss.

The use log decides between a retrieval miss and an application miss, not the agent. A line of `orient` is no read. Codex has no hook, so a read of a session of Codex is a `memcore show`.

A pass has two limits, and ends at the first:
- **The budget**, 2 dollars unless `--budget` or the setting `memcore.reflectbudget` says otherwise. The cost is what the agent reports, at API prices. On a subscription, a pass spends the limits of the plan and no money. A pass compares its cost with the budget before it starts a session. It finishes each session that it starts, so it can pass the budget by one session. No call of the agent gets a limit of its own.
- **`--sessions <n>`**, the most sessions of one pass. It needs no price, so it also limits an agent that reports no cost.

A pass prints one line for each session, with its level or its directory. Then it prints the totals: the judged sessions, the corrections by verdict, the cost and the sessions that wait. An error of the agent ends the pass with its cause, and the finished sessions stay recorded. A second pass refuses to run beside the first.

Three settings in the git configuration of the memory repo change a pass:
- `memcore.reflectbudget`: the budget in dollars.
- `memcore.reflectmodel`: the model of the agent, `sonnet` by default.
- `memcore.reflectagent`: the agent. `claude` is the default and the only value now.

The headless session of a pass is memcore's own. It runs in an empty folder, with no tools, no settings, no `CLAUDE.md` and no auto-memory, and it leaves no transcript. memcore marks it with `MEMCORE_INTERNAL`. A call of memcore from a marked session logs nothing, and starts no background work.

To judge every session again, delete `.git/memcore/reflect.jsonl` of the memory repo, and run a pass. The pass then reads each transcript again, at its full cost.

Each correction has an id: the SHA-256 of its session, its time and its quote. So the id stays the same when a later pass finds the same words. A print shows the first 7 digits of an id, and more while two recorded corrections share them.

A correction is open when it is a miss or a stale memory, and it has no answer. `memcore reflect --list` prints the open corrections, the newest session first. Each one has these lines:
- a head line: the id, the time, the level of the session, the verdict and the memory if any;
- your words, the rule and the reason of the judge, one line each.

A session outside home has no level, so the list shows its directory instead, such as `/tmp/work/checks`. The first line of the list counts every correction, and the open ones. `memcore reflect --list --all` prints every correction, and the answer of each answered one. The answer of a moved document shows the path that the document has now.

`--list --all` prints a copied turn once. A turn is one redacted `you` line of a transcript. A quote is the words of that line that state a correction. A session that continues another holds the same early turns, so one turn can carry corrections of several sessions.

A group needs two or more distinct sessions. They must share every transcript event through the quoted turn. Each quote must match exactly one `you` line at the recorded time. Later events may differ. A correction without this proof stays separate, such as one with a missing, unreadable or ambiguous transcript. Other verified copies can still group.

A group takes the place of its first member. Its heading shows the time. Each child heading carries its id, session, location and verdict. Each child shows its rule, followed by the reason and answer when present.

When every child quotes the same part of the turn, the group shows only that `Quote`, and no `You` line. When every child quotes the whole turn, the group shows the whole turn once under `You`. When the quotes differ, the group shows the whole turn under `You`, and each child shows its own partial quote. A quote of the full turn never repeats under a child. This example uses invented text and different quotes:

```text
2026-11-23 09:10  Copied turn
  You: Use the project template. Include the impact.
  f1995c2  ~/dev/shop  session 4e14814: before memory
    Quote: Use the project template.
    Rule: Use the project template.
  22a38b6  ~/dev/shop  session 6ef110c: retrieval miss, dev/shop/memories/template.md
    Quote: Include the impact.
    Rule: State the impact in the description.
    Why: The session did not read the memory.
    Answer: dev/shop/memories/template.md
```

`--list` groups its open corrections with the same proof and the same layout. Only open corrections can be members of a group there, so a group needs two open members, and a lone one stays flat. One session can form a group there. Its heading is `Same turn`. A group of several sessions keeps the heading `Copied turn`. Each child keeps its id, so every id stays answerable.

The totals count every correction once, including corrections outside the open list. A list reads a transcript only for a time that its visible corrections could group on, and reads each blob once. Use `--answer` with the ids of the corrections you want to answer.

`memcore reflect --answer <id>[,<id>]` records an answer: what happened to a correction. It takes an id, or its first 7 digits or more.
- `--with <path>` names the document of the memory repo that now holds the rule.
- `--reason <text>` says why. It stands alone for no change, and after `forget` removed the document.

An answer needs `--with`, `--reason` or both, and a second answer of a correction replaces the first. An answer changes no document. It is a line of the local state, `.git/memcore/answers.jsonl`. It still holds after you delete `reflect.jsonl`, when the next pass finds the same words. Its print follows each later `move` of its document, so a move needs no second answer. `--list` and `--answer` run no agent, so an agent may run them.

**Learn.** memcore ships the [learn](../skills/learn/SKILL.md) skill, which an agent follows in a session with you after a pass. The agent checks each open correction against memory as it is now. It groups corrections by rule and level, and ranks the groups by independent turns. A turn counts once for each rule and level. A correction outside a proven block counts as one, provisionally, and identical wording proves no copy. Equal groups keep the order of the open list. The CLI supplies the proof. The skill decides which corrections state one rule. Then it makes the change that fits the verdict, with `remember`, `revise` or `forget`, and records the answer. A correction that concerns only its task gets a reason and no memory. The skill makes at most ten changes in one session, and each change lands in the review queue. When `remember` warns about the bytes of `orient`, the skill adds no more memories at the top of that level.

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

**Any agent.** The whole integration is one instruction in `AGENTS.md`<sup>[1](#sources)</sup>. It says: run `memcore orient` at the start of every session and follow it. The output of `orient` teaches the rest: the commands, the rules, the indexes and the skill descriptions. `memcore help <command>` holds the full text and the input of a command.

memcore reads the session of Codex from `CODEX_THREAD_ID`, which Codex sets for each command that it runs. So the use log and a memory name the session of Codex. memcore logs no session of another agent.

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

**Cleanup.** memcore also ships the [cleanup](../skills/cleanup/SKILL.md) skill. It tidies one level of the memory repo while you watch: names, descriptions, topic folders, levels and sizes. It proposes every change before it applies one, and records its findings in a note at the level. Its survey counts the lines of the level in `orient`, and the bytes of the whole print. A print can pass the 9,500 bytes of the `SessionStart` hook far under the budget of 40 lines.

**Building.** memcore also ships three skills for changes to code: [plan](../skills/plan/SKILL.md), [build](../skills/build/SKILL.md) and [review](../skills/review/SKILL.md). The plan skill turns a task into small steps that an agent with no context can build. After you approve the plan, the build skill builds every step in a worktree. Each step gets one commit and one review. The review skill prefers an agent from another provider as the reviewer.

Where the agent's permissions allow it, `init` also denies the agent direct edits under the memory repo. So `memcore` stays the only way in. It denies `drop`, `recover` and `ui` there too, the commands that only you run. It leaves `push` allowed, because an agent pushes memories, notes and plans when you ask. `init` gets no rule, because a rule cannot tell `--dry-run` apart. memcore refuses an agent for the rest of `init`. Skill formats are standard, but the place where each agent looks for skills is not. memcore serves skills itself, from the checkout of the accepted history, so that location never matters.

memcore ships as a single executable. It runs the installed git, and for a reflect pass the agent that you already use. So you install nothing else. See [Architecture](architecture.md).

## Sources

1. AGENTS.md <https://agents.md>
