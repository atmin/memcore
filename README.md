# memcore

*A palimpsest for agents.*

memcore is long-term memory for the agents you work with, kept as plain markdown in git. The working tree holds what is true now, and history holds earlier beliefs. The `memcore` command does the git work for the agents. It runs the installed git, so you install nothing else.

Agents forget between sessions, so you repeat the same instructions again and again. Your attention is the scarcest resource in this work. memcore keeps what you teach your agents, so you never tell an agent the same thing twice.

**Status:** the store, the write-read loop and the correction loop exist. `init` sets up the memory repo and Claude Code. It snapshots the transcripts, imports auto-memory, and adds the hooks and the permissions. `orient`, `show`, `help`, `remember`, `revise`, `forget`, `draft`, `verify`, `snapshot`, `inbox`, `stats`, `push`, `drop` and `recover` work on it. `push` asks at your terminal, and an agent's push sends memories, notes and plans without a question.

These docs describe what memcore does today. A trial exists outside this repo: one person's agent memories, imported and consolidated by hand.

## Principles

- **Agents act, and you verify.** An agent writes a memory. memcore commits it to the memory repo without asking you. You review the commits in bulk. An agent pushes memories, notes and plans when you ask, and only you push skills and scripts.
- **Lean context, progressive disclosure.** An agent sees the least it needs. It reaches anything else in a few small hops, as an index keeps a large table one small read away.
- **Plain files, plain git.** memcore uses no database and no vector store. You can read and edit everything by hand.
- **Local first.** Memory lives in a git repo on your machine, and it works without the remote. Local redaction runs before anything leaves. Memories go to your remote for storage.
- **Any agent.** A stock agent learns memcore from one instruction. The formats are open standards, so nothing ties memcore to one agent.

## Docs

These docs describe memcore. An agent that uses memcore loads none of them: it runs `memcore orient` and follows it. Each line below says when to read a doc, for you and for an agent that changes this repo.

- [Conventions](AGENTS.md): read before you change code or docs in this repo. It covers the work, the code, the tests, the verification and the commits.
- [Install](docs/install.md): read when you set up memcore on a machine. It covers how to build memcore, `memcore init` and what runs on its own.
- [Glossary](docs/glossary.md): read before you name anything. It gives each term in the docs one meaning.
- [Layout](docs/layout.md): read when you place or find a document. It covers levels, kinds, the index, placement and lookup.
- [A memory](docs/memory.md): read before you write a memory. It covers the frontmatter and the body.
- [Git](docs/git.md): read before you commit, review or push. It covers the review queue, fixups and the remote.
- [Interface](docs/interface.md): read when you use or build `memcore`. It covers the commands and how a stock agent learns them.
- [Architecture](docs/architecture.md): read when you build or change `memcore`. It covers its parts, git, concurrency and local state.
- [Transcripts](docs/transcripts.md): read when you work with transcripts. It covers what a snapshot keeps and how memcore redacts it.
- [Prior art](docs/prior-art.md): read when you compare memcore with other tools. It covers what exists and what memcore adds.

## Skills

memcore ships these skills, and this repo follows them itself. The voice skill applies to every reply and every document. The plan, build and review skills are for changes to code, in this repo and in yours.

- [Voice](skills/voice/SKILL.md): read before you write any reply or document. It covers how agents talk and write.
- [Plan](skills/plan/SKILL.md): read when you plan a change that needs more than one commit. It covers the rules and the template of a plan.
- [Build](skills/build/SKILL.md): read when you get an approved plan. It covers the worktree, the steps, the stops and the report.
- [Review](skills/review/SKILL.md): read when a plan or a commit needs a review. It covers the choice of the reviewer and the answers.
