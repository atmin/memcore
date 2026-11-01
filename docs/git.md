# Git

A memory repo always has a remote, and any git remote will do. The pushed commits are the accepted history. The commits after it are the review queue, and when you push them, you accept them.

The intended remote is a future gitmote. It is serverless, lives in object storage and is end-to-end encrypted, so neither the host nor the bucket can read the memory.

- Agents commit through `memcore` as they go, one change per commit, without asking.
- You review the queue in bulk, at a time you choose.
- A correction becomes a fixup commit, which memcore autosquashes into the commit it corrects.
- Until you push, memcore can rewrite history for clarity: it can reorder, merge, reword or drop commits.
- Nobody rewrites pushed history, and nobody pushes with `--force`, except you, in a purge.
- Only you push, and only you purge.

Agents load unreviewed memories at once, and `orient` labels them as unreviewed. A bad memory lasts until your next review, and the review leaves a clean history.

Hostile text, such as a web page, an issue or an email, can make an agent plant a memory or a skill. Later agents then follow that memory or skill. Skills and scripts are code, so they run only after you push them. memcore keeps a checkout of the accepted history under its local state, and moves it forward at every push and fetch. It serves skills and links scripts from that checkout. So a revision in the review queue changes nothing that runs, and a helper beside a script is the accepted helper.

## Only you

An agent runs as you, so nothing on your machine can stop it from calling `push` or `purge`. It can fake a terminal and answer a prompt. So the guard is something the agent does not have: the credential that the remote needs for a write. `init` binds that credential to your presence, or to a passphrase that only you know, with the strongest guard available:
1. The strongest is a security key, such as a YubiKey. OpenSSH keeps the key on it, and every signature needs a touch, with a PIN if you choose. The OpenSSH that macOS ships lacks this support, and the one from Homebrew has it.
2. The next is Touch ID, through an SSH agent that keeps the key in the Secure Enclave, such as Secretive<sup>[1](#sources)</sup>. The agent asks for your touch at every signature, and the system dialog falls back to your login password when Touch ID is unavailable.
3. The floor is a passphrase key. memcore runs ssh with that key alone, without an agent or a cache, so you type the passphrase at every push.

`push` and `purge` sign with that key, so they need you at the keyboard, whoever starts them. You push in bulk at review time, so you type or touch once per review. A background fetch cannot wait for you, so the memory repo also has a read-only credential, which memcore may cache. `inbox` shows which guard protects the remote.

Two more guards stop an agent's mistakes earlier, and you do not rely on them. memcore refuses `push` and `purge` when it finds no controlling terminal or sees the variables that agents set. `init` denies both commands in the agent's permissions where it can. Where the host can forbid force pushes, keep that protection on. Lift it for a purge in the host's own interface.

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
- When an agent calls memcore and five minutes have passed since the last fetch, memcore fetches. `inbox` and `push` fetch at once.
- The fetch lands under the write lock. memcore moves the accepted checkout forward, rebases the review queue onto the fetched tip, and links any new scripts. A fetched memory needs no review, because a push accepted it.
- When the queue and the fetched history change the same document, the fetched version stays in place. The local version becomes a revision on top of it in the queue, and `inbox` lists the pair as a contradiction.
- When the fetched tip does not contain the accepted history as it stood before the fetch, you purged on another machine. The review queue sits on top of that history, so its absence from the remote is normal. memcore then stops writes, and asks you to run the same purge here before it rebases the queue.
- memcore retries a failed fetch in silence. `inbox` shows the time of the last successful fetch.

## Sources

1. Secretive <https://github.com/maxgoedjen/secretive>
