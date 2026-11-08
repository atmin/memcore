# memcore: conventions for agents

Read [README.md](README.md) first. It indexes the design docs and the skills.

## Work

- Plan a change with the [plan](skills/plan/SKILL.md) skill. Build an approved plan with the [build](skills/build/SKILL.md) skill.
- Write every reply and every document by the [voice](skills/voice/SKILL.md) skill, with the terms of the [glossary](docs/glossary.md).
- Keep the docs current truth. When behaviour changes, the same plan updates the affected docs.
- Push only when the person says so.

## Code

- memcore is one Go module, and it builds with `CGO_ENABLED=0`.
- `cmd/memcore` holds the entry point. `internal/` holds one package for each part in [Architecture](docs/architecture.md): `commands`, `store`, `readers`, `guards` and `ui`.
- Use the standard library first. A new dependency is an open question in the plan.
- memcore runs the installed git, and never links a git library.
- Wrap each error with its context: `fmt.Errorf("<what failed>: %w", err)`.

## Tests

- Write the tests of a step before its code. Use table-driven tests where cases repeat.
- A test that needs git runs the installed git in a temporary repo from `t.TempDir()`.
- No test calls a real agent. Tests replace the headless agent with a fake.
- Checks with a real agent live in `scripts/`, and run by hand. `go test` never runs them.
- The tests of `internal/commands` set the process environment, so `t.Parallel()` cannot run them. `TestMain` runs each top-level test in its own process instead, `MEMCORE_TEST_PROCS` at a time. `MEMCORE_TEST_PROCS=1` runs them all in one process.
- `newMemory` copies a memory repo that `TestMain` built once with `init`. A test that changes the environment before `newMemory` gets a real `init`.

## Verification

Run the checks from the root of the repo. Each command must pass, and `gofmt -l .` must print nothing.

The step checks run while a step is built, and before its commit. They run only the tests of the step:

    gofmt -l .
    go vet ./...
    go test -run '<the tests of the step>' <their packages>
    CGO_ENABLED=0 go build ./cmd/memcore

The full checks run every test. They run in the background during the review of each commit, and on the final commit of a branch, before a merge or a push:

    gofmt -l .
    go vet ./...
    go test ./...
    CGO_ENABLED=0 go build ./cmd/memcore

A step that changes a helper of many tests, such as `newMemory`, runs the full checks as its step checks.

## Worktree

Create the worktree next to the main checkout, as `~/dev/memcore-<slug>`. Go shares its module cache, so a worktree needs no other setup.

## Commits

Write one line with no body: `<type>: <imperative subject>`, as in Conventional Commits.
