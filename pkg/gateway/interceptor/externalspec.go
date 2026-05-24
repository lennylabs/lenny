// SPDX-License-Identifier: MIT

package interceptor

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ExternalSpec is the parsed form of one `--external-interceptor` flag
// value: the deployer-supplied registration of a §4.8 external
// interceptor service. The gateway dials Endpoint, builds an
// InterceptClient, and registers an External at Phase/Priority. It
// carries the dial-time fields the interceptor package cannot resolve
// on its own (the address and the target phase) so the gateway wiring
// stays in one place.
//
// spec: §4.8 line 1019 — the registration table's required fields are
// name, endpoint, priority, failPolicy, and timeout, plus the phase the
// interceptor targets.
type ExternalSpec struct {
	Name       string
	Endpoint   string
	Phase      Phase
	Priority   int32
	FailPolicy FailPolicy
	Timeout    time.Duration
}

// ParseExternalSpec parses one `--external-interceptor` flag value of
// the form `name=<n>,endpoint=<host:port>,phase=<phase>[,priority=<n>]
// [,failPolicy=fail-open|fail-closed][,timeout=<duration>]`. Whitespace
// around keys and values is trimmed. name, endpoint, and phase are
// required; the rest take the §4.8 registration defaults (priority 500,
// fail-closed, the chain's default timeout) when omitted.
func ParseExternalSpec(s string) (ExternalSpec, error) {
	spec := ExternalSpec{}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		k, v, ok := strings.Cut(part, "=")
		if !ok {
			return ExternalSpec{}, fmt.Errorf("interceptor: malformed --external-interceptor segment %q (want key=value)", part)
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		switch k {
		case "name":
			spec.Name = v
		case "endpoint":
			spec.Endpoint = v
		case "phase":
			spec.Phase = Phase(v)
		case "priority":
			n, err := strconv.ParseInt(v, 10, 32)
			if err != nil {
				return ExternalSpec{}, fmt.Errorf("interceptor: --external-interceptor priority %q: %w", v, err)
			}
			spec.Priority = int32(n)
		case "failPolicy":
			switch FailPolicy(v) {
			case FailOpen, FailClosed:
				spec.FailPolicy = FailPolicy(v)
			default:
				return ExternalSpec{}, fmt.Errorf("interceptor: --external-interceptor failPolicy %q must be fail-open or fail-closed", v)
			}
		case "timeout":
			d, err := time.ParseDuration(v)
			if err != nil {
				return ExternalSpec{}, fmt.Errorf("interceptor: --external-interceptor timeout %q: %w", v, err)
			}
			spec.Timeout = d
		default:
			return ExternalSpec{}, fmt.Errorf("interceptor: unknown --external-interceptor key %q", k)
		}
	}
	if spec.Name == "" {
		return ExternalSpec{}, fmt.Errorf("interceptor: --external-interceptor requires name=")
	}
	if spec.Endpoint == "" {
		return ExternalSpec{}, fmt.Errorf("interceptor: --external-interceptor %q requires endpoint=", spec.Name)
	}
	if spec.Phase == "" {
		return ExternalSpec{}, fmt.Errorf("interceptor: --external-interceptor %q requires phase=", spec.Name)
	}
	if !spec.Phase.IsValid() {
		return ExternalSpec{}, fmt.Errorf("interceptor: --external-interceptor %q phase %q is not a known phase", spec.Name, spec.Phase)
	}
	return spec, nil
}
