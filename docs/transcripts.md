# Transcripts

Transcripts are the verbatim store behind the memories. In other systems, recall comes from the verbatim store more than from the hierarchy<sup>[1](#sources)</sup>.

Transcripts live on a `transcripts` branch that is never checked out and never pushed. memcore commits snapshots to it with git plumbing. A snapshot never records a deletion, so a transcript outlives the agent's own cleanup. A snapshot starts on its own, the same way a sleep pass does.

A snapshot keeps only the conversation:
- your text and the agent's text;
- the calls the agent made, as one-line descriptions;
- your reason when you reject a call, which is often your sharpest correction.

A snapshot drops tool output, thinking, attachments and per-line metadata. In one month of one developer's Claude Code sessions, that leaves 6 MB of 575 MB. With the full call inputs, it would leave 33 MB, or 7.7 MB gzipped.

Before memcore commits a snapshot, it redacts the snapshot locally with:
- a pattern-and-entropy scanner for keys, tokens and connection strings;
- a small local model for personal and production data, including data in other languages and scripts;
- typed placeholders such as `[phone]` or `[secret:api-key]`, so the conversation still reads correctly.

The same filter checks every memory that an agent writes.

## Sources

1. MemPalace <https://github.com/mempalace/mempalace>
