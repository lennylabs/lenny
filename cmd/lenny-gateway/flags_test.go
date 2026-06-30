// SPDX-License-Identifier: MIT

package main

import (
	"reflect"
	"testing"
)

// spec: 4.1 (gateway subsystem seams)
//
// TestParseFlagsPopulatesEveryField pins the R1 composition-root
// decomposition: parseFlags splits its flag definitions across the
// per-domain register helpers (registerCoreFlags, registerStorageFlags,
// registerPolicyFlags, registerSessionFlags, registerArtifactFlags, and
// registerOpsFlags). If a future edit drops a flag from a helper, or omits
// a helper from parseFlags' call sequence, the corresponding gatewayFlags
// field stays at its zero value and this test fails. Every flag-pointer
// field (and the kmsOpts/kmsFinalize selector) must be non-nil after
// parseFlags returns, so the build helpers under runGateway never
// dereference a flag the composition root forgot to register.
//
// parseFlags mutates the process-global flag.CommandLine, so this test
// calls it exactly once; no other test in this package defines flags on the
// global set, so the single call cannot trigger a flag-redefinition panic.
func TestParseFlagsPopulatesEveryField(t *testing.T) {
	f := parseFlags()
	if f == nil {
		t.Fatal("parseFlags returned nil")
	}

	v := reflect.ValueOf(f).Elem()
	tp := v.Type()
	for i := 0; i < v.NumField(); i++ {
		field := v.Field(i)
		name := tp.Field(i).Name
		switch field.Kind() {
		case reflect.Pointer, reflect.Func:
			// Every flag.<T> pointer and the kmsFinalize closure must be set.
			if field.IsNil() {
				t.Errorf("gatewayFlags.%s is nil after parseFlags: a register helper "+
					"dropped its flag, or parseFlags omitted the helper", name)
			}
		default:
			// Non-pointer fields are the secret byte slices and the
			// externalInterceptors slice, which are legitimately empty when
			// no value is supplied. They are exercised by the wiring tests
			// rather than asserted here.
		}
	}
}
