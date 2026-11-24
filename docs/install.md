# Install

Install memcore on macOS or Linux with Homebrew or with the shell installer. Choose one method.

**Homebrew**

```
brew install atmin/tap/memcore
```

**Shell installer**

```
curl -fsSL https://memco.re/install | sh
```

After either method, check which executable your shell will run.

```
command -v memcore
```

For Homebrew, the expected path is `bin/memcore` under the directory printed by `brew --prefix`.
For the shell installer, the expected path is `~/.local/bin/memcore`.
If the path differs, fix your shell's `PATH` before running `init`.

Then set up the machine. This step is the same for both methods.

```
memcore init git@github.com:you/memory.git
```

## The executable

### Homebrew

The formula pins the URL and the SHA-256 of the executable for your system and processor, and Homebrew checks the SHA-256 on download. On Linux, it installs the glibc build. That build includes native-window support, but the window still needs WebKitGTK on the system. To update, run `brew upgrade atmin/tap/memcore`.

The desktop application is optional with Homebrew. To add it, run `memcore ui --install` after `brew install` and after each `brew upgrade`. Then the application uses the upgraded executable.

On macOS, `memcore.app` holds a copy of the executable, so the run replaces the copy. On Linux, the launcher records the resolved path of the executable, which names a version in the Homebrew Cellar. After an upgrade, `brew` may already have removed the old path, and the run records the path of the new version, which repairs the launcher.

### Shell installer

The install script downloads the executable of the latest release for your system and processor into `~/.local/bin`. It supports macOS and Linux, each on arm64 and amd64. On Linux, it installs the glibc build when the system has the glibc loader, and the static build otherwise. It checks the SHA-256 of the download against `SHA256SUMS` of the release, and refuses a download that differs. It needs no `sudo`. Read it before you run it, with `curl -fsSL https://memco.re/install`. To update, run `curl -fsSL https://memco.re/install | sh` again.

Then the install script runs `memcore ui --install`, which adds the application of the window. When the application fails to install, the install script warns and still installs memcore. Then open the window with `memcore ui`.

### The desktop application

`memcore ui --install` adds the application of the window.

- On macOS, it writes `memcore.app` in `~/Applications`, and registers it, so Spotlight and the Finder open it.
- On Linux, it writes a desktop entry and an icon under `~/.local/share`, or under `XDG_DATA_HOME` when it is set.

On macOS, `memcore.app` holds its own copy of memcore. So the application is the window itself, and the Dock shows memcore with its icon. The copy does not change when the installed executable changes, so an update writes the application again. The install script does it on each update. `Info.plist` keeps the `PATH` of the install, because an application gets no shell profile. It sets `MEMCORE_REPO` only when the install had it set. The application writes its errors to `~/Library/Logs/memcore.log`.

On Linux, the desktop entry runs a launcher. The launcher runs the executable that `memcore ui --install` resolved, with the same environment. After a shell install, that is `~/.local/bin/memcore`, so an update of memcore updates the application too.

### From source

You can also build memcore from source with Go 1.27.1. Clone the memcore repo, and run this command in it:

```
CGO_ENABLED=0 go build -o ~/.local/bin/memcore ./cmd/memcore
```

To update a build from source, pull the repo and build again. Then run `memcore ui --install`, so memcore.app holds the new build. `memcore version` names the build that runs: the tag of a release, or `dev` for a build from source. It adds the commit of the build when the build records one.

### Before you run `init`

At run time, memcore needs only git. A reflect pass also needs Claude Code on `PATH`, logged in, because it runs your agent headless.

Homebrew does not use `~/.local/bin`, but `push` and a fetch link the accepted scripts onto `~/.local/bin`, with either method. Put `~/.local/bin` on `PATH` to run them.

## `memcore init <remote>`

Setup needs only the remote, because every memory repo has one. You can run `init` again safely. It:
- **Sets up the memory repo.** If the remote is empty, `init` creates the memory repo in `~/memory`. If not, it clones the remote. So the same command sets up your first machine and every later one. A new memory repo gets a `.gitignore` in its first commit. It names the files that an operating system or an editor leaves, such as `.DS_Store` and `*~`.
- **Snapshots the transcripts.** `init` first commits every transcript of Claude Code to the transcripts branch. So the transcripts are safe before anything else can fail. See [Transcripts](transcripts.md).
- **Imports auto-memory.** `init` imports each file of the auto-memory of Claude Code as a memory, at its project's level. It makes one commit per project. It skips `MEMORY.md`, the index of auto-memory, so a fact that only that index holds stays out. A file that fails a check stays out, and `inbox` lists it with its reason. When every file is in, or you accepted the rest at the terminal, `init` turns auto-memory off.
- **Connects your agents.** For Claude Code, `init` adds five hooks to `~/.claude/settings.json`, and denies some tools and commands. See [Interface](interface.md). Before it changes the settings, it shows the diff and asks once. It keeps a copy of the original under `.git/memcore/init/`.
- **Shows your inbox.** `init` ends with the report of `memcore inbox`, without a fetch.

