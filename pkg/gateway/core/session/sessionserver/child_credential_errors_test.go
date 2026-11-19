// SPDX-License-Identifier: MIT

package sessionserver

import (
	"errors"
	"fmt"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/credentials/credassign"
	"github.com/lennylabs/lenny/pkg/gateway/llmproxy/credrouter"
	"github.com/lennylabs/lenny/pkg/gateway/seam"
)

// TestChildCredentialErrorAddsTheSeamSentinel checks that each credential
// cause of a materialization failure also matches its seam sentinel with
// its message and chain unchanged, and that any other error, nil included,
// is returned as it came.
// spec: GWSEAM-MaterializeChild
func TestChildCredentialErrorAddsTheSeamSentinel(t *testing.T) {
	for _, tc := range []struct {
		name     string
		cause    error
		sentinel error
	}{
		{"pool exhausted", credrouter.ErrNoCredentialAvailable, seam.ErrChildCredentialUnavailable},
		{"user credential not found", credrouter.ErrUserCredentialNotFound, seam.ErrChildUserCredentialNotFound},
		{"token service unavailable", credassign.ErrTokenServiceUnavailable, seam.ErrTokenServiceUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := fmt.Errorf("materialize child sess_child: %w", tc.cause)
			got := childCredentialError(err)
			if !errors.Is(got, tc.sentinel) || !errors.Is(got, tc.cause) {
				t.Fatalf("%v matches neither the seam sentinel nor its cause", got)
			}
			if got.Error() != err.Error() {
				t.Errorf("message = %q, want %q", got.Error(), err.Error())
			}
		})
	}
	other := errors.New("claim failed")
	if got := childCredentialError(other); got != other {
		t.Errorf("an unrelated error = %v, want it unchanged", got)
	}
	if got := childCredentialError(nil); got != nil {
		t.Errorf("nil = %v, want nil", got)
	}
}
