# Prior art

Each part of memcore already exists somewhere:

| Part | Done by |
| --- | --- |
| Markdown in git: the files are the present, history the past | DiffMem<sup>[1](#sources)</sup>; Letta's context repositories<sup>[2](#sources)</sup> |
| Consolidation while idle | Letta's reflection subagent<sup>[2](#sources)</sup>; Claude Code's Auto Dream, so far known from third-party reports<sup>[3](#sources)</sup>; RemAgent<sup>[4](#sources)</sup> |
| Hierarchy | MemPalace<sup>[5](#sources)</sup>; Letta<sup>[2](#sources)</sup> |
| Onboarding from agent transcripts | Letta's `/init`<sup>[2](#sources)</sup> |
| One memory across agents and projects | RemAgent<sup>[4](#sources)</sup> |

memcore does four things that none of these tools do:
- **Agents act, and you verify.** Changes take effect at once, as they do in other tools. They also wait, unpushed, for your bulk review and your fixups. In other tools, nothing waits for review.
- **The hierarchy is yours, not one agent's.** It spans every project and every agent, and fixed rules guard promotion.
- **Corrections are the main input, and acceptance is the staleness test.**
- **Memory and working documents share one tree.** Plans, notes, conventions, skills and scripts follow the same placement rule as memories.

## Sources

1. DiffMem <https://jimmysong.io/ai/diffmem>
2. Letta, context repositories <https://www.letta.com/blog/context-repositories>
3. Claude Code Auto Dream (third-party) <https://claudefa.st/blog/guide/mechanics/auto-dream>
4. RemAgent <https://pypi.org/project/remagent/>
5. MemPalace <https://github.com/mempalace/mempalace>
