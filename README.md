# memcore

*A palimpsest for agents.*

memcore is long-term memory for the agents you work with, kept as plain markdown in git. The working tree holds what is true now, and history holds earlier beliefs. The `memcore` command does the git work for the agents. It runs the installed git and the agent that you already use, so you install nothing else.

Agents forget between sessions, so you repeat the same instructions again and again. Your attention is the scarcest resource in this work. memcore keeps what you teach your agents, so you never tell an agent the same thing twice.

**Status:** design only, with no code yet. A trial exists outside this repo: one person's agent memories, imported and consolidated by hand.

## Principles

- **Agents act, and you verify.** An agent writes a memory, and memcore commits it to the memory repo without asking you. You review the commits in bulk, and only you push.
- **Lean context, progressive disclosure.** An agent sees the least it needs. It reaches anything else in a few small hops, the way an index keeps a large table one small read away.
- **Plain files, plain git.** memcore uses no database and no vector store. You can read and edit everything by hand.
- **Local first.** Memory lives in a git repo on your machine, and it works without the remote. Local redaction runs before anything leaves. Memories go to your remote for storage. Text goes to the agent that you already use for thinking, and nowhere else.
- **Any agent.** A stock agent learns memcore from one instruction. The formats are open standards, so nothing ties memcore to one agent.

## Docs

These docs are the design of memcore. An agent that uses memcore loads none of them: it runs `memcore orient` and follows it. Each line below says when to read a doc, for you and for an agent that changes this repo.

- [Conventions](AGENTS.md): read before you change code or docs in this repo. It covers the work, the code, the tests, the verification and the commits.
- [Install](docs/install.md): read when you set up memcore on a machine. It covers the install script, `memcore init` and what runs on its own.
- [Glossary](docs/glossary.md): read before you name anything. It gives each term in the docs one meaning.
- [Layout](docs/layout.md): read when you place or find a document. It covers levels, kinds, the index, placement and lookup.
- [A memory](docs/memory.md): read before you write a memory. It covers the frontmatter and the body.
- [Git](docs/git.md): read before you commit, review or push. It covers the review queue, fixups and the remote.
- [Interface](docs/interface.md): read when you use or build `memcore`. It covers the commands and how a stock agent learns them.
- [Architecture](docs/architecture.md): read when you build or change `memcore`. It covers its parts, git, the thinking, concurrency and local state.
- [Sleep](docs/sleep.md): read when you consolidate memory. It covers a sleep pass, promotion, staleness and budgets.
- [Transcripts](docs/transcripts.md): read when you work with transcripts. It covers what a snapshot keeps and how memcore redacts it.
- [Onboarding](docs/onboarding.md): read when you start from an empty memory repo. It covers the sources and how the structure emerges.
- [Prior art](docs/prior-art.md): read when you compare memcore with other tools. It covers what exists and what memcore adds.
- [Open questions](docs/open-questions.md): read when you make a decision. It lists what is still open.

## Skills

memcore ships these skills, and this repo follows them itself. The voice skill applies to every reply and every document. The plan, build and review skills are for changes to code, in this repo and in yours.

- [Voice](skills/voice/SKILL.md): read before you write any reply or document. It covers how agents talk and write.
- [Plan](skills/plan/SKILL.md): read when you plan a change that needs more than one commit. It covers the rules and the template of a plan.
- [Build](skills/build/SKILL.md): read when you get an approved plan. It covers the worktree, the steps, the stops and the report.
- [Review](skills/review/SKILL.md): read when a plan or a commit needs a review. It covers the choice of the reviewer and the answers.
