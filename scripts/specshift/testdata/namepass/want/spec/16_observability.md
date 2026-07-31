## 16.4 Metrics

The mTLS handshake latency histogram is labeled by `direction`, whose values are
`gateway_to_pod` and `pod_to_gateway`. It measures the handshake on the LNK-POD-GRPC and CH-LLMPROXY links
the NetworkPolicies define, and both directions are instrumented because each
side initiates a handshake on a distinct path.
