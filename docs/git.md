# Git

A memory repo always has a remote, and any git remote will do. The pushed commits are the accepted history. The commits after it are the review queue, and when you push them, you accept them.

The intended remote is a future gitmote. It is serverless, lives in object storage and is end-to-end encrypted, so neither the host nor the bucket can read the memory.

- Agents commit through `memcore` as they go, one change per commit, without asking.
- You review the queue in bulk, at a time you choose.
- A correction of an unpushed change becomes a fixup commit. `push` folds each fixup into the commit it corrects.
- Until you push, memcore can rewrite history for clarity: it can reorder, merge, reword or drop commits.
- Nobody rewrites pushed history, and nobody pushes with `--force`, except you, in a purge.
- An agent pushes memories, notes and plans when you ask. Only you push the other documents, and only you purge.
- You drop a commit of the queue with `memcore drop`, at your terminal, and the drop keeps your reason.

Each commit of memcore names its command and its path, as `remember: dev/memories/tests-before-code.md`. The commands that commit are `remember`, `revise`, `forget` and `draft`. `push` adds `index: <path>` for an index that a hand edit left stale. The body of a `revise` or a `forget` holds its reason. A fixup is `fixup! <subject of its target>`, with its command, its path and its reason in the body. The first commit of a new memory repo is `init: create the memory repo`. `init` commits each import of auto-memory as `init: import the auto-memory of <dir>`. A snapshot commits to the transcripts branch as `snapshot: <n> transcripts`, never to `main`.

**Fold.** `push` folds the queue with one interactive rebase, and never rewrites the accepted history. A fixup folds into the nearest earlier commit with its subject. A chain that leaves every document as it found it, such as a remember and its forget, goes whole. Indexes are generated, so the fold writes each index again after each commit. A conflict in any other document stops the fold and leaves the queue as it was.

`inbox` runs the same fold on a scratch clone of `main`, to show what `push` would leave. It shows an index that `push` would commit as `new`. The header of `inbox` counts the index commits that `push` adds apart from the commits that the fold leaves. Only `push` folds the memory repo. The clone runs no hook, file system monitor or signing helper. When the fold in the clone stops, `inbox` warns and lists each commit of the queue as it is.

Agents load unreviewed memories at once, and `orient` labels them as unreviewed. A bad memory lasts until your next review, and the review leaves a clean history.

Hostile text, such as a web page, an issue or an email, can make an agent plant a memory or a skill. Later agents then follow that memory or skill. Skills and scripts are code, so they run only after you push them. memcore keeps a checkout of the accepted history under its local state, and moves it forward at every push and fetch. It serves skills and links scripts from that checkout. So a revision in the review queue changes nothing that runs, and a helper beside a script is the accepted helper.

## Only you

An agent runs as you, so nothing on your machine can stop it from calling `push` or `purge`. It can fake a terminal and answer a prompt. So the planned guard is something the agent does not have: the credential that the remote needs for a write. Later work: `init` binds that credential to your presence, or to a passphrase that only you know, with the strongest guard available:
1. The strongest is a security key, such as a YubiKey. OpenSSH keeps the key on it, and every signature needs a touch, with a PIN if you choose. The OpenSSH that macOS ships lacks this support, and the one from Homebrew has it.
2. The next is Touch ID, through an SSH agent that keeps the key in the Secure Enclave, such as Secretive<sup>[1](#sources)</sup>. The agent asks for your touch at every signature, and the system dialog falls back to your login password when Touch ID is unavailable.
3. The floor is a passphrase key. memcore runs ssh with that key alone, without an agent or a cache, so you type the passphrase at every push.

With that key, `push` and `purge` would need you at the keyboard, whoever starts them. You would push in bulk at review time, so you type or touch once per review. A background fetch cannot wait for you, so the memory repo would also have a read-only credential, which memcore may cache. `inbox` would show which guard protects the remote.

Two more guards stop an agent's mistakes earlier, and you do not rely on them. memcore refuses `purge`, `drop` and `recover` when it finds no controlling terminal or sees the variables that agents set. `init` denies `drop` and `recover` in the permissions of Claude Code, but not `push`. Where the host can forbid force pushes, keep that protection on. Lift it for a purge in the host's own interface.

An agent pushes when you ask it to. memcore cannot see the chat, so `orient` tells an agent to push only then. Skills and scripts run from the accepted history, so their push stays yours. Today `push` uses the credential that git already has, for you and for an agent alike. A key bound to your presence would stop an agent's push too, so its design is open: see [Open questions](open-questions.md).

Without a terminal, or with the variables that agents set, `push` asks nothing. It checks the queue that the fold leaves. It pushes the whole queue only when every commit changes only memories, notes, plans and indexes. Otherwise it pushes nothing. It names each commit that you push at your terminal: a merge, or a commit with any other file.

## Purge

A purge removes content from every commit, for a lawful erasure request, a leaked secret or a court order. Before a push there is no purge: you drop the commit in the review. A purge is for pushed history, and only you run it.

- `purge <path>` removes a document, or a whole level, from every commit. `purge --text` reads a passage from standard input, never from an argument, and replaces it in every commit with `[purged]`.
- A purge shows its footprint before it asks: every document, transcript, commit message and index line that holds the content. It rewrites all of them, in the memory repo and on the transcripts branch, and it expires the reflogs and prunes. You find what sleep reworded with `search` and `recall`, and you purge it by path.
- A purge takes the write lock for its whole run, because every commit changes. It fetches first and rewrites what it fetched too, so no other machine's push is lost.
- Then it asks for confirmation at your terminal and pushes with a lease. That is the only force push in memcore. The remote must then drop its unreachable objects: gitmote does so when you ask, and for another host you ask the host.
- A purge never sends what it removes anywhere, so you run it once per machine. memcore on another machine finds the rewrite at its next fetch and asks you for it.
- A purge leaves one commit that says a purge happened, with the reason you give and nothing of the content. `verify` then flags the references it broke, and sleep repoints them.

A purge does not make a leaked secret safe: rotate it first.

A purge cannot reach four places, which are the host's job or yours:
- backups and versioned buckets at the host;
- clones that memcore does not manage;
- the agent's own transcript files;
- the provider's servers.

## Fetch

memcore fetches the way snapshots and sleep passes start, in the background, so no call waits:
- When an agent calls memcore and five minutes have passed since the last fetch, memcore will fetch. Today only `inbox` and `push` fetch, and they fetch at once.
- The fetch lands under the write lock. memcore moves the accepted checkout forward, rebases the review queue onto the fetched tip, and links any new scripts. A conflict in an index writes the index again. A fetched memory needs no review, because a push accepted it.
- When the queue and the fetched history change the same document, the fetch stops and names the paths. The queue stays as it was. Later, the fetched version will stay in place, and the local version will become a revision on top of it. `inbox` will list the pair as a contradiction.
- After a purge on another machine, a fetched tip lacks the accepted history as it stood before. memcore keeps `origin/main`, rebases nothing, and refuses every write. `memcore recover` asks at your terminal, drops the lost commits here, and moves the queue onto the remote.
- memcore retries a failed fetch in silence. `inbox` shows the time of the last successful fetch.

## Sources

1. Secretive <https://github.com/maxgoedjen/secretive>
