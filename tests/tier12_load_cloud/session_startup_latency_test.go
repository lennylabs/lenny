// SPDX-License-Identifier: MIT

//go:build load_cloud

// Tier-12 §6.3 startup-latency suite. The cloud tier measures SDK-warm
// claim-to-ready across the three isolation classes (runc, gVisor, Kata)
// in one provisioned cluster, so the per-class SDK-warm numbers are
// directly comparable — the §6.3 line 352 SDK-warm complexity-tradeoff
// validation. Pod-warm measurement stays on tier 7b (runc). This tier
// therefore asserts no §6.3 pod-warm SLO; each arm gates on a healthy run
// (assertScenarioRan) and records the SDK-warm claim-to-ready P95.
//
// The gVisor and Kata arms require their RuntimeClass and node pool to be
// provisioned (scripts/cloud/<provider>/up-runtimeclass-pools.sh). The
// operator names the provisioned classes in LENNY_BENCH_RUNTIME_CLASSES;
// an arm whose class is absent skips with a NEEDS-OPERATOR message rather
// than failing, mirroring the cloud-only skips elsewhere in the suite.

package tier12_load_cloud_test

import (
	"os"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/load"
)

// sdkWarmRuntimeFor returns the §6.1 SDK-warm runtime
// (capabilities.preConnect) and isolation profile for a runtime class.
// Each class has its own Runtime so the Runtime's declared
// isolationProfile matches the pool that serves it: the runc runtime is
// registered by the agent-workload-load fixture, and the gVisor and Kata
// runtimes are registered by up-runtimeclass-pools.sh alongside their
// RuntimeClass and node pool. The create body carries both the runtimeRef
// and the isolationProfile, which together resolve the per-class pool.
func sdkWarmRuntimeFor(runtimeClass string) (runtimeRef, isolationProfile string) {
	switch runtimeClass {
	case "gvisor":
		return "load-preconnect-runtime-gvisor", "sandboxed"
	case "kata":
		return "load-preconnect-runtime-kata", "microvm"
	default:
		return "load-preconnect-runtime", "standard"
	}
}

// benchRuntimeClassEnabled reports whether the operator provisioned the
// given runtime class for this run. LENNY_BENCH_RUNTIME_CLASSES is the
// comma-separated opt-in (default "runc"); scripts/cloud/<provider>/run-load.sh
// sets it to match the node pools up-runtimeclass-pools.sh created.
func benchRuntimeClassEnabled(class string) bool {
	raw := strings.TrimSpace(os.Getenv("LENNY_BENCH_RUNTIME_CLASSES"))
	if raw == "" {
		raw = "runc"
	}
	for _, c := range strings.Split(raw, ",") {
		if strings.TrimSpace(c) == class {
			return true
		}
	}
	return false
}

// runSDKWarmArm drives the SDK-warm startup_latency scenario for one
// runtime class and records its claim-to-ready P95. It gates on the
// cloud-load opt-in and on the class being provisioned.
func runSDKWarmArm(t *testing.T, runtimeClass string) {
	t.Helper()
	_ = requireCloudLoad(t)
	if !benchRuntimeClassEnabled(runtimeClass) {
		t.Skipf("NEEDS-OPERATOR: runtime class %q is not provisioned for this run. "+
			"Provision its RuntimeClass + node pool via "+
			"scripts/cloud/<provider>/up-runtimeclass-pools.sh and add %q to "+
			"LENNY_BENCH_RUNTIME_CLASSES (gVisor needs no hardware virtualization; "+
			"Kata needs nested-virt node pools).", runtimeClass, runtimeClass)
	}
	runtimeRef, isolationProfile := sdkWarmRuntimeFor(runtimeClass)
	s := loadScale(t)
	baseURL := gatewayBaseURL(t)
	before := load.ScrapeStartupBuckets(t, baseURL, runtimeClass)
	res := load.RunScenario(t, "startup_latency", cloudLoadOptions(s, baseURL, map[string]string{
		"LENNY_RUNTIME":           runtimeRef,
		"LENNY_ISOLATION_PROFILE": isolationProfile,
		"LENNY_WARMING_MODEL":     "sdk_warm",
		"LENNY_RUNTIME_CLASS":     runtimeClass,
	}))
	assertScenarioRan(t, "startup_latency_"+runtimeClass+"_sdk_warm", res)
	recordCloudSDKWarmP95(t, baseURL, before, runtimeClass)
}

// recordCloudSDKWarmP95 reads the §6.3 claim-to-ready histogram delta for
// the arm. The gateway runs multiple replicas under load, so a single
// port-forwarded /metrics scrape is a partial view; the authoritative
// per-class aggregation is the cluster Prometheus. This recording is
// therefore best-effort and never fails the arm — assertScenarioRan is
// the gate. A populated window is logged as an observational signal.
func recordCloudSDKWarmP95(t *testing.T, baseURL string, before load.StartupBuckets, runtimeClass string) {
	t.Helper()
	after := load.ScrapeStartupBuckets(t, baseURL, runtimeClass)
	p95, ok := load.P95Delta(before, after)
	if !ok {
		t.Logf("§6.3 startup_latency: runtime_class=%s SDK-warm — no claim-to-ready observations on the scraped gateway replica (multi-replica: read the per-class P95 from cluster Prometheus)", runtimeClass)
		return
	}
	t.Logf("§6.3 startup_latency: runtime_class=%s SDK-warm claim-to-ready P95 %.3fs (single-replica view; cluster Prometheus aggregates all replicas)", runtimeClass, p95)
}

// spec: 6.3 (startup_latency SDK-warm runc — per-class comparison anchor)
// diagnosis: the runc SDK-warm cloud arm regressed or failed. It drives
// POST /v1/sessions/start against the standard-profile SDK-warm pool. A
// failure means the SDK-warm start path errored under the cloud arrival
// rate; this arm is the runc anchor the gVisor and Kata SDK-warm numbers
// are compared against.
func TestCloudStartupLatencySDKWarmRunc(t *testing.T) {
	runSDKWarmArm(t, "runc")
}

// spec: 6.3 (startup_latency SDK-warm gVisor — sandboxed per-class number)
// diagnosis: the gVisor SDK-warm cloud arm regressed, failed, or skipped.
// It drives POST /v1/sessions/start against the sandboxed-profile SDK-warm
// pool. A failure means the SDK-warm start path errored on gVisor; a skip
// means the gvisor RuntimeClass/node pool was not provisioned for the run.
func TestCloudStartupLatencySDKWarmGvisor(t *testing.T) {
	runSDKWarmArm(t, "gvisor")
}

// spec: 6.3 (startup_latency SDK-warm Kata — microvm per-class number)
// diagnosis: the Kata SDK-warm cloud arm regressed, failed, or skipped. It
// drives POST /v1/sessions/start against the microvm-profile SDK-warm
// pool. A failure means the SDK-warm start path errored on Kata; a skip
// means the kata RuntimeClass/node pool (nested virtualization) was not
// provisioned for the run.
func TestCloudStartupLatencySDKWarmKata(t *testing.T) {
	runSDKWarmArm(t, "kata")
}
