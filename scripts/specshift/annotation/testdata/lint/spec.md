<!-- SPDX-License-Identifier: MIT -->

# Pod claims

## 4.7 Claim hold and release rules

**Reserved hold.** A recycled pod's claim stays reserved for the tenant's next session until the hold expires, and the gateway refuses a claim from any other tenant while the hold is in force. See [the pool page](pools.md#pool-r5) for the pool rules.
