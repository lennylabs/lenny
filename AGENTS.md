# Agent Instructions

## Where work is recorded

- `spec/` states the current system. It is the source of truth that code and tests implement.
- `changes/` holds every planned or in-progress change, one folder per change. [`changes/README.md`](changes/README.md) describes the folder, the change template, statuses, programs, the workflow, and the helpers. [`changes/BOARD.md`](changes/BOARD.md) lists the active changes.
- `changes/programs/` holds programs: plans over several changes. Before working on a change that belongs to a program, read the program's reading guidance. The active program is [`changes/programs/v1.md`](changes/programs/v1.md), and its "Reading the program" section states how its member changes are read together.
- `issues/` holds known discrepancies that no change has finished. [`issues/README.md`](issues/README.md) describes the layout and the lifecycle.
- `proposals/` holds the change proposals written before `changes/` existed. They stay as written.

## Non-Interactive Shell Commands

**ALWAYS use non-interactive flags** with file operations to avoid hanging on confirmation prompts.

Shell commands like `cp`, `mv`, and `rm` may be aliased to include `-i` (interactive) mode on some systems, causing the agent to hang indefinitely waiting for y/n input.

**Use these forms instead:**
```bash
# Force overwrite without prompting
cp -f source dest           # NOT: cp source dest
mv -f source dest           # NOT: mv source dest
rm -f file                  # NOT: rm file

# For recursive operations
rm -rf directory            # NOT: rm -r directory
cp -rf source dest          # NOT: cp -r source dest
```

**Other commands that may prompt:**
- `scp` - use `-o BatchMode=yes` for non-interactive
- `ssh` - use `-o BatchMode=yes` to fail instead of prompting
- `apt-get` - use `-y` flag
- `brew` - use `HOMEBREW_NO_AUTO_UPDATE=1` env var