The snapshot and the import need no agent. Each file of auto-memory becomes a memory at the level of its project, and keeps its session. `MEMORY.md` is only the index of those files, so `init` skips it, with any fact that it holds alone. A fact that two projects hold stays twice, once at each project.

A memory repo from an older `init` has no `.gitignore`, and `init` adds none to a clone. To add one by hand, copy the `.gitignore` of a memory repo that a newer `init` created into the root of your memory repo. Then commit it, and push.

A second run adds only what is new: new transcripts, new files of auto-memory, and each missing hook. Two flags help you try `init` and leave it:
- `memcore init --dry-run <remote>` builds the memory repo at `MEMCORE_REPO`, and prints the change of the settings without making it. It asks nothing, so an agent may run it too.
- `memcore init --undo` removes what `init` added to the settings, and keeps every later change. It names an addition that you changed since, and leaves it.

## Day to day

Agents run `memcore` themselves. You run `memcore inbox` to see what waits for your review, and `memcore push` to accept it. Or you run `memcore ui`, which opens a window to review, push, browse and search. You run `memcore reflect` when you want to know where memory failed you: a reflect pass never starts by itself. `push` and a fetch link the accepted scripts onto `~/.local/bin`.

Nothing of memcore runs between sessions: there is no service and no scheduled job. The one exception is the server of `memcore ui`. It runs in the background while a window is open, apart from any session, and stops 10 s after the last window closes. Snapshots start the way `git gc --auto` does:
1. An agent calls `memcore`.
2. If six hours have passed since the last snapshot, `memcore` starts a snapshot in the background. The snapshot reads the sessions of Claude Code and of Codex.
3. `memcore` answers the agent at once, without waiting for the snapshot.

A snapshot also starts at the end of each session of Claude Code, through its `SessionEnd` hook.

The automatic push starts the same way, when you turned it on. A call of `memcore` starts `memcore push --auto` in the background, at most once an hour. See [Automatic push](git.md#automatic-push).

## Uninstall

Both methods keep the memory repo, with your memories and the transcripts. They also keep the links of accepted scripts in `~/.local/bin`, which point into the memory repo. Remove the memory repo yourself when you no longer want it.

### Uninstall a Homebrew install

Run this chain. Each command runs only after the one before it succeeds.

```
memcore init --undo && \
  memcore ui --uninstall && \
  brew uninstall atmin/tap/memcore
```

1. `memcore init --undo` shows the diff of the settings of Claude Code and asks. Without a memory repo, `init` changed nothing to undo, so it asks nothing, unless a hook still runs memcore.
2. `memcore ui --uninstall` removes the application of the window.
3. `brew uninstall atmin/tap/memcore` removes the executable.

The chain stops before step 3 when you answer no, or when a step fails. Without memcore, a hook in the settings would fail in every session. Fix the cause, then run the chain again.

### Uninstall a shell install

Run this command.

```
curl -fsSL https://memco.re/uninstall | sh
```

The uninstall script runs `~/.local/bin/memcore`, and never another `memcore` on `PATH`. So it fits only a shell install. It does three steps.
1. It runs `memcore init --undo`, which shows the diff of the settings of Claude Code and asks. Without a memory repo, `init` changed nothing to undo, so it asks nothing, unless a hook still runs memcore.
2. It runs `memcore ui --uninstall`, which removes the application of the window.
3. It removes `~/.local/bin/memcore`.

It stops before step 3, with memcore kept, when you answer no, or when a hook in the settings still runs memcore. Without memcore, such a hook would fail in every session. Remove the hook by hand, then run the uninstall again.

The uninstall script names the memory repo folder when it finishes.

### Switch from the shell installer to Homebrew

1. Run the shell uninstall until it succeeds and removes `~/.local/bin/memcore`.
2. Run `brew install atmin/tap/memcore`.
3. Open a new shell, or run `hash -r` in Bash, to clear the cached path. Run `command -v memcore` and check that it points to `bin/memcore` under the directory printed by `brew --prefix`.
4. Run `memcore init` again with the same remote.

Both methods keep the memory repo, so step 4 reuses it. Run `memcore ui --install` after step 4 when you want the desktop application.
