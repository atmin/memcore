# Sleep

Sleep keeps memory lean. A sleep pass starts on its own, in the background, when an agent calls `memcore` and enough has changed since the last pass (see [Install](install.md)). A pass:
- merges duplicates;
- splits a memory that mixes levels;
- moves memories up or down levels;
- drops superseded memories;
- moves state that looks like a fact (ticket status, "since…", work in progress) into notes or plans;
- turns a command sequence that you keep having agents run into a script, or into a skill when it needs a decision (see [Layout](layout.md));
- repoints links and paths that moved;
- keeps glossaries in step: it flags a synonym of a glossary term, and adds a term when one thing has two names;
- keeps every index in step.

**Promotion.** An agent loads its own level and the levels above it, never a sibling level. So promotion is the only way a fact can leak into another project, and these rules guard it:
- Placement is low by default. Too low costs one repeated instruction; too high costs every context.
- A fact moves up only when it appears in two or more sibling levels.
- What moves up is the lesson, with the project names removed. The specific fact stays where it was.
- A level whose index marks it as sealed never promotes anything.

**Staleness.** A pass finds stale memories in three ways, from the simplest to the cleverest:
1. Dead references: files, flags, commands, links or branches that no longer exist.
2. Contradictions along one path, which is the chain of levels that an agent loads.
3. Behaviour against memory: an agent acted against a memory. If you objected, the memory holds and gets stronger. If you accepted the act again and again, the memory is stale or too broad.

Age alone never decides. It only sets the order of the review.

**Budget.** Each level has a line budget, which counts what `orient` prints for it (see [Layout](layout.md)). When a level goes over it, the pass must merge, demote or move memories into notes.

When a pass cannot settle a contradiction, it lists the contradiction for you and does not decide it.
