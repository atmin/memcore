# Glossary

The docs use each term below with one meaning only.

- **memcore**: the system, and its one command. The docs use the name for both.
- **agent**: an AI coding agent that works with you, such as Claude Code or Codex.
- **you**: the person whose memory it is. Skills, which address the agent, call you "the person".
- **memory repo**: the git repo that memcore manages, in `~/memory` by default.
- **level**: a folder in the memory repo that mirrors a directory, such as the root, `work/` or `work/shop/`. A folder whose name would collide with a kind gets a `+` in front, as `+bin/` for `~/bin`.
- **path**: the chain of levels from one level up to the root. An agent loads its path.
- **document**: any file that memcore stores at a level. It is a memory, a plan, a note, a skill, a script, a conventions file or a glossary.
- **kind**: the type of a document. The name of its folder or file gives the kind.
- **memory**: a document that holds one current fact.
- **script**: an executable file in a `bin/` folder. `push` links it onto your `PATH`.
- **index**: the `README.md` of a level or of a topic folder. It has one line per document, topic folder or child level.
- **topic folder**: a folder inside the `memories/` or `notes/` folder of a level. It groups the documents of one topic, and its index has a frontmatter that says when to read it.
- **session**: one run of an agent, from its start to its exit.
- **transcript**: the record of one session.
- **snapshot**: a commit of new transcripts to the transcripts branch.
- **hook**: a command that an agent runs at a point of a session, such as its start. `init` adds the hooks of memcore to Claude Code.
- **mark**: a word that you add to a prompt, `#again`, `#wrong` or `#stale`, to say that memory failed you.
- **accepted history**: the pushed commits of the memory repo, as the remote-tracking branch holds them after the last fetch.
- **review queue**: the commits of the memory repo after the accepted history, which are not yet pushed.
- **release**: a version of memcore that a `v*` tag names, with one executable for each system and processor on GitHub.
- **window**: the local page of `memcore ui`, in which you review, push, browse and search.
- **accepted mistake**: a memory that a `revise` or a `forget` changed within 14 days after a push accepted it.
- **fixup**: a commit that corrects an unpushed commit, and that `push` folds into it.
- **fold**: the step of `push` that folds each fixup into the commit it corrects.
- **use log**: the local record of the calls of memcore, under `.git/memcore/`, which memcore never commits.
- **read**: a document that a session reads, through a successful `show` or a tool call that a hook logs.
