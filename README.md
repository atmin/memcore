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

- [Glossary](docs/glossary.md): read before you name anything. It gives each term in the docs one meaning.

## Skills

memcore ships these skills, and this repo follows them itself.

- [Voice](skills/voice/SKILL.md): read before you write any reply or document. It covers how agents talk and write.
