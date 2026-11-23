---
id: GATE-I8
status: open
kind: question
severity: medium
requirements: []
---

# The release image build files are outside the merge-gate review list

## Specification

No specification page owns the merge gate, which `TESTING.md` ("Merge gate") and `.claude/rules/test-coverage.md` ("Gates") define. The issue is filed under `GATE`, the issue code that `issues/README.md` ("Codes outside the specification") assigns to the merge gate. The security review of the branch `fix/gate-follow-ups-2` raised it on 2026-11-20. That branch added `.github/workflows/release.yml`, `go.work`, `go.work.sum`, and `vendor/` to the list in `.claude/rules/git-workflow.md` ("Review changes to the merge gate") by the owner's answer to question GT-19. Whether the list also covers the files the release images are built from is the owner's decision, because the list then covers inputs of the release build beyond the gate's own files.

## Evidence

- The image job of `.github/workflows/release.yml` builds each image with `file: ${{ matrix.file || './Dockerfile' }}` and `platforms: linux/amd64,linux/arm64`, and the matrix entry of `lenny-supervisor` names `file: ./Dockerfile.supervisor`.
- `Dockerfile` copies the build context and compiles each binary with `RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/lenny ./cmd/"$BINARY"`.
- `.dockerignore` at the repository root decides which files the build context holds.
- The list in `.claude/rules/git-workflow.md` ("Review changes to the merge gate") names none of `Dockerfile`, `Dockerfile.supervisor`, or `.dockerignore`.

## Gap

An edit to a Dockerfile or to `.dockerignore` merges without the `review` skill, and it can make the released image differ from the tree the merge gate tested. A Dockerfile can add `-modfile`, `-tags`, `-overlay`, or `GOFLAGS` to the `go build`, or rewrite a source file before it, and a `.dockerignore` entry can drop a file from the build context. The merge gate reads the unmodified tree in each case and passes.

## Suggested resolution

The options are:

1. Add `Dockerfile`, `Dockerfile.supervisor`, any other Dockerfile that a matrix `file` of `.github/workflows/release.yml` names, and `.dockerignore` to the list, with the reason stated in the rule.
2. Keep the list to the gate's own files and the inputs of its checks, and record that the release build files are reviewed by the release process instead.

The owner decides.
