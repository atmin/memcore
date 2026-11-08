---
name: build
description: Use when the person hands over an approved plan. It builds each step in a worktree, with one commit and one review per step.
---

# Build

The person approved the plan, so build every step without asking. The person reviews the result in bulk at the end.

## Start

1. Fetch the base. Then create a worktree on a new branch: `git worktree add <path> -b <branch> <base>`. The conventions of the repo name the worktree path and any setup that it needs.
2. Work only in the worktree. Never commit in the main checkout.

## Each step

1. Write the evidence of the step first, then the change.
2. Run the step checks of the plan until they pass.
3. Stage the files of the step by path, and check the staged diff. Commit with the message of the step.
4. Start the full checks of the plan in the background. Meanwhile, have the commit reviewed with the [review](../review/SKILL.md) skill.
5. Answer every finding, and a failure of the full checks. Fix it with `git commit --fixup=<step sha>`, or give the reason that it stands. Run the step checks after each fix. The full checks of the next commit cover the fixups.

Start the next step only after the full checks and the review are done. So a failure of the full checks belongs to one step. When the plan names no step checks, the full checks are the step checks too.

## Stop

Stop and report in the cases that the plan names:
- a change on the base invalidates the plan;
- the criteria and the steps disagree;
- the work needs a decision that no constraint covers.

Add the question to the open questions of the plan.

## Ready for review

After the last step, run the full checks on the final commit. Then report:
1. the output of `git log --oneline <base>..` for each repo, and whether anything is pushed;
2. the commit that the full checks passed on, and any check that did not run;
3. each finding, with its fixup or the reason that it stands;
4. anything that the plan got wrong.

The person may ask for more fixes. Make them as fixup commits.

## Publish

Push only when the person says so. Before the first push, fold the fixups into their commits with `git rebase --autosquash <base>`. Never rewrite pushed history, and never push with `--force`. After the first push, a fix is a plain commit.

## Resume

`git worktree list` shows the worktree. Walk `git log --oneline <base>..`, and continue from the first step whose review or answers are missing.

## Clean up

After the merge, remove the worktree with `git worktree remove <path>`.
