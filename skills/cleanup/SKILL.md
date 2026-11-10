---
name: cleanup
description: Use when a level of memory is hard to read, or orient prints it over its budget. It tidies one level while the person watches.
---

# Cleanup

A level is legible when a reader with no context understands it from its index. An agent picks a document from the same index lines that a person reads. So an index that confuses a person also misleads an agent. Cleanup makes one level legible, in a session with the person.

Make every change through a memcore command, so it lands in the review queue. Never edit a file of the memory repo by hand, and never push.

## Survey

Read the level first, and change nothing. Two directories take part:
- the directory of the level, such as `~/work/shop`, where every `memcore` command runs;
- the memory repo, where the level is a path, such as `work/shop`.

Collect these numbers:
1. The lines that the level costs in `orient`, against its budget of 40: `memcore orient | grep -c '^- <level>/'`.
2. The faults, and the descriptions that name no moment to read: `memcore verify <level>`.
3. The size of each document: `(cd <memory repo> && find <level>/memories <level>/notes -name '*.md' ! -name README.md | xargs wc -c)`.
4. The reads of each document, which follow moves: `memcore stats <level>`.
5. The body of each document whose name and description leave its topic unclear: `memcore show <path>`.

Give the person the numbers of the survey in a few lines. Then look for the faults below.

## Faults

1. A name states the type instead of the topic, such as a `reference-` prefix from an import of auto-memory.
2. A description summarizes the document, instead of saying when to read it.
3. A fact sits at the wrong level: it repeats in sibling levels, or it holds for the whole parent level.
4. A document is stale, or holds state that belongs in a note or a plan.
5. A document is too large for its kind, or a folder holds too many documents to scan.

### Names

- A name states the topic, in lower case with hyphens. The `type` key holds the type, so the name never repeats it.
- A name stays unique in the memory repo, because a `[[name]]` link depends on it. So keep enough of the topic in a name to read it outside its folder.

### Descriptions

A description says when to read the document, as "Read before you ..." or "Read when ...". It names the moment first, then the topic. It keeps to 25 words.

### Topic folders

- Start from two values, and test them. A folder lists 15 documents at most. A topic folder holds 3 documents at least.
- Split a folder into topic folders past 15 documents, or when `orient` prints the level over its budget.
- Name a topic after a part of the domain, such as `payments` or `local-setup`. Never name it after a type or a date.
- Nest a topic folder only when it lists more than 15 documents itself.
- Leave a document that fits no topic directly in the folder of its kind.

A topic folder of memories costs one line in `orient`, and a memory outside it costs one line each. Notes and plans cost one line for all of them. So topic folders of notes serve the person who browses, not the budget. Keep the rules about how the person works at the top of `memories/`, so `orient` prints each one.

### Levels

A fact about a whole parent level belongs there, such as a tool that every repo under it uses. Move it up now: a document that spans repos goes to the level of their common parent. `memcore move <name> <parent level>/memories/ --reason <text>` carries it whole, with its sessions and the links to it.

Promotion is different. A fact moves up one level only when it appears in two or more sibling levels. The lesson moves up, with the project names removed. The specific fact stays where it is. Placement is low by default: too low costs one repeated instruction, and too high costs every context.

A promoted lesson is a new memory, so `move` cannot carry it. Write it with `memcore remember --at <directory>`, under a name that no memory holds yet. Each copy keeps its own name until it goes. When `remember` lists one of the copies as a near duplicate, write the lesson again with `--new`. Then revise each copy down to its specific fact, or forget a copy with nothing specific left.

A refused `remember` writes nothing. So check with `memcore show` that the lesson exists before you forget a copy. `remember` also refuses a link to a memory that does not exist yet. Write two memories that link each other in three steps:
1. Write the first memory without the link.
2. Write the second memory, with its link to the first.
3. Revise the first memory to add its link to the second.

### Stale documents and state

- A dead reference shows up in `verify`. Check the reference against the project, then fix or forget the document.
- `verify` warns on a plain-text name of a memory that moved or went. A name from an import that never was a memory escapes it. So search the bodies for such names too, such as a `project-` prefix from an import of auto-memory.
- Ticket, incident or project state goes into a note or a plan, never into a memory.
- Age alone never makes a document stale. When the evidence is unclear, ask the person.

### Size

No fixed size turns a memory into a note, a topic folder or a doc of the project's own repo. Ask the person about each large document, with its data:
1. its size, against the sizes of the other documents at the level;
2. its reads, from the survey;
3. its line in `orient`;
4. where it would go, and what would link to it there.

## Propose

Show the person one proposal for the whole level, grouped by fault, with one line for each change:
1. a rename: the old name and the new name;
2. a topic folder: its name, its description and its documents;
3. a move to another level: the copies, the target level and the fact or the lesson;
4. a stale document: the evidence, and whether to revise or forget it;
5. a large document: the question, with its data.

Apply nothing until the person approves. The person may approve only part of the proposal. Keep each rejected change and its reason for the findings.

## Apply

Apply the approved changes in this order, so each command sees the final names:

1. Start a topic folder with `memcore draft topic <kind>/<topic> --description <text> --at <directory>`, where `<kind>` is `memories` or `notes`. Then move its documents into it at once.
2. Rename or move each document with `memcore move <name or path> <new name, folder or path> --reason <text>`. A move to `<level>/memories/<topic>/<new name>.md` renames the document and files it in one commit.
3. Rewrite a description or a stale body. Print the document with `memcore show <path>`, then pipe the whole new version to `memcore revise <name or path> --reason <text>`.
4. Move each placed memory to its level with `memcore move`. Write each promoted lesson, then revise or forget its copies. `forget` refuses while a document links to the one it removes, so revise that document first.
5. Run the survey again, and compare it with the first one.

Turn a memory into a note with three commands:

    memcore draft note <note name> --description <text> --at <directory>
    memcore revise <level>/notes/<note name>.md --reason <text> < note.md
    memcore forget <memory name> --reason <text>

`note.md` holds the frontmatter that `draft` wrote, then the body of the memory. Let `--reason` name the fault that the change fixes, such as "The name states the topic." After each batch, tell the person what changed and what comes next.

## Findings

Write the findings as a note at the level: `memcore draft note cleanup-<YYYY-MM-DD> --description "Read before the next cleanup of this level."`. Then fill it with `memcore revise`. The note holds:
1. the survey before and after: the lines in `orient`, the warnings of `verify` and the documents in each folder;
2. each rejected change, with its reason;
3. each answer about size, with its reason;
4. each fault that no command could fix, and each step that was slow or awkward;
5. each start value of the topic folders that did not fit, and the value that did.

The note counts in the folder of notes, so check the limit again after you write it. The next cleanup of the level reads this note first.
