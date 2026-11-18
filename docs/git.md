# Git

A memory repo always has a remote, and any git remote will do. The pushed commits are the accepted history. The commits after it are the review queue, and when you push them, you accept them.

- Agents commit through `memcore` as they go, one change per commit, without asking.
- You review the queue in bulk, at a time you choose.
- A correction of an unpushed change becomes a fixup commit. `push` folds each fixup into the commit it corrects.
- Until you push, history can change: `push` folds fixups, and `drop` removes a commit.
- memcore never rewrites pushed history, and never pushes with `--force`.
- An agent pushes memories, notes and plans when you ask. Only you push the other documents.
- When you turn the automatic push on, memcore pushes ripe memories, notes and plans by itself. Such a commit is accepted without a review.
- You drop a commit of the queue with `memcore drop`, at your terminal, and the drop keeps your reason.

Each commit of memcore names its command and its path, as `remember: dev/memories/tests-before-code.md`. The commands that commit are `remember`, `revise`, `forget`, `move` and `draft`. A move names both paths, as `move: <old path> to <new path>`, and is never a fixup. `push` adds `index: <path>` for an index that a hand edit left stale. The body of a `revise`, a `forget` or a `move` holds its reason. A fixup is `fixup! <subject of its target>`, with its command, its path and its reason in the body. The first commit of a new memory repo is `init: create the memory repo`. `init` commits each import of auto-memory as `init: import the auto-memory of <dir>`. A snapshot commits to the transcripts branch as `snapshot: <n> transcripts`, never to `main`.

**Fold.** `push` folds the queue with one interactive rebase, and never rewrites the accepted history. A fixup folds into the nearest earlier commit with its subject. A chain that leaves every document as it found it, such as a remember and its forget, goes whole. Indexes are generated, so the fold writes each index again after each commit. The index of a topic folder keeps its frontmatter, and only its body is generated. A conflict in that frontmatter stops the fold, unless only one side changed it. A conflict in any other document stops the fold and leaves the queue as it was. A chain that goes whole can leave an index stale, so `push` writes each stale index again after the fold.

A folded commit keeps the author date of its target, and loses the dates of its fixups. The automatic push needs the newest of those dates. So before a fold rewrites a commit, it records the newest author date of each chain in the local state.

`inbox` runs the same fold on a scratch clone of `main`, to show what `push` would leave. It shows an index that `push` would commit as `new`. The header of `inbox` counts the index commits that `push` adds apart from the commits that the fold leaves. Only `push` folds the memory repo. The clone runs no hook, file system monitor or signing helper. When the fold in the clone stops, `inbox` warns and lists each commit of the queue as it is.

Agents load unreviewed memories at once, and `orient` labels them as unreviewed. A bad memory lasts until your next review, and the review leaves a clean history. With the automatic push on, a memory can leave the queue before you read it. It loses the label then.

Hostile text can make an agent plant a memory or a skill. Such text is a web page, an issue or an email. Later agents then follow that memory or skill.

Skills and scripts are code, so they run only after you push them. memcore keeps a checkout of the accepted history under its local state. A copy of the memory repo gets a checkout of its own, and never moves the checkout of the original. memcore moves the checkout forward at every push and fetch. It serves skills and links scripts from that checkout. So a revision in the review queue changes nothing that runs. A helper beside a script is the accepted helper.

## Automatic push

The automatic push is off until you turn it on. You set its delay in the git configuration of the memory repo:

    git config memcore.autopush 24h

The value is a duration, such as `24h` or `90m`, and `0s` means no delay. The setting holds for one machine. To turn the automatic push off, unset the key. The remote needs an accepted history first, so your first push comes before any automatic push.

With the setting on, memcore pushes memories, notes and plans without a question. Skills, scripts, helpers, the conventions file and the glossary still reach the remote only through your push. A command of memcore starts `memcore push --auto` in the background, at most once an hour.

The automatic push sorts the queue into chains. A chain is a commit with the fixups that fold into it. Each chain has one state:

- **Ripe.** The newest change of the chain is older than the delay. memcore takes the newer of two dates as the newest change. The first is the newest author date of the commits of the chain. The second is the date that a fold recorded for the chain. So a fresh correction makes its chain wait again, also after a fold.
- **Waiting.** The chain gets ripe at a known time.
- **Held.** The automatic push never sends the chain.

A chain is held for one of these reasons:

- It changes a file outside memories, notes, plans and indexes. Only you push such a chain.
- It leaves a file with another mode than `100644`, such as an executable file or a symlink. Only you push such a chain.
- Its commits cancel each other. Your next push drops it.
- Its first commit is a fixup with no commit to fold into. Only you push such a subject.
- A document, a path or a commit message of it holds a likely secret.

A chain can also take the state of an earlier chain that is not ripe. That happens when both chains change one document. The index of a level never counts, because memcore generates it. The index of a topic folder counts when a chain adds it, removes it or changes its frontmatter. So a held chain blocks a later ripe chain only when both change one document or one topic folder.

With a merge commit in the queue, the automatic push pushes nothing. A rebase turns a merge into plain commits, so only your push sends a merge. With nothing ripe, the automatic push fetches nothing and changes nothing. The one exception is a record of an earlier automatic push. Its fetch ends that push, or the automatic push sends its built commits again.

With a ripe chain, the automatic push does these steps under the write lock:

