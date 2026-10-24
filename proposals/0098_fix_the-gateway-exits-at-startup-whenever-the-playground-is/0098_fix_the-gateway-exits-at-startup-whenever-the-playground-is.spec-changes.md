# Spec changes — Gateway exits at startup when the playground is enabled

## Design (as the spec must state it)

§27.8 names the label of `lenny_playground_page_views_total` as `auth_mode`, which conforms to the underscore-case label names §16.1.1 already requires. The label values remain the `playground.authMode` values. No other cell, row, or section changes, and §16.1.1 is not edited (summary D3).

## Edge cases and accepted failure modes

None. The edit changes one label name and no behavior the spec states.

## Staged edits

### SPEC-1 · spec/27_web-playground.md § 27.8 Metrics

Under the heading `### 27.8 Metrics`, replace the row

```
| `lenny_playground_page_views_total` | Counter | `authMode` | Playground index loads |
```

with

```
| `lenny_playground_page_views_total` | Counter | `auth_mode` | Playground index loads |
```

The row stays one physical line, and no other cell in the table changes.

## Spec files touched

- spec/27_web-playground.md
