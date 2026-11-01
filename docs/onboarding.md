# Onboarding

Onboarding is the first sleep pass, over an empty memory repo. Later passes then run on whatever is new.

The sources, from the most valuable to the least:
- **Agent transcripts** from Claude Code, Codex, Cursor, Aider and chat exports. Your corrections are their richest part.
- **Instruction files**: `CLAUDE.md`, `AGENTS.md`, `.cursorrules` and any existing agent memory. These files are already distilled.
- **Your home directory and git.** The directory layout and the remotes give the levels. The commit history gives your conventions and shows which repos are still in use.
- **Review comments on pull requests.**
- **Shell history.** Commands that you keep typing become candidates for `bin/`.
- **Notes that you already keep.**

The structure emerges in three steps:
1. **Levels.** The directories where sessions happened become levels. A directory with no sessions gets no level.
2. **Candidates.** Your agent, run headless, reads the redacted conversation text of each session. It extracts candidate facts and tags each one with the directory and the date.
3. **Placement.** Sleep places each candidate by co-occurrence, with the same rule as [promotion](sleep.md).

The first review starts with the ten corrections that you repeated most, each with the quote behind it. The rest waits in the review queue.
