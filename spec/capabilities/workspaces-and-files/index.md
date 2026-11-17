---
code: WSAREA
type: index
area: workspaces-and-files
summary: >-
  Lists the capabilities that build a session's workspace and move files in and out of it.
---

# Workspaces and files

Lists the capabilities that build a session's workspace and move files in and out of it.

## Pages

<!-- generated:index -->

| Page | Code | Summary |
| :-- | :-- | :-- |
| [Artifacts](artifacts.md) | `ARTIFACT` | States what Lenny keeps as session artifacts, transcripts, blob resolution, deduplication, and artifact retention. |
| [Default clones](default-clones.md) | `CLONE` | States the default git clones a tenant-derived runtime declares and how they reach a session's workspace. |
| [File export](file-export.md) | `FILEX` | States the file export model between sessions and to clients, export limits, and scanning of exported files. |
| [Files in messages](files-in-messages.md) | `FILEMSG` | States how files travel with messages and output, including files a child returns to its parent. |
| [Materialization](materialization.md) | `MATER` | States how the adapter and the helper build a workspace from a plan inside the slot, the ordering and collision rules, atomicity, and the validation of results. |
| [Repository stores](repository-stores.md) | `REPO` | States the shared repository stores on a pod, the warm list, repository folders, and the warm-up checkout. |
| [Uploads](uploads.md) | `UPLOAD` | States upload authorization, the upload token, size caps, archive handling, and mid-session uploads. |
| [Workspace plan](workspace-plan.md) | `WSPLAN` | States what a workspace plan means: its sources, when it binds, its defaults, and the rules Lenny applies to each member. |

<!-- /generated:index -->
