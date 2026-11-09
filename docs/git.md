# Git

A memory repo always has a remote, and any git remote will do. The pushed commits are the accepted history. The commits after it are the review queue, and when you push them, you accept them.

- Agents commit through `memcore` as they go, one change per commit, without asking.
- You review the queue in bulk, at a time you choose.
- A correction of an unpushed change becomes a fixup commit. `push` folds each fixup into the commit it corrects.
- Until you push, history can change: `push` folds fixups, and `drop` removes a commit.
- memcore never rewrites pushed history, and never pushes with `--force`.
- An agent pushes memories, notes and plans when you ask. Only you push the other documents.
- You drop a commit of the queue with `memcore drop`, at your terminal, and the drop keeps your reason.

Each commit of memcore names its command and its path, as `remember: dev/memories/tests-before-code.md`. The commands that commit are `remember`, `revise`, `forget`, `move` and `draft`. A move names both paths, as `move: <old path> to <new path>`, and is never a fixup. `push` adds `index: <path>` for an index that a hand edit left stale. The body of a `revise`, a `forget` or a `move` holds its reason. A fixup is `fixup! <subject of its target>`, with its command, its path and its reason in the body. The first commit of a new memory repo is `init: create the memory repo`. `init` commits each import of auto-memory as `init: import the auto-memory of <dir>`. A snapshot commits to the transcripts branch as `snapshot: <n> transcripts`, never to `main`.

**Fold.** `push` folds the queue with one interactive rebase, and never rewrites the accepted history. A fixup folds into the nearest earlier commit with its subject. A chain that leaves every document as it found it, such as a remember and its forget, goes whole. Indexes are generated, so the fold writes each index again after each commit. The index of a topic folder keeps its frontmatter, and only its body is generated. A conflict in that frontmatter stops the fold, unless only one side changed it. A conflict in any other document stops the fold and leaves the queue as it was. A chain that goes whole can leave an index stale, so `push` writes each stale index again after the fold.

`inbox` runs the same fold on a scratch clone of `main`, to show what `push` would leave. It shows an index that `push` would commit as `new`. The header of `inbox` counts the index commits that `push` adds apart from the commits that the fold leaves. Only `push` folds the memory repo. The clone runs no hook, file system monitor or signing helper. When the fold in the clone stops, `inbox` warns and lists each commit of the queue as it is.

Agents load unreviewed memories at once, and `orient` labels them as unreviewed. A bad memory lasts until your next review, and the review leaves a clean history.

Hostile text can make an agent plant a memory or a skill. Such text is a web page, an issue or an email. Later agents then follow that memory or skill.

Skills and scripts are code, so they run only after you push them. memcore keeps a checkout of the accepted history under its local state. A copy of the memory repo gets a checkout of its own, and never moves the checkout of the original. memcore moves the checkout forward at every push and fetch. It serves skills and links scripts from that checkout. So a revision in the review queue changes nothing that runs. A helper beside a script is the accepted helper.

## Only you

An agent runs as you. It can fake a terminal and answer a prompt. So nothing on your machine can stop an agent from calling `push`.

Two guards stop an agent's mistakes, and you do not rely on them. memcore refuses `drop` and `recover` when it finds no controlling terminal or sees the variables that agents set. `init` denies `drop`, `recover` and `ui` in the permissions of Claude Code, but not `push`. Where the host can forbid force pushes, keep that protection on.

The window of `memcore ui` is yours too. memcore refuses `ui` when it sees the variables that agents set. In the window, you confirm a push or a drop in a dialog instead of at the terminal, so a push from the window can push skills and scripts. The token of the server guards against an agent's mistake only: an agent that runs as you can read it.

An agent pushes when you ask it to. memcore cannot see the chat, so `orient` tells an agent to push only then. Skills and scripts run from the accepted history, so their push stays yours. `push` uses the credential that git already has, for you and for an agent alike.

Without a terminal, or with the variables that agents set, `push` asks nothing. It checks the queue that the fold leaves. It pushes the whole queue only when every commit changes only memories, notes, plans and indexes. Otherwise it pushes nothing. It names each commit that you push at your terminal: a merge, or a commit with any other file.

## Fetch

`inbox` and `push` fetch at once, under the write lock:
- memcore moves the accepted checkout forward, rebases the review queue onto the fetched tip, and links any new scripts. A conflict in an index writes the index again, by the rule of the fold for a topic index. A fetched memory needs no review, because a push accepted it.
- When the queue and the fetched history change the same document, the fetch stops and names the paths. The queue stays as it was.
- A fetched tip can lack the accepted history as it stood before. Then memcore keeps `origin/main`, rebases nothing, and refuses every write. `memcore recover` asks at your terminal, drops the lost commits here, and moves the queue onto the remote.
