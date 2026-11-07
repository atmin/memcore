# Layout

The memory repo mirrors your home directory. Each document lives at the highest level where it holds:

```
memory/
├── README.md            index of the root level
├── memories/            you, in every project
├── skills/              procedures that hold everywhere
├── bin/                 scripts, linked onto your PATH
├── dev/                 ~/dev
│   ├── README.md
│   ├── memories/        what holds across your personal projects
│   └── app/             ~/dev/app
│       └── …
└── work/                ~/work
    ├── README.md
    ├── memories/        what holds across work repos
    ├── plans/           plans that span work repos
    ├── bin/             scripts for your work repos
    └── shop/            ~/work/shop
        ├── README.md
        ├── conventions.md
        ├── glossary.md
        ├── memories/
        ├── plans/
        ├── notes/
        ├── skills/
        └── bin/
```

Every level has the same parts:
- an index, `README.md`, with one line per document or child level;
- a folder per kind of document;
- a folder per child level.

The root is a level like any other, so nothing sits there loose. The folder gives the kind, so a filename gives only the topic.

| Kind | Holds |
| --- | --- |
| `memories/` | One current fact per file. When a fact is superseded, `forget` deletes it, and git keeps it. |
| `plans/` | How to do one piece of work, written for an agent with no context. A plan is deleted when its work merges. |
| `notes/` | Dated investigations. A note records findings at the time, not current truth. |
| `skills/` | One folder per skill, in the open SKILL.md format<sup>[1](#sources)</sup>. A skill is a procedure that an agent follows, with any scripts it needs. |
| `bin/` | Scripts: one executable file each, with any shared helper beside it. `push` links each script onto your `PATH`. |
| `conventions.md` | The rules of one repo, for an agent with no context. A repo that keeps its own `AGENTS.md` needs no conventions file. |
| `glossary.md` | The agreed terms at this level, each with one meaning. Agents use these terms and no synonyms for them. An entry may end with the words it forbids. `verify` blocks a document that uses one of them. Such an ending reads `Not: memory repository, memory store.` Any level can have a glossary. |

**Scripts.** An agent writes one-off scripts wherever it works. A script enters `bin/` only when it recurs: you ask for it. `draft script <name>` starts it from the template, and `revise` fills it. The template starts with a shebang, then one comment line that says when to run the script, then its usage. That comment line is the script's index line.

**Drafts.** `draft <kind> [<topic>]` starts a document from its template, at its level, and `revise` then fills it. A conventions file and a glossary take no topic:
- a plan: the template of the [plan](../skills/plan/TEMPLATE.md) skill, with its name and description;
- a note: its name, its description, a title and the date of the draft;
- a script: a shebang, its comment line, its usage and `set -eu`;
- a helper, or a file inside a skill: an empty file;
- a skill: its `SKILL.md`, with its name, its description and a title;
- a conventions file or a glossary: a title for the level.

A script runs the same way every time, from its arguments, with no decision inside. A procedure with judgment is a skill, and a skill carries any scripts that it needs. So a script is one executable file, and a shared helper sits beside it, not executable and never linked. Anything that needs a folder or a decision is a skill. Starting the local stack with one command is a script. Setting up a worktree, with steps and a check at the end, is a skill.

A script name is global, because a command name is. It is unique in the memory repo, and memcore refuses a name that a command on your `PATH` already uses. The level says only where the script lives and which agents see it in their index.

`push` links each new script onto `~/.local/bin` and removes the links of the scripts it deletes. A link points into the checkout of the accepted history, not into the working tree. So a script and its helpers run as you accepted them. A revision in the queue waits for your push. Until you push, a new script sits in the review queue unlinked, and its index line says unreviewed. `push` refuses a new script whose name a file in `~/.local/bin` already holds, unless memcore linked that file.

**Index.** memcore generates every index from the frontmatter of the documents, and commits it. So an index reads as a plain file. Nobody writes an index by hand, because the next write regenerates it. Each line says when to read:
- the line of a memory, a plan, a note or a skill is the `description` in its frontmatter;
- the line of a conventions file is fixed: "Read before you change anything in this repo.";
- the line of a glossary is fixed: "Read before you name anything at this level.";
- the line of a script is the first comment line of the script;
- the line of a document without a description, or a script without a comment line, says so;
- the line of a child level is fixed: "Read when you work under ~/<level>."

An index starts with a title: `# ~` for the root, and `# ~/<level>` for any other level. A section for each kind follows, in a fixed order: Memories, Skills, Scripts, Conventions, Glossary, Plans, Notes and Levels. The index leaves out an empty section. Each line links to its document by the path from the level, then gives the line. The line of a child level names the directory and links to its folder, as `[bin](+bin/README.md)`. The index of a shop repo looks like this:

```markdown
# ~/work/shop

## Memories

- [api-versions](memories/api-versions.md): Read when you call the API: it has two versions.

## Conventions

- [conventions](conventions.md): Read before you change anything in this repo.

## Levels

- [web](web/README.md): Read when you work under ~/work/shop/web.
```

A level has a budget of 40 lines. The budget counts what `orient` prints for the level: its memories, skills, scripts, conventions and glossary. `orient` prints no child level, because an agent never loads a sibling. It prints plans and notes as one line with their counts. So the budget limits what an agent sees, not what a level stores. `remember` warns past the budget and does not block.

The budget counts lines, and the `SessionStart` hook adds a limit in bytes for the whole path. The hook shows `orient` in full only up to 9,500 bytes. That limit covers the header and every level up to the root (see [Interface](interface.md)). Long descriptions can pass that limit within the budget. So `remember` also warns when the `orient` of its level passes 9,500 bytes.

**Placement.** Where a document goes follows from where the agent works:
- The working directory maps to the toplevel of the git repo that holds it, as a path relative to home. A worktree maps to its main checkout, through the common git directory.
- A directory in no repo maps to itself. Home maps to the root.
- A document that spans repos goes to the level of their common parent.
- A document about you goes to the root.

So a plan that you ask for anywhere in `~/work/shop` goes to `work/shop/plans/`. memcore creates a level and its missing ancestors on the first write there. A directory outside home has no level: reads there show the root, and writes fail with that reason.

A directory can carry any name, so its folder could collide with a kind folder of the level. So memcore escapes each segment of the level on its own:
- A segment gets a `+` in front when it names a kind folder or file, `README.md` or `.git`, ignoring case. So `~/bin` maps to `+bin/`, and `~/dev/Notes` maps to `dev/+Notes/`.
- A segment that starts with `+` gets one more, so `~/+tmp` maps to `++tmp/`.
- Every other segment stays as it is.

So each directory maps to one folder, and each folder maps back to one directory. A folder that no directory maps to, such as `+tmp/`, is not a level, and the index leaves it out.

**Lookup.** An agent reads the indexes from its own level up to the root, as `orient` prints them. Then it opens documents on demand. A skill's index line is its description. So skills load the way agents expect: the description first, the body on demand. Each memory names the sessions it came from.

## Sources

1. Agent Skills, the SKILL.md specification <https://agentskills.io>