1. It fetches, as `inbox` does, and the fetch rebases the queue.
2. It sorts the chains again, because the fetch can change the queue.
3. It builds the commits in a scratch clone, on the accepted commit. Each ripe chain becomes one commit, with its fixups folded in and its indexes written again. The commit keeps the message, the author and the author date of its target. A chain whose change the accepted history holds already gets no commit.
4. It ends the message of each built commit with the trailer `Pushed: auto`. No commit of the queue has that trailer.
5. It pushes the built commits, and it never forces.
6. It removes the pushed chains from the queue with one rebase. That rebase drops each pushed commit by its ID. It keeps every other commit as it is, on the new accepted commit.

Until the remote accepts the push, the queue is as the fetch left it. So a push that fails needs no repair, and memcore never resets `main`. The removal amends no accepted commit. When a kept commit leaves no change, memcore commits a changed index as a new commit `index: <path>`.

Before the push, memcore records the built tip and the pushed commits in the local state. The record stays in two cases:

- The removal cannot run, for example over a hand edit in the working tree. The next fetch removes the pushed chains.
- The push ends with an error that is no plain no of the remote, such as a connection that breaks. The remote may have accepted it, or may accept it later, because a push can end on the remote after its client died.

When the remote says no to the push, it never gets the built tip, and memcore removes the record at once. A hook that declines the push is such a no. The record stays after a no only when an earlier push of the same commits is still open. If the remote never takes them, remove the file that the error names. After any other error, the record stays, and each fetch looks at the remote:

- When the remote has the built tip, the fetch removes the pushed chains.
- When the remote lacks the built tip and can still get it, the record stays, and so does the queue. The remote can still get the built tip while that tip and the queue both stand on the accepted commit.
- When the remote moved on to other commits, it rejects the built tip. The fetch then removes only the record.

While the record stays, only the remote can end that push. So the next automatic push sends the same built tip again, and builds nothing new. `memcore push --auto` sends it at once. With the setting off, memcore starts no push in the background. `memcore push --auto` then still sends a recorded built tip, because the remote may hold it already.

memcore never removes such a record itself. The one exception is `recover`: the built tip then stands on commits that the remote lost, and must not land. The recorded commits stay in the queue, and `recover` lists them before you confirm. A push that lands late would then meet a changed queue, and a forgotten memory could come back. So your push refuses while the record stays after its fetch, because its fold gives the recorded commits new IDs. Run `memcore push --auto` first.

A correction that you make while the record stays is not lost. `revise` and `forget` then count a recorded commit as pushed, so the correction is a commit of its own, and no fixup. The removal drops only the recorded commits, and the correction stays in the queue. A fixup that you commit by hand in that time loses its target. The automatic push holds such a fixup, so only your push sends its subject.

The git process of the push holds a lock for as long as it runs. While a record stays and that lock is held, a fetch refuses. `drop` refuses while a record stays. So nothing rewrites the queue before memcore knows what the remote has. When `drop` still refuses after `memcore inbox`, run `memcore push --auto`.

The queue can lack a commit of the record, because somebody rewrote the queue by hand. Then memcore changes nothing, and tells you to remove the pushed commits by hand. A removal that ended before its record went is no such case. memcore then removes only the record.

With the setting on, `inbox` shows the state of each commit that the fold leaves in the queue. It also lists the commits that memcore pushed automatically since your last push. The automatic push holds the write lock for its whole run. So another write of memcore waits for it, and gives up after ten seconds.

## Only you

An agent runs as you. It can fake a terminal and answer a prompt. So nothing on your machine can stop an agent from calling `push`.

Two guards stop an agent's mistakes, and you do not rely on them. memcore refuses `drop` and `recover` when it finds no controlling terminal or sees the variables that agents set. `init` denies `drop`, `recover` and `ui` in the permissions of Claude Code, but not `push`. Where the host can forbid force pushes, keep that protection on.

The window of `memcore ui` is yours too. memcore refuses `ui` when it sees the variables that agents set. In the window, you confirm a push or a drop in a dialog instead of at the terminal, so a push from the window can push skills and scripts. The token of the server guards against an agent's mistake only: an agent that runs as you can read it.

An agent pushes when you ask it to. memcore cannot see the chat, so `orient` tells an agent to push only then. Skills and scripts run from the accepted history, so their push stays yours. `push` uses the credential that git already has, for you and for an agent alike.

Without a terminal, or with the variables that agents set, `push` asks nothing. It checks the queue that the fold leaves. It pushes the whole queue only when every commit changes only memories, notes, plans and indexes. Otherwise it pushes nothing. It names each commit that you push at your terminal: a merge, or a commit with any other file.

The automatic push sends only memories, notes, plans and indexes, by the same check of each commit. So every other file still reaches the remote only through your push. The setting `memcore.autopush` is no guard: an agent that runs as you can set it, as it can call `push`.

## Fetch

`inbox` and `push` fetch at once, under the write lock:
- memcore moves the accepted checkout forward, rebases the review queue onto the fetched tip, and links any new scripts. A conflict in an index writes the index again, by the rule of the fold for a topic index. A fetched memory needs no review, because a push accepted it.
- When the queue and the fetched history change the same document, the fetch stops and names the paths. The queue stays as it was.
- When the record of an automatic push stays, the fetch ends that push before it rebases. See [Automatic push](#automatic-push). While that push still runs, the fetch refuses.
- A fetched tip can lack the accepted history as it stood before. Then memcore keeps `origin/main`, rebases nothing, and refuses every write. `memcore recover` asks at your terminal, drops the lost commits here, and moves the queue onto the remote.
