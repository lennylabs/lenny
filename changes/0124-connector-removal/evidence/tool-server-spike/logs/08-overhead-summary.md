## Overhead benchmark (n=200 per run, 3 runs per cell; median of the per-run p50/p95)
| client | path/era/tool | p50 ms | p95 ms | runs |
|---|---|---|---|---|
| py-mcp-2.3.0 | direct/modern/fast | 0.851 | 1.137 | 3 |
| py-mcp-2.3.0 | direct/modern/fast_progress | 2.935 | 3.501 | 3 |
| py-mcp-2.3.0 | inspect/modern/fast | 1.134 | 1.463 | 3 |
| py-mcp-2.3.0 | inspect/modern/fast_progress | 2.746 | 3.252 | 3 |
| py-mcp-2.3.0 | passthru/modern/fast | 1.097 | 1.374 | 3 |
| py-mcp-2.3.0 | passthru/modern/fast_progress | 2.645 | 3.130 | 3 |
| ts-2.4.0 | direct/legacy/fast | 2.110 | 2.487 | 3 |
| ts-2.4.0 | direct/modern/fast | 0.623 | 0.889 | 3 |
| ts-2.4.0 | direct/modern/fast_progress | 0.748 | 1.310 | 3 |
| ts-2.4.0 | inspect/legacy/fast | 2.243 | 2.555 | 3 |
| ts-2.4.0 | inspect/modern/fast | 0.755 | 1.200 | 3 |
| ts-2.4.0 | inspect/modern/fast_progress | 0.952 | 1.299 | 3 |
| ts-2.4.0 | passthru/legacy/fast | 2.232 | 2.668 | 3 |
| ts-2.4.0 | passthru/modern/fast | 0.849 | 1.199 | 3 |
| ts-2.4.0 | passthru/modern/fast_progress | 1.001 | 1.560 | 3 |

### Added latency vs direct (median p50 / p95 deltas)
| client | era/tool | passthru Δp50 | passthru Δp95 | inspect Δp50 | inspect Δp95 | inspect-vs-passthru Δp50 |
|---|---|---|---|---|---|---|
| py-mcp-2.3.0 | modern/fast | +0.246 | +0.237 | +0.283 | +0.326 | +0.037 |
| py-mcp-2.3.0 | modern/fast_progress | -0.290 | -0.371 | -0.189 | -0.249 | +0.101 |
| ts-2.4.0 | legacy/fast | +0.122 | +0.181 | +0.133 | +0.068 | +0.011 |
| ts-2.4.0 | modern/fast | +0.226 | +0.310 | +0.132 | +0.311 | -0.094 |
| ts-2.4.0 | modern/fast_progress | +0.253 | +0.250 | +0.204 | -0.011 | -0.049 |
