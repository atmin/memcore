---
name: build
description: Use when the person hands over an approved plan. It builds every step in a git worktree, with one commit and one review per step, and stops when the work is ready for review.
---

# Build

The person approved the plan, so build every step without asking. The person reviews the result in bulk at the end.

## Start

1. Fetch the base. Then create a worktree on a new branch: `git worktree add <path> -b <branch> <base>`. The conventions of the repo name the worktree path and any setup that it needs.
2. Work only in the worktree. Never commit in the main checkout.

## Each step

1. Write the evidence of the step first, then the change.
2. Run the verification of the plan.
3. Stage the files of the step by path, and check the staged diff. Commit with the message of the step.
4. Have the commit reviewed with the [review](../review/SKILL.md) skill.
5. Answer every finding. Fix it with `git commit --fixup=<step sha>`, or give the reason that it stands. Run the verification again after each fix.

## Stop

Stop and report in the cases that the plan names:
- a change on the base invalidates the plan;
- the criteria and the steps disagree;
- the work needs a decision that no constraint covers.

Add the question to the open questions of the plan.

## Ready for review

After the last step, run the verification once more on the final commit. Then report:
1. the output of `git log --oneline <base>..` for each repo, and whether anything is pushed;
2. the commit that the verification passed on, and any check that did not run;
3. each finding, with its fixup or the reason that it stands;
4. anything that the plan got wrong.

The person may ask for more fixes. Make them as fixup commits.

## Publish

Push only when the person says so. Before the first push, fold the fixups into their commits with `git rebase --autosquash <base>`. Never rewrite pushed history, and never push with `--force`. After the first push, a fix is a plain commit.

## Resume

`git worktree list` shows the worktree. Walk `git log --oneline <base>..`, and continue from the first step whose review or answers are missing.

## Clean up

After the merge, remove the worktree with `git worktree remove <path>`.
