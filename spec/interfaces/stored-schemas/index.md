---
code: STOR
type: index
summary: >-
  The stored schemas that must stay compatible across versions, and the compatibility rules for each store.
---

# Stored schemas

The stored schemas that must stay compatible across versions, and the compatibility rules for each store.

## Pages

| Page | Code | Summary |
| :-- | :-- | :-- |
| [Object store layout](object-store-layout.md) | `STOROBJ` | The object key layout and the checkpoint pack format. |
| [Postgres](postgres.md) | `STORPG` | The Postgres tables other versions read: sessions, tasks, pod state, billing, audit, and operations tables. |
| [Redis keys](redis-keys.md) | `STORREDIS` | The Redis key patterns and their tenant prefixing. |
