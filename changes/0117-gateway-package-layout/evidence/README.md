# Evidence: 0117 Gateway package layout

Measurements of `pkg/gateway`, `cmd/lenny-gateway`, and their importers, taken on 2026-11-16 at commit `b1248f85b` of `main`.

| File | Holds |
| :-- | :-- |
| `measure.py` | The script that assigns each package to a part of the proposed layout and measures sizes, import sites, and the import edges between parts. Its header gives the commands. |
| `packages.tsv` | One row per package under `pkg/gateway` (251 rows): the part the layout assigns, the note (the concern directory for a package that leaves, or the change that removes it), the non-test Go file count, non-test and test line counts, and the number of Go files outside the package's own directory that import it. |
| `sessionserver-files.tsv` | One row per non-test file of `pkg/gateway/sessionserver` (109 files, one of them behind the `contract` build tag): the part it belongs to and its line count, with totals per part at the end. |

## How the numbers were taken

- Packages and imports come from `CGO_ENABLED=0 go list -json ... ./...`. Line counts include blank lines and comments.
- An import site is a Go file anywhere in the tree, tests included, that imports the package, counted once per package. A file that imports three gateway packages counts once for each, so sums over packages count some files more than once.
- The part of each package follows the rules at the top of `measure.py`. The assignment is a proposal: decisions 1, 2, and 5 of `decisions.md` can move rows between "domain", the gateway parts, and "removed".
- The file assignment of `sessionserver` was made by reading each file's functions. Files with no seam or domain content default to the session core.

## Other measurements cited in `change.md`

- Importers outside `pkg/gateway`: a scan of every tracked Go file for `pkg/gateway/` import literals, grouped by top-level directory.
- Path references outside Go: `git grep -c "pkg/gateway/"` over tracked non-Go files outside `changes/` and `proposals/`, and the same count of `tests/registers/spec-section-citations.yaml` on the branch `change/0115-spec-structure`.
- Files the kind renames of 0137.S5 touch: `git grep -lE` for `Sandbox`, `SandboxClaim`, `SandboxTemplate`, `SandboxWarmPool`, `pkg/sandbox`, and `pkg/controller/sandbox` over `pkg/gateway` and `cmd/lenny-gateway`, counted per package.
