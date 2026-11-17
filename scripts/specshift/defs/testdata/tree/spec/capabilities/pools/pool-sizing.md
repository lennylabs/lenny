---
code: PSIZE
type: capability
area: pools
status: partial
summary: Sizes a pool. The page is being written, so its blocks stay off the documentation's reference pages.
---

# Pool sizing

## Terms

| Term | Definition |
| :-- | :-- |
| <a id="surge"></a>Surge | The pods a pool creates above its target during a rollout. |

## Settings

<a id="setting-maxsurge"></a>

```lenny-def
kind: setting
name: maxSurge
type: integer
default: 1
scopes: [pool]
read-by: WPC
summary: The pods a pool creates above its target during a rollout.
```
