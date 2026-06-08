// SPDX-License-Identifier: MIT

// Package resourceclass maps a §5.2 named resource class (small, medium,
// large, or a deployer-defined class) to concrete Kubernetes container
// CPU/memory requests and limits. The §4.7 Sandbox-to-Pod reconciler
// resolves a Sandbox's resource class through a Registry and stamps the
// resulting corev1.ResourceRequirements onto every agent container so the
// pod has a per-pod cgroup memory and CPU boundary.
//
// spec: §5.2 line 369 names the small/medium/large classes; §6.4 line 413
// requires that "Resource class definitions must account for tmpfs usage
// in memory requests" — the agent pod's memory-backed /sessions, /tmp, and
// /dev/shm tmpfs volumes charge against the pod memory cgroup, so a class's
// memory limit must leave headroom above the tmpfs reservation or a runaway
// runtime fills the tmpfs and the kernel OOM-kills a container with no
// predictable boundary. The spec pins the class names but no concrete
// CPU/memory quantity, so the quantities are operator-tunable through this
// registry with the documented defaults below.
package resourceclass

import (
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

// TmpfsReservationMiB is the sum of the §6.4 memory-backed tmpfs size caps
// the pod builder applies: /sessions (256Mi) + /tmp (256Mi) + /dev/shm
// (64Mi) = 576Mi. spec: §6.4 lines 413, 420. The credential tmpfs is a
// handful of KiB and is not counted. A class's memory limit must exceed
// this value so the agent process has working memory after tmpfs growth;
// Registry.Validate enforces the invariant.
const TmpfsReservationMiB = 256 + 256 + 64

// DefaultClass is the §5.1 line 357 deployer-safe default resource class a
// Sandbox resolves to when it declares none and its SandboxTemplate
// declares none. It matches the §5.2 line 100 canonical pool example
// (`resourceClass: medium`).
const DefaultClass = "medium"

// Registry resolves a §5.2 resource-class name to container resource
// requirements. The zero value is unusable; construct it with
// DefaultRegistry and apply operator overrides with Set.
type Registry map[string]corev1.ResourceRequirements

// DefaultRegistry returns the built-in §5.2 small/medium/large classes.
// Each class sets the memory request equal to its memory limit so the pod
// carries a predictable OOM boundary (§6.4): the kubelet reserves the full
// memory budget and the cgroup limit caps tmpfs-plus-agent growth at the
// same value. CPU request is below the CPU limit so a pod may burst.
//
// Every class's memory limit clears TmpfsReservationMiB (576Mi) with
// headroom for the agent process, satisfying the §6.4 line 413 tmpfs
// accounting requirement.
func DefaultRegistry() Registry {
	return Registry{
		"small":  requirements("250m", "1Gi", "1", "1Gi"),
		"medium": requirements("500m", "2Gi", "2", "2Gi"),
		"large":  requirements("1", "4Gi", "4", "4Gi"),
	}
}

// requirements builds a corev1.ResourceRequirements from the four
// canonical quantity strings. It panics on a malformed literal, so it is
// only used for the compile-time DefaultRegistry constants.
func requirements(reqCPU, reqMem, limCPU, limMem string) corev1.ResourceRequirements {
	return corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse(reqCPU),
			corev1.ResourceMemory: resource.MustParse(reqMem),
		},
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse(limCPU),
			corev1.ResourceMemory: resource.MustParse(limMem),
		},
	}
}

// Resolve returns a deep copy of the requirements for class, or false when
// the class is not registered. The copy keeps each pod's ResourceList
// independent of the shared registry value.
func (r Registry) Resolve(class string) (corev1.ResourceRequirements, bool) {
	req, ok := r[class]
	if !ok {
		return corev1.ResourceRequirements{}, false
	}
	return *req.DeepCopy(), true
}

// Set installs or replaces the requirements for class. It is the
// operator-override entry point: a controller flag parses a string with
// ParseOverride and calls Set so the deployer can retune a class without a
// code change.
func (r Registry) Set(class string, req corev1.ResourceRequirements) {
	r[class] = req
}

// Validate reports the first class whose memory limit does not exceed the
// §6.4 tmpfs reservation, or whose memory limit is unset. A class that
// fails this check would let tmpfs growth consume the entire memory budget
// and leave the agent process with no headroom, defeating the §6.4
// "predictable OOM boundaries" guarantee. The controller calls Validate at
// startup so a misconfigured override fails fast rather than producing a
// pod that OOM-kills under load.
func (r Registry) Validate() error {
	floor := resource.MustParse(fmt.Sprintf("%dMi", TmpfsReservationMiB))
	for class, req := range r {
		lim, ok := req.Limits[corev1.ResourceMemory]
		if !ok || lim.IsZero() {
			return fmt.Errorf("resource class %q: memory limit is required (must exceed the %dMi tmpfs reservation, §6.4 line 413)", class, TmpfsReservationMiB)
		}
		if lim.Cmp(floor) <= 0 {
			return fmt.Errorf("resource class %q: memory limit %s does not exceed the %dMi tmpfs reservation (§6.4 line 413)", class, lim.String(), TmpfsReservationMiB)
		}
	}
	return nil
}

// ParseOverride parses an operator override of the form
// `name=requests.cpu:250m,requests.memory:512Mi,limits.cpu:1,limits.memory:1Gi`.
// All four quantity keys are required so an override is self-contained
// rather than silently inheriting half of a built-in class. It returns the
// class name and the parsed requirements.
func ParseOverride(s string) (string, corev1.ResourceRequirements, error) {
	name, rest, ok := strings.Cut(s, "=")
	name = strings.TrimSpace(name)
	if !ok || name == "" {
		return "", corev1.ResourceRequirements{}, fmt.Errorf("resource class override %q: expected name=key:qty,... ", s)
	}
	want := map[string]*resource.Quantity{
		"requests.cpu":    nil,
		"requests.memory": nil,
		"limits.cpu":      nil,
		"limits.memory":   nil,
	}
	for _, pair := range strings.Split(rest, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		key, val, ok := strings.Cut(pair, ":")
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		if !ok {
			return "", corev1.ResourceRequirements{}, fmt.Errorf("resource class override %q: field %q is not key:qty", s, pair)
		}
		slot, known := want[key]
		if !known {
			return "", corev1.ResourceRequirements{}, fmt.Errorf("resource class override %q: unknown field %q (want requests.cpu, requests.memory, limits.cpu, limits.memory)", s, key)
		}
		if slot != nil {
			return "", corev1.ResourceRequirements{}, fmt.Errorf("resource class override %q: field %q set twice", s, key)
		}
		q, err := resource.ParseQuantity(val)
		if err != nil {
			return "", corev1.ResourceRequirements{}, fmt.Errorf("resource class override %q: field %q value %q: %w", s, key, val, err)
		}
		want[key] = &q
	}
	for key, q := range want {
		if q == nil {
			return "", corev1.ResourceRequirements{}, fmt.Errorf("resource class override %q: field %q is required", s, key)
		}
	}
	req := corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    *want["requests.cpu"],
			corev1.ResourceMemory: *want["requests.memory"],
		},
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:    *want["limits.cpu"],
			corev1.ResourceMemory: *want["limits.memory"],
		},
	}
	return name, req, nil
}
