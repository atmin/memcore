---
name: learn
description: Use after a sleep pass, when corrections are open. It turns each open correction into a change of memory, or into a reason for none.
---

# Learn

A sleep pass finds the corrections of the person, and judges each one against memory. A miss is a correction that memory should have prevented. A miss is open until it has an answer. This skill gives each open correction its answer, in a session with the person.

Make every change through a memcore command, so it lands in the review queue. Never edit a file of the memory repo by hand.

## Survey

1. Run `memcore sleep --list`. It prints each open correction with its id, its verdict, the words of the person and the rule. `memcore sleep --list --all` also prints the answered corrections, each with its answer.
2. Group the corrections that state one rule at one level. One change answers a whole group. A memory answers only the sessions that see it, at its level or below it. So the same rule at a sibling level is a group of its own.
3. Order the groups by the count of their sessions. A rule that the person repeated in several sessions comes first.

Change nothing yet. Give the person the count of the open corrections and the groups in a few lines.

## Check

Memory changed since the session of a correction. So check each group against memory as it is now.

1. Run `memcore orient` in the directory of the session. The head line of a correction names its level, such as `~/work/shop`.
2. Read each memory that may hold the rule, with `memcore show <path>`.
3. Check the verdict. The judge errs, so read the words of the person against the memory that the verdict names. When the verdict is wrong, name the fault that you find, and decide by that fault.

A later change may have fixed the fault already. Then record the answer, and change nothing:
- For a capture miss, a memory on the path of the session now holds the rule.
- For a retrieval miss, the description of the memory changed since the session, and it now names the moment.
- For an application miss, the body of the memory changed since the session, and it now states the rule first.
- For a stale memory, the memory now agrees with the person, or it is gone.

A memory that held the rule then, and that nobody changed since, fixes no retrieval miss and no application miss.

## Decide

The verdict names the fault, and each fault has its change:

1. **A capture miss:** no memory held the rule. Write a memory with `memcore remember`, when the rule serves future sessions.
2. **A retrieval miss:** a memory held the rule, and the session did not read it. Revise its description, so that it names the moment of the correction.
3. **An application miss:** the session read the memory, and acted against it. Revise the body: state the rule first, then its reason.
4. **A stale memory:** the memory says otherwise. Revise it to the rule of the person, or forget it.

Write the rule in the words of the person where they are clear. Keep each change small: one rule, one document.

## No change

Some corrections need no change of memory. Each one still gets an answer, with the reason.

- A correction that concerns only its task gets no memory. It says nothing about a future session.
- A correction about an instruction of the agent, a skill or a script is the person's to change. Name it in the report.
- A correction with a wrong verdict gets no change only when you find no fault of memory at all. The reason then says why the verdict is wrong.

When you cannot tell whether a rule serves future sessions, ask the person.

## Placement

A new memory goes to the level of the session, and never higher. Placement is low by default: too low costs one repeated instruction, and too high costs every context.

A rule moves up only when two sibling levels hold it, and the person says so. The lesson then moves up, with the project names removed.

## Limits

- Make at most ten changes in one session. The person reviews each one, so a short queue gets a careful review.
- Never push. The person reviews the queue and pushes.
- Never run a sleep pass. Only the person runs one, at a terminal.

## Record

Record the answer of each correction right after its change. An answer takes the ids of a whole group, with a comma between two:

    memcore sleep --answer <id>[,<id>] --with <path>
    memcore sleep --answer <id>[,<id>] --reason <text>

- After `remember` or `revise`, `--with` names the document that now holds the rule.
- For no change, `--reason` says why.
- After `forget`, no document is left. So `--reason` says that the memory went, and why.

## Report

End the session with a report to the person:

1. each change, with its document and the corrections that it answers;
2. each answer with no change, with its reason;
3. each instruction, skill or script that the person may want to change;
4. the count of the corrections that stay open, from `memcore sleep --list`.
