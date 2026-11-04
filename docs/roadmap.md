# Roadmap

memcore grows in five milestones, from writing memory to scoring it. Each milestone ends in a state that you can use, and you can stop after any of them. Each milestone becomes one plan, built with the [build](../skills/build/SKILL.md) skill.

**Status:** M1 and M2 exist. M3 is next.

## Goal

memcore is the only long-term memory of your agents. `memcore init` builds the memory repo from scratch. Daily use needs no hand edits and no git commands. memcore measures whether memory helps, from git history and transcripts. Each sleep pass then improves memory a little.

## The milestones

| Milestone | You get | Agent calls |
| --- | --- | --- |
| M1 Write | Agents remember, revise and forget, and you review without git. | none |
| M2 Onboard | One `memcore init` sets up the memory repo and Claude Code from scratch. | none |
| M3 Measure | `memcore stats` shows use, misses, review precision and context cost per week. | none |
| M4 Sleep | A sleep pass turns your corrections into small proposals in the review queue. | headless agent |
| M5 Evals | Past corrections become test cases, and memcore scores any commit of the memory repo. | headless agent |

Between M2 and M3, memcore goes live: it becomes the only memory of its maintainer's agents. So M3 measures real use.

## How the milestones fit

- Data collection comes before analysis. The use log ships in M1 and snapshots ship in M2, so later milestones have history to measure.
- Deterministic work comes before thinking. M1 to M3 call no agent, so they stay cheap and testable.
- M3 comes before M4. M3 is cheap, and it shows the misses and the friction before sleep spends tokens.
- Agents act, and you verify. No milestone lets memcore push, and every change waits in the review queue.
- Exact data comes from the hooks of Claude Code, and conversation text comes from transcripts. Hook input is documented. The transcript format is internal to Claude Code, and it changes between versions.

## M1: Write

A fresh agent reads, writes, corrects and removes any document through memcore alone. You review the review queue without git. memcore logs its own use, so later milestones can measure it.

- `remember`, `revise` and `draft` take the document as memcore stores it. memcore reads it leniently, and writes the canonical form.
- `memcore help <command>` shows the format of each input. Every input error names its cause.
- `inbox` shows each queue commit with its reason and its files. Only you drop or push, at your terminal.
- Every command appends one line to the use log. memcore keeps each commit in the use log reachable, so a fold loses no version that a session saw.
- `MEMCORE_REPO` points memcore at a scratch memory repo, for tests, dry runs and evals.

See [Interface](interface.md) and [Git](git.md).

## M2: Onboard

With no memory repo and an empty remote, one `memcore init <remote>` sets up the memory repo and Claude Code. You can run it again at any time. You can undo its changes outside the memory repo.

- `init` snapshots every transcript of Claude Code onto the transcripts branch, trimmed and redacted.
- `init` imports the auto-memory of Claude Code, one memory per file, at the level of its project.
- When every file is in, or you accepted the rest at your terminal, `init` turns auto-memory off.
- `init` adds three hooks and some deny rules to the settings of Claude Code. It shows the diff and asks once.
- `--dry-run` rehearses `init`, and `--undo` removes what `init` added to the settings.

See [Install](install.md) and [Transcripts](transcripts.md).

## M3: Measure

`memcore stats` answers whether memory helps, from data that memcore already has. It calls no agent. It recomputes every number from the use log, git history and the transcripts branch.

`stats` shows these numbers for each week:
1. **Capture:** the memories remembered, revised and forgotten.
2. **Review precision:** the queue commits pushed and dropped. A revision of an accepted memory within 14 days counts as an accepted mistake.
3. **Use:** the reads per session, the most-read memories, and the memories that no session read in 30 days.
4. **Context cost:** the words and the estimated tokens that `orient` printed per session and per level, against the budget.
5. **Friction:** the failed calls of memcore, grouped by error.
6. **Your marks:** the counts of `#again`, `#wrong` and `#stale`.

`inbox` starts with a summary of the week in three lines.

## M4: Sleep

A sleep pass finds your corrections in new transcripts, and proposes a few small changes for review. Each proposal cites the corrections behind it. [Sleep](sleep.md) holds the design.

- A pass starts in the background when enough has changed since the last pass. It stops at a budget, reports its cost, and never pushes.
- Your agent, run headless, lists the corrections in each new session. A correction is your words, or your rejection with a reason.
- A judge compares each correction with the memory that its session saw, and names the miss.
- A pass commits at most ten changes, ranked by repeats. It never proposes a change that you dropped, unless new corrections support it.
- After your consent, `init` runs the first pass over every snapshot. That pass completes [onboarding](onboarding.md).

## M5: Evals

memcore scores any commit of the memory repo against your real past corrections. A proposal that fixes no case, or breaks a passing case, never reaches the review queue.

- The judge turns each correction about memory into a case. A case holds the directory, the situation, the expected behaviour and your quote.
- Cases stay on a local branch, which memcore never pushes.
- `memcore eval [--at <commit>]` runs each case in a fresh headless session, against a read-only worktree of that commit.
- Scores at older commits show the trend. `stats` shows the pass rate at each push.
- `stats` compares your marks with the verdicts of the judge, so you see how far to trust the judge.

## The improvement loop

After M5, each correction feeds one loop:
1. A snapshot keeps the session, and a sleep pass extracts the correction.
2. The judge names the miss, and the correction becomes a case.
3. The pass proposes the smallest change that passes the case and breaks no other case.
4. You keep or drop the change at review, and a drop teaches the next pass.
5. `stats` shows whether your repeats fall while the context cost stays flat.

The same loop improves memcore itself. The friction in `stats` and the drops at review become plans for this repo.

## Not in M1 to M5

- The gitmote remote and the key bound to your presence.
- `purge`. Its design must also cover the use log and the commits that memcore keeps reachable.
- A local model for redaction. See [Open questions](open-questions.md).
- `search`, `recall` and `history`, until the use log shows that agents need them.
- Codex and other agents, until Claude Code works end to end.
