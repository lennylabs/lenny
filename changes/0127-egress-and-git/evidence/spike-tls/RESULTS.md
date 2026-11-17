# Server TLS on I2, I5, and I7 (2026-11-02)
Compared plaintext and TLS on the same harness. Laptop, Docker, arm64. Raw output: a-*.out (feature matrix), b1 to b7.out.
Stand-ins: netagent (go/nas), mirror (go/mir), I7 (go/i7, gRPC health Watch), decision service (earlier authz). Go netagent does not exist yet.
Summary figures are in the design under "Transport security". Harness notes: plaintext-only dial errors at 2000/s and in one 60 s run did not recur on a rerun after idle and are not TLS results. Docker stats CPU samples on the bulk runs were unreliable, so bulk CPU uses cgroup deltas. The first I7 restart run did not kill the server; b7 replaced it.
b9.out: I3 mTLS and the readiness listener. In its readiness section the client column reads 503 for valid states because that Envoy config used plaintext I3 against a TLS decision service; the test there is the probe column.
