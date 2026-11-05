# Open questions

- **Capture.** Should `remember` run only when the agent decides, or should a hook capture every correction and leave the sorting to sleep?
- **Disclosure.** Which of these ideas keep every hop small?
  - A split on overflow, like a b-tree node: when a level passes its budget, documents move into a subfolder and the parent keeps one line for it.
  - A fixed token budget for `orient`.
  - A small pinned set for the rules an agent would not think to look up.
  - Triggers: a memory declares a command or path pattern, and a hook in the agent's harness surfaces it before a matching tool call.
  - Reads in sleep, so that sleep promotes what agents open often and rewords what they never follow.
- **Redaction model.** Which small local model, if any, finds personal and production data in mixed languages? Is it bundled with the executable or downloaded on first use?
- **Local thinking.** A later option runs the thinking on a local model instead of a hosted agent. What form should it take?
- **Sharing.** Can an organisation's subtree become a team's shared memory, and what does review look like then?
- **Transcripts off the machine.** Once the remote is end-to-end encrypted, should the transcripts branch be pushed too, as a backup and to reach other machines? A purge would then rewrite it on the remote too.
- **The guard on gitmote.** gitmote speaks smart HTTP with tokens, and a serverless remote has no server to check a touch, because the bucket credential is the write access. The write credential would have to be sealed to a presence-bound key and unsealed per push. How?
- **Chat apps.** Chat apps have no shell; they reach outside tools only through MCP connectors. Should memcore reach them too?
