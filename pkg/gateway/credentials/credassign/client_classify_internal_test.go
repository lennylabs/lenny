// SPDX-License-Identifier: MIT

package credassign

import (
	"errors"
	"fmt"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lennylabs/lenny/pkg/credential"
)

// breakerCountingCodes is the closed set of gRPC codes §4.3 treats as a
// Token Service or transport fault. Every other code is a healthy
// answer and must not count toward opening the breaker.
var breakerCountingCodes = map[codes.Code]bool{
	codes.Unavailable:      true,
	codes.DeadlineExceeded: true,
	codes.Internal:         true,
	codes.Unknown:          true,
	codes.DataLoss:         true,
	codes.Aborted:          true,
	codes.Canceled:         true,
}

// allGRPCCodes enumerates every gRPC status code, OK through
// Unauthenticated, so a table over it covers the whole input space.
func allGRPCCodes() []codes.Code {
	out := make([]codes.Code, 0, 17)
	for c := codes.OK; c <= codes.Unauthenticated; c++ {
		out = append(out, c)
	}
	return out
}

// TestClassifyTokenServiceErrorEveryCode_spec_4_3 drives every gRPC
// status code through the breaker classifier. A fault code returns the
// error unchanged so the breaker counts it; every other code returns nil
// so the breaker records a success. ResourceExhausted, the Token
// Service's exhausted-pool answer, is the regression case: counting it
// let one exhausted pool open the breaker for every pool.
//
// spec: §4.3 (Token Service, High availability and failure handling),
// §4.9 (Credential Leasing Service).
func TestClassifyTokenServiceErrorEveryCode_spec_4_3(t *testing.T) {
	for _, code := range allGRPCCodes() {
		t.Run(code.String(), func(t *testing.T) {
			var err error
			if code != codes.OK {
				err = status.Error(code, "token service answer")
			}
			got := classifyTokenServiceError(err)
			want := breakerCountingCodes[code]
			if want && got == nil {
				t.Fatalf("code %s: classifier returned nil; a service fault must count against the breaker", code)
			}
			if !want && got != nil {
				t.Fatalf("code %s: classifier returned %v; a healthy answer must not count against the breaker", code, got)
			}
			if want && !errors.Is(got, err) {
				t.Fatalf("code %s: classifier returned %v, want the original error", code, got)
			}
			if countsAgainstBreaker(code) != want {
				t.Fatalf("countsAgainstBreaker(%s) = %v, want %v", code, !want, want)
			}
		})
	}
}

// TestClassifyTokenServiceErrorNonStatusErrors_spec_4_3 covers the
// error inputs that carry no gRPC status: nil is a success, and a plain
// Go error maps to codes.Unknown, which is a fault. A status error
// wrapped by fmt.Errorf keeps its code, so a wrapped ResourceExhausted
// still does not count.
//
// spec: §4.3 (Token Service, High availability and failure handling).
func TestClassifyTokenServiceErrorNonStatusErrors_spec_4_3(t *testing.T) {
	if got := classifyTokenServiceError(nil); got != nil {
		t.Fatalf("nil error classified as %v, want nil", got)
	}
	plain := errors.New("connection reset")
	if got := classifyTokenServiceError(plain); !errors.Is(got, plain) {
		t.Fatalf("plain error classified as %v, want it counted (codes.Unknown)", got)
	}
	wrapped := fmt.Errorf("assign: %w", status.Error(codes.ResourceExhausted, "pool exhausted"))
	if got := classifyTokenServiceError(wrapped); got != nil {
		t.Fatalf("wrapped ResourceExhausted classified as %v, want nil", got)
	}
}

// TestMapAssignErrorEveryCode_spec_4_9 drives every non-OK gRPC code
// through the AssignCredentials error mapping. NotFound maps to
// ErrPoolNotFound, ResourceExhausted maps to credential.ErrPoolExhausted
// (surfaced as CREDENTIAL_POOL_EXHAUSTED), and every other code is
// wrapped with the pool name so the original status stays inspectable.
//
// spec: §4.9 (Credential Leasing Service).
func TestMapAssignErrorEveryCode_spec_4_9(t *testing.T) {
	for _, code := range allGRPCCodes() {
		if code == codes.OK {
			continue
		}
		t.Run(code.String(), func(t *testing.T) {
			src := status.Error(code, "answer")
			got := mapAssignError("pool-a", src)
			switch code {
			case codes.NotFound:
				if !errors.Is(got, ErrPoolNotFound) {
					t.Fatalf("NotFound mapped to %v, want ErrPoolNotFound", got)
				}
			case codes.ResourceExhausted:
				if !errors.Is(got, credential.ErrPoolExhausted) {
					t.Fatalf("ResourceExhausted mapped to %v, want credential.ErrPoolExhausted", got)
				}
			default:
				if status.Code(errors.Unwrap(got)) != code {
					t.Fatalf("code %s mapped to %v, want the status wrapped", code, got)
				}
			}
		})
	}
}
