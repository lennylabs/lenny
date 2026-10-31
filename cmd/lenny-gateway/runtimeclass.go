// SPDX-License-Identifier: MIT

package main

import "github.com/lennylabs/lenny/pkg/sandbox/isolation"

// runtimeClassOverrides is the §17.5 per-profile RuntimeClass name override
// map the gateway labels runtime_class through. It is built by the same
// isolation.RuntimeClassOverrides helper the controller uses, from flags the
// chart renders from the same `runtimeClasses.profiles.<profile>.name` values, so the
// gateway and the controller map each §5.3 profile to one RuntimeClass name
// (F-16.1.10). The binder, the session server, and the recycle seams all
// read this one map. spec: §17.5, §5.3 (Isolation Profiles), §16.1 (Metrics).
func (w *gatewayWiring) runtimeClassOverrides() map[isolation.Profile]string {
	return isolation.RuntimeClassOverrides(
		*w.f.standardRuntimeClass, *w.f.sandboxedRuntimeClass, *w.f.microvmRuntimeClass,
	)
}
