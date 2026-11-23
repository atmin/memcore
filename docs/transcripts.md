# Transcripts

Transcripts are the verbatim store behind the memories. In other systems, recall comes from the verbatim store more than from the hierarchy<sup>[1](#sources)</sup>.

Transcripts live on a `transcripts` branch that is never checked out and never pushed. memcore commits snapshots to it with git plumbing. A snapshot never records a deletion, so a transcript outlives the agent's own cleanup. A snapshot reads the sessions of Claude Code and of Codex. It starts on its own at a call of memcore six hours after the last snapshot. The `SessionEnd` hook of Claude Code also starts one at the end of each session of Claude Code.

A snapshot keeps only the conversation:
- your text and the agent's text;
- the calls the agent made, as one-line descriptions;
- your reason when you reject a call, which is often your sharpest correction.

A snapshot drops tool output, thinking, attachments and per-line metadata. In one month of one developer's Claude Code sessions, that leaves 6 MB of 575 MB. With the full call inputs, it would leave 33 MB, or 7.7 MB gzipped.

Before memcore commits a snapshot, it redacts each text locally, before it encodes the line. So no escape of JSON hides a secret. The redaction uses:
- a pattern-and-entropy scanner for keys, tokens, private keys and connection strings;
- typed placeholders such as `[secret:api-key]`, so the conversation still reads correctly.

The same scanner checks every memory that an agent writes.

## The format

A snapshot keeps one JSONL file for each session, and one for each subagent. The path depends on the agent:
- Claude Code: `claude/<project>/<session id>.jsonl`, and `claude/<project>/<session id>/subagents/<file>.jsonl` for a subagent.
- Codex: `codex/<project>/<session id>.jsonl`, and `codex/<project>/<parent id>/subagents/<session id>.jsonl` for a subagent.

`<project>` is a hash of the project folder of Claude Code, or of the working directory of a Codex session. A folder name or a directory can hide a secret from the scanner. The header of each file keeps the directory, redacted. A file name that may hold a secret becomes `redacted-` and a hash of the name. Codex moves and renames its session files, so the path of a Codex transcript comes from its content.

A file starts with a header line, then holds one line for each part of the conversation:

```
{"agent":"claude","session":"<id>","dir":"<directory>"}
{"time":"<time>","who":"you","text":"Use small commits."}
{"time":"<time>","who":"call","text":"Bash: Run the tests"}
```

The header names the agent, `claude` or `codex`. The header of a `codex exec` run also holds `"source":"exec"`, so a reader can tell such a run apart.

`who` is one of these:
- `you` and `agent` for the text of each side;
- `call` for a call: its name, then the input that describes it best, in 200 characters at most;
- `rejected` for a call that you rejected, and your next message holds your reason;
- `parent` for the text that the main agent gave a subagent. The header of a subagent file names the parent session.

A call of Codex is one of these:
- `exec` and the first line of the command;
- `patch` and the paths of the changed files;
- `mcp` and the server and the tool;
- the kind of an extension, such as `web.search`, and its query, its address or its prompt.

Codex records no call that you rejected, so its transcripts hold no `rejected` line. A snapshot skips two kinds of Codex session:
- a guardian, which reviews the approvals of another session;
- a session that Codex imported from Claude Code, because the branch holds the transcript of Claude Code.

A forked subagent of Codex starts with a copy of the history of its parent, and its transcript leaves that copy out.

Codex compresses a session about 7 days after its last write, into a `.jsonl.zst` file. memcore reads that file itself, with no installed program. A compressed file that fails to decompress stops no other file. The snapshot names it, and the next snapshot reads it again.

A snapshot replaces a transcript only with a version that extends it. A version that diverges goes beside it, named by its blob. A last line without a newline is still being written, so it waits for the next snapshot. A line that fails to parse is counted, and the snapshot names its file.

## Sources

1. MemPalace <https://github.com/mempalace/mempalace>
