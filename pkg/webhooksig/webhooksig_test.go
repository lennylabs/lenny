// SPDX-License-Identifier: MIT

package webhooksig_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/webhooksig"
)

var (
	secret = []byte("subscription-secret")
	body   = []byte(`{"type":"drift_detected","resource":"pool/default"}`)
	at     = time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC)
)

func TestSignThenVerifyRoundTrips(t *testing.T) {
	header := webhooksig.Sign(secret, body, at)
	if err := webhooksig.Verify(body, header, at, secret); err != nil {
		t.Errorf("Verify of a freshly signed delivery = %v, want nil", err)
	}
}

func TestSignProducesTheHeaderFormat(t *testing.T) {
	header := webhooksig.Sign(secret, body, at)
	if !strings.HasPrefix(header, "t=") || !strings.Contains(header, ",v1=") {
		t.Errorf("Sign produced %q, want the t=<unix>,v1=<hex> form", header)
	}
}

func TestVerifyRejectsWrongSecret(t *testing.T) {
	header := webhooksig.Sign(secret, body, at)
	err := webhooksig.Verify(body, header, at, []byte("not-the-secret"))
	if !errors.Is(err, webhooksig.ErrSignatureMismatch) {
		t.Errorf("Verify with the wrong secret = %v, want ErrSignatureMismatch", err)
	}
}

func TestVerifyRejectsTamperedBody(t *testing.T) {
	header := webhooksig.Sign(secret, body, at)
	err := webhooksig.Verify([]byte(`{"type":"tampered"}`), header, at, secret)
	if !errors.Is(err, webhooksig.ErrSignatureMismatch) {
		t.Errorf("Verify with a tampered body = %v, want ErrSignatureMismatch", err)
	}
}

func TestVerifyEnforcesReplayWindow(t *testing.T) {
	header := webhooksig.Sign(secret, body, at)
	// Ten minutes later — beyond the five-minute window.
	if err := webhooksig.Verify(body, header, at.Add(10*time.Minute), secret); !errors.Is(err, webhooksig.ErrReplayWindow) {
		t.Errorf("Verify of a stale delivery = %v, want ErrReplayWindow", err)
	}
	// Ten minutes before signing — a future-dated signature.
	if err := webhooksig.Verify(body, header, at.Add(-10*time.Minute), secret); !errors.Is(err, webhooksig.ErrReplayWindow) {
		t.Errorf("Verify of a future-dated delivery = %v, want ErrReplayWindow", err)
	}
	// Two minutes later — within the window.
	if err := webhooksig.Verify(body, header, at.Add(2*time.Minute), secret); err != nil {
		t.Errorf("Verify within the replay window = %v, want nil", err)
	}
}

func TestVerifyHonorsSecretRotationOverlap(t *testing.T) {
	// A delivery signed with the old secret still validates while both
	// secrets are accepted during the rotation overlap window.
	header := webhooksig.Sign([]byte("old-secret"), body, at)
	err := webhooksig.Verify(body, header, at, []byte("new-secret"), []byte("old-secret"))
	if err != nil {
		t.Errorf("Verify during secret rotation = %v, want nil", err)
	}
}

func TestVerifyRejectsMalformedHeader(t *testing.T) {
	for _, header := range []string{"", "garbage", "t=123", "v1=abcd", "t=,v1=", "noequalssign"} {
		if err := webhooksig.Verify(body, header, at, secret); !errors.Is(err, webhooksig.ErrMalformedHeader) {
			t.Errorf("Verify(%q) = %v, want ErrMalformedHeader", header, err)
		}
	}
}

func TestVerifyRejectsNonNumericTimestamp(t *testing.T) {
	if err := webhooksig.Verify(body, "t=notanumber,v1=abcd", at, secret); !errors.Is(err, webhooksig.ErrMalformedHeader) {
		t.Errorf("Verify with a non-numeric timestamp = %v, want ErrMalformedHeader", err)
	}
}
