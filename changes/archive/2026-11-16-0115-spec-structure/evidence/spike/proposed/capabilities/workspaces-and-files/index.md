---
code: WSAREA
type: index
area: workspaces-and-files
summary: >-
  Lists the capabilities that build a session's workspace and move files in and out of it.
---

# Workspaces and files

The capabilities in this area build a session's workspace and move files in and out of it. A workspace plan names the sources, the adapter and the materialization helper build the workspace inside the slot, and files travel by upload, with messages, by export between sessions, and as stored artifacts. Repository stores on the pod hold git repositories that sessions check out.

## Reading paths

- To change how a workspace is built, read [Workspace plan](workspace-plan.md), then [Materialization](materialization.md), then [Workspace plan](../../interfaces/workspace-plan.md).
- To change how files reach a running session, read [Uploads](uploads.md), then [Files in messages](files-in-messages.md).
- To change how repositories are prepared on a pod, read [Repository stores](repository-stores.md), then [Default clones](default-clones.md), then [Git access](../network-and-egress/git-access.md).

## Capabilities

| Page | Code | Summary |
| :-- | :-- | :-- |
| [Workspace plan](workspace-plan.md) | WSPLAN | States what a workspace plan means: its sources, when it binds, its defaults, and the rules Lenny applies to each member. |
| [Materialization](materialization.md) | MATER | States how the adapter and the helper build a workspace from a plan inside the slot, the ordering and collision rules, atomicity, and the validation of results. |
| [Uploads](uploads.md) | UPLOAD | States upload authorization, the upload token, size caps, archive handling, and mid-session uploads. |
| [Files in messages](files-in-messages.md) | FILEMSG | States how files travel with messages and output, including files a child returns to its parent. |
| [File export](file-export.md) | FILEX | States the file export model between sessions and to clients, export limits, and scanning of exported files. |
| [Artifacts](artifacts.md) | ARTIFACT | States what Lenny keeps as session artifacts, transcripts, blob resolution, deduplication, and artifact retention. |
| [Repository stores](repository-stores.md) | REPO | States the shared repository stores on a pod, the warm list, repository folders, and the warm-up checkout. |
| [Default clones](default-clones.md) | CLONE | States the default git clones a tenant-derived runtime declares and how they reach a session's workspace. |

## Components that carry out this area

- [Adapter](../../components/agent-pod/adapter.md): The adapter container: startup sequence, the slot registry, gateway and runtime connections, health checks, process settings, and metrics.
- [Runtime supervisor](../../components/agent-pod/runtime-supervisor.md): The slot containers and the runtime supervisor that runs as PID 1 in each: process launch and end, the sweep, the file-descriptor broker, and the materialization helper.
- [Init and support containers](../../components/agent-pod/init-and-support-containers.md): The init containers that stage platform binaries and fill repository stores, and the keepalive container.
- [Object store](../../components/external/object-store.md): The object store: backends, HA, versioning, replication, lifecycle rules, encryption, and outage handling.

## Interfaces this area exposes

- [Workspace plan](../../interfaces/workspace-plan.md): The WorkspacePlan schema with every field, and its versioning.
- [Files and artifacts](../../interfaces/client-api/files-and-artifacts.md): Upload, artifact, blob, and export operations.
- [Filesystem and environment](../../interfaces/runtime-protocol/filesystem-and-environment.md): The paths, environment variables, and injected configuration a runtime sees in its slot.
