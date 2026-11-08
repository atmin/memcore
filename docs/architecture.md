# Architecture

memcore is one Go executable. It runs the installed git, so it ships no git of its own.

## Parts

1. **Commands.** The `memcore` command line. Its output, such as the output of `orient`, is plain text that an agent reads.
2. **Store.** The memory repo, its indexes and the transcripts branch. memcore changes the store only through the installed git.
3. **Readers.** One adapter, for the JSONL of Claude Code. It converts that format into one trimmed conversation format. The readers also read the auto-memory of Claude Code, and find the directory behind each of its project folders.
4. **Guards.** Redaction, `verify`, the write lock, the confirmation behind `push`, and the check of the files that an agent pushes.
5. **UI.** The server and the page of `memcore ui`. It imports nothing of the commands: they give it the data, and run its actions. The page embeds three vendored files, marked, DOMPurify and pele, pinned by SHA-256.

Redaction stays local, with patterns and entropy.

## Git

memcore runs the installed git. It needs rebase with autosquash, git plumbing, and your credential helpers and SSH keys. So a git library is not enough. Everyone who runs coding agents already has git.

## Concurrency

Several sessions and worktrees can call memcore at the same time. Reads take no lock. Each write takes one short lock on the memory repo. `inbox` and `push` fetch at once, and rebase the review queue onto the fetched tip under the write lock. The queue of the window fetches the same way, so it takes the write lock too.

## Local state

- Local state lives under `.git/memcore/`, and memcore never commits it. It holds:
  - the write lock;
  - the use log, `log.jsonl`, with one line per call of memcore and no standard input;
  - the checkout of the accepted history, a worktree of the remote-tracking branch;
  - the marker of a remote that lost accepted commits;
  - the state of snapshots, `snapshot.json`, with the time of the last one, and the private index of the transcripts branch;
  - the record of the import of auto-memory, `import.json`, with the outcome of each file;
  - the record of what `init` added to the settings of Claude Code, `init.json`, and a copy of each original file;
  - the log of background work, `background.log`;
  - the state of the server of the window, `ui.json`, with its port and its token at mode `0600`, and its lock, `ui.lock`.
- The tool hooks of Claude Code run after every call of `Bash`, `Read` and `Grep`. So a tool call that reads no document logs no line, and starts no git process.
- memcore keeps each commit that the use log names under `refs/memcore/seen/`. So no fold and no garbage collection loses a version that a session saw. memcore never pushes these refs.

## Release

- A push of a `v*` tag runs the full checks on GitHub Actions. Then it builds memcore for darwin and linux, each on arm64 and amd64, with `CGO_ENABLED=0`.
- The workflow makes a GitHub Release of the tag with the four executables, `memcore-<system>-<processor>`, and `SHA256SUMS`. A tag with a `-` makes a prerelease. The install script skips it, unless `MEMCORE_RELEASE_URL` names the download folder of that tag.
- The build sets the version to the tag, with `-ldflags -X`. So `memcore version` names the release.
- The site `memco.re` is the folder `site/` with the icon of the window. A push to `main` that changes it deploys it to GitHub Pages. The site serves the install script at `/install`.
