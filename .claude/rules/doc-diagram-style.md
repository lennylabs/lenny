# Diagram style

Project-wide style rules for SVG documentation diagrams. These rules apply to every `.svg` file in `docs/assets/diagrams/`, to the markdown that embeds them, and to the ASCII fallback blocks that accompany them.

The companion rules in `doc-style.md` apply to any prose that appears in a diagram (titles, captions, footers, alt text).

## Top-level principle

Diagrams are flat, geometric, and readable at thumbnail size. They use straight lines, a consistent warm palette, and Helvetica everywhere. They convey one concept per diagram. If the diagram cannot be described in one declarative sentence in the page caption, it is doing too much.

## Visual style

### Canvas

- SVG `viewBox` is explicit on the root element.
- White page background. Render with `<rect class="b-bg" width="W" height="H"/>` plus `.b-bg { fill: #ffffff; }`.
- 20–40 px outer margin on all sides.

### Palette

Reuse these values across diagrams. Define them as classes in a `<style>` block inside `<defs>`:

| Role | Color |
|:--|:--|
| Page background | `#ffffff` |
| Cream card (default) | `#fffaf0`, stroke `#1f2933` |
| Mustard card (primary) | `#ffe9c7`, stroke `#1f2933` |
| Sage card (accent) | `#d6e9d2`, stroke `#1f2933` |
| Coral box (error or event payload) | `#fde2d6`, stroke `#c84a1d` |
| Primary text | `#1f2933` |
| Sub text | `#54616e` |
| Section label (orange) | `#b56b1f`, with `letter-spacing: 0.04em` |
| Foot text (muted) | `#8a7755` |
| Accent line (coral, dashed) | `#c84a1d` |
| Faint guide line | `#c0b290` |
| Cream-brown divider (dashed) | `#d3c8b0` |

Custom palettes are an escape hatch only when an existing diagram already uses them locally; do not introduce new colors casually.

### Fonts

- `font-family="Helvetica, Arial, sans-serif"` on the root `<svg>`.
- Monospace for code, file paths, and env vars: `'SF Mono', 'Menlo', 'Consolas', monospace` via a class so the override is local.
- No web fonts. No hand-drawn fonts. No `feTurbulence` or `feDisplacementMap` filters (the previous "Style B" wobble is retired).
- Type scale: title 22 px, section label 14–16 px, body or card title 17–22 px, sub 14–16 px, mono 13–15 px, foot 13 px. Pick one size per role and keep the same role at the same size across the diagram.

### Lines and corners

- All lines are straight or orthogonal. No hand-drawn wobble.
- Rounded corners: `rx=6` for narrow cards (40–60 px tall), `rx=8`–`rx=10` for wider primary cards.
- Stroke widths: 2.0–2.5 for primary card borders, 1.6–2.0 for secondary, 1.1–1.5 for faint guide lines.
- Lines use `stroke-linecap: round` for cleaner termini.

## Arrows

### Markers

- Use `markerUnits="userSpaceOnUse"` on every marker so the head size is proportional to the canvas instead of to the stroke width. Stroke-width scaling produces an arrowhead that engulfs the line on short spans.
- Default marker:

  ```xml
  <marker id="b-arrow" viewBox="0 0 12 12" refX="10" refY="6"
          markerWidth="10" markerHeight="10"
          markerUnits="userSpaceOnUse" orient="auto">
    <path d="M 0 0 L 11 6 L 0 12 z" fill="#1f2933"/>
  </marker>
  ```

- Match the marker fill to the line stroke. A dashed coral event line uses a second marker with `fill="#c84a1d"`; default black lines use `fill="#1f2933"`. A dark arrowhead on a coral dashed line reads as a separate element rather than as the arrow's tip.

### Geometry

- Leave a 4–8 px gap between the path endpoint and the target box edge so the arrowhead reads as terminating at the box rather than overlapping it.
- Arrows between rows of boxes (consumers → targets, sources → server) end at the top edge of the target box, not mid-gap. Floating arrowheads suggest the arrow leads nowhere.
- T-junctions: one source feeding multiple targets. The trunk path carries no `marker-end`; only the branch paths carry arrowheads. A stray arrowhead in the middle of the trunk is the most common rendering bug.
- Sequence-diagram messages: end the message path 4–6 px before the target lifeline.
- A vertical arrow crossing tiers must terminate on a box whose horizontal extent contains the arrow's x-coordinate, centered when possible. When source and natural target are not aligned, reposition one of the cards rather than route through a long horizontal jog across unrelated content.
- A parent card that branches to a column of children must sit directly above that column so the down-arrow drops straight onto the first child. A parent placed above a sibling column reads as pointing to the sibling.

## Boxes and layout

