## 18. Build Sequence

The implementation phasing for the platform components described in this specification has not been worked out yet. A future revision of this section will describe the order in which the gateway, controllers, stores, runtime adapter, and supporting subsystems are built, along with the gating decisions and milestone criteria for each phase.

The phasing decision is independent of the test-infrastructure phasing. The test infrastructure is built first, against the contracts in this spec, and its own build sequence lives in [`TESTING.md`](https://github.com/lennylabs/lenny/blob/main/TESTING.md) §13. When the implementation phasing is decided, it will be added here and cross-referenced with the test-infrastructure phases that gate each implementation milestone.
