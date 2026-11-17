# Steps: 0113 Records and workflow

## Steps

### 0113.S1 Create the records
- status: Landed
- lanes: [non-specification]

Write `changes/README.md`, `issues/README.md`, `changes/BOARD.md`, and `changes/programs/v1.md`, and the skeletons of 0114 and 0115.

### 0113.S2 Move the designs
- status: Landed
- lanes: [non-specification]
- depends-on: [0113.S1]

A script moves each design and its evidence, splits out the review history, renames "Proposal split and order" to Steps, sets the status line, rewrites links and path mentions, and moves the spike's evidence into 0115. The script lives in this change's `evidence/` and is not committed elsewhere.

### 0113.S3 Exclude `changes/` from the gates, and update the agent instructions
- status: Landed
- lanes: [non-specification]
- depends-on: [0113.S2]

Update `scope.go`, the residual registers, `AGENTS.md`, and the contribution page, and run tier 0 and the link check.

### 0113.S4 Convert the moved designs' proposals into steps, and generate the board
- status: Landed
- lanes: [non-specification]
- depends-on: [0113.S3]

Rewrite the Steps section of each change from 0118 to 0153 into step entries: one step per proposal, numbered in the design's order, with its old labels as aliases, its dependencies within and across changes, and the findings it closes. A change whose units are mechanism sections (such as GH-1 to GH-3) gains a Steps section that links each step to its section. The design text outside the Steps section and the dependency prose it absorbs is unchanged. A script in this change's `evidence/` checks every step entry (format, identifiers that resolve, no dependency cycle) and generates `BOARD.md`. A reviewer per change compares the old and new Steps sections and confirms that no step, edge, closed finding, or reason was lost.

### 0113.S5 Split each change folder, nest the detailed design, and shorten the status lines
- status: Landed
- lanes: [non-specification]
- depends-on: [0113.S4]

A script in this change's `evidence/` splits each `change.md` into `change.md`, `decisions.md`, and `steps.md`, moves the mechanism sections under "Detailed design" one heading level down, rewrites every link to a heading that moved, and checks that every line of text survives and that no link breaks. Each status line is cut to the status, revision, date, and programs, with its former text quoted in `review-history.md`, and an agent per group of changes copies the facts that still hold into the section of `change.md` where they belong. `steps.py` reads the new files, and the changes README describes them.