- Group related boxes in a row with consistent height and corner radius. Rows read as one tier.
- Cards have internal padding of 16–20 px on the left for text. A section label (if any) sits 22–26 px from the top; the card title 22–30 px below that.
- Inner cards inside a container card share equal left and right padding from the container's edges. Compute padding as `(container_width − total_child_width − inner_gaps) / 2`. Do not let the rightmost child touch the container's right edge while the leftmost sits inset.
- Multiple text lines inside a box: 18–22 px between baselines.
- When a card contains multiple discrete items, prefix each with a bullet (`•`) and indent the text after the bullet consistently so the bullet column aligns. Skip bullets for paragraph-style descriptions or for cards with a single item.
- Vertical spacing between items inside a card is uniform regardless of whether each item is one line or several. A wrapped item uses the same line-height as the gap between single-line items so every baseline sits on a regular grid. Continuation lines indent to align with the text after the bullet (hanging indent).
- Sibling tiers (sources, consumers, targets) all have outlined boxes or all do not. Mixing outlined cards with bare text in a parallel row reads as a missing outline rather than a deliberate choice.
- Do not place a single purely-vertical path inside an SVG `<filter>` group. The bounding box is degenerate and bbox-relative filter regions can drop the rendering. The project does not currently use SVG filters; this rule is here to prevent reintroducing them.

## Content

### Example identifiers

Use the cryptography convention from `doc-style.md`: `alice`, `bob`, `carol`, ... for human users; `acme` (`acme.com`, `Acme Corp`) for the example tenant. Do not use real names of project people, customers, or contributors in diagrams.

### Names and references

- No spec section references (`§4.6`, `§13.5`, etc.) in diagram text. Diagrams are often reused across docs and the spec; section numbers age out.
- No historical content. Do not include former names, deprecated synonyms, or rename notes inside a diagram. Authors and consumers see the current name only.
- Non-exhaustive lists use `etc.` followed by a period, not a trailing ellipsis. When the list is exhaustive, use a proper conjunction (`a, b, and c`).

### Text fit

Text must not overflow its containing box at the rendered size. When a string does not fit at the current size:

1. Shrink the font by one tier in the scale.
2. Widen the box.
3. Split into two lines.
4. Rephrase to a shorter equivalent.

Apply one option per row; do not mix font sizes within a row.

### Captions and alt text

The markdown `![alt](svg)` alt text describes what the diagram shows in prose terms and follows `doc-style.md`. No em-dash overuse. No "X, not Y" rhythm. No marketing language. No "shape" as a generic noun.

## ASCII fallbacks

Every diagram has an HTML-comment ASCII fallback immediately after the image so machine consumers and screen-reader-friendly source readers can recover the structure when the SVG is not rendered.

### Hazards inside HTML comments

XML comments cannot contain the substring `--`. The most common trap is an ASCII arrow that contains `-->` or `--->`: the first occurrence closes the comment prematurely, and everything after it renders as visible content.

Replacements:

- `-->` between boxes becomes `==>` or `===>`.
- `--(label)-->` becomes `===(label)==>`.
- `--->|` and `<---` in sequence diagrams become `===>|` and `<===`.
- Avoid `--watch` and similar `--flag` patterns inside comments. Refer to "the watch flag" or "the long-running form" instead.

The check is mechanical: every `-->` inside an HTML comment that has non-whitespace content on the same line after it is a bug. The sweep is in `How to apply when editing` below.

### Structure

- Open with a short label: `ASCII fallback for the diagram above (<name>):`.
- Use `+`, `-`, `|`, backtick, and forward slash for the structural skeleton. Unicode box-drawing characters (`├`, `└`, `│`) are not portable across the renderers we care about; they misalign in markdown previews.
- Match the SVG's identifiers (alice, acme, …) and field names.
- Mirror the SVG's structure faithfully. The ASCII is the same diagram in another medium, not a different summary.

## Where these rules apply

- All `.svg` files in `docs/assets/diagrams/`.
- All markdown that embeds a diagram via `![alt](...svg)`.
- ASCII fallback blocks adjacent to those images.
- The prose paragraphs immediately before and after the diagram, where the same content rules from `doc-style.md` apply.

## How to apply when editing

1. Render the SVG and visually inspect the result. On macOS: `qlmanage -t -s 2000 -o /tmp/diag <file>.svg`, then read the PNG. Render at two scales (for example 1080 and 2400) to catch overflow that only surfaces at thumbnail resolution.
2. Sweep the source for the known hazards:
   - `grep -n "—" <file>` — em-dashes in alt text or captions. Re-check that each is a genuine aside.
   - `grep -n " shape\b" <file>` — `shape` as a generic noun.
   - `grep -n "§\|spec/" <file>` — spec section references.
   - For HTML-commented ASCII: scan every line inside `<!-- ... -->` for `-->` followed by non-whitespace; rewrite the arrows.
3. After moves or restructures, run the full SVG re-render pass and re-scan for hazards. Visual regressions from layout shifts are common.

## Escape hatches

- A pictogram inside a diagram (a brand logo, a stylized icon) is exempt from the font and palette rules within its own bounding box.
- Diagrams imported from external standards (OAuth flow, MCP protocol) may preserve their canonical styling when the source is authoritative and the diagram is a faithful reproduction.
- A single em-dash is acceptable as a true aside in a diagram footer. Decorative em-dash beats are not.

## Maintenance

When a new visual failure surfaces in review, add a specific, actionable rule above. Keep the file actionable; do not let it grow into a style thesaurus. When the palette gains a new color, add it to the palette table and use the class name everywhere.
