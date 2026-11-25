// SPDX-License-Identifier: MIT

package uploadtoken_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/uploadtoken"
)

// redactFixture is a ring with one key, an issuer and a verifier over it,
// and a redactor over the same ring.
type redactFixture struct {
	ring     *uploadtoken.KeyRing
	issuer   *uploadtoken.Issuer
	verifier *uploadtoken.Verifier
	redactor *uploadtoken.Redactor
}

func newRedactFixture(t *testing.T) redactFixture {
	t.Helper()
	ring := uploadtoken.NewKeyRing(uploadtoken.SigningKey{KeyID: "k1", Secret: []byte("secret-1")})
	fixed := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	clock := func() time.Time { return fixed }
	return redactFixture{
		ring:     ring,
		issuer:   uploadtoken.NewIssuer(ring, clock),
		verifier: uploadtoken.NewVerifier(ring, nil, clock),
		redactor: uploadtoken.NewRedactor(ring),
	}
}

func (f redactFixture) mint(t *testing.T, sessionID string) string {
	t.Helper()
	tok, err := f.issuer.Issue(sessionID, time.Minute)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	return tok
}

const testSessionID = "0f7c2a5e-9d41-4c3b-8a2f-6b1e0d9c7a35"

// TestStripRemovesTheTokenAndRemintRestoresTheOriginalBytes checks that a
// stored create response holds no token, that the reference does not verify
// as a token, and that the replayed body equals the original byte for byte.
//
// spec: §11.5 (Idempotency), §7.1 (Normal Flow)
func TestStripRemovesTheTokenAndRemintRestoresTheOriginalBytes(t *testing.T) {
	f := newRedactFixture(t)
	tok := f.mint(t, testSessionID)
	body := []byte(fmt.Sprintf(`{"id":%q,"state":"created","uploadToken":%q}`, testSessionID, tok))

	stored := f.redactor.Strip(body)
	if strings.Contains(string(stored), tok) {
		t.Fatalf("stored body still holds the token: %s", stored)
	}
	signature := tok[strings.LastIndex(tok, ".")+1:]
	if strings.Contains(string(stored), signature) {
		t.Fatalf("stored body still holds the token's signature: %s", stored)
	}
	var parsed struct{ UploadToken string }
	if err := json.Unmarshal(stored, &parsed); err != nil {
		t.Fatalf("stored body is not JSON: %v", err)
	}
	if !strings.HasPrefix(parsed.UploadToken, testSessionID+".") || !strings.Contains(parsed.UploadToken, ".redacted.") {
		t.Fatalf("reference = %q, want <session>.<expiry>.redacted.<mark>", parsed.UploadToken)
	}
	if _, err := f.verifier.Verify(parsed.UploadToken, testSessionID); !errors.Is(err, uploadtoken.ErrInvalid) {
		t.Fatalf("Verify(reference) = %v, want ErrInvalid", err)
	}

	if got := f.redactor.Remint(stored); string(got) != string(body) {
		t.Fatalf("Remint = %s, want the original %s", got, body)
	}
}

// TestStripAndRemintReachATokenInsideJSONEncodedText covers the MCP paths,
// where the token sits in a tool result's text content, and in that text
// inside a JSON-RPC response.
//
// spec: §11.5 (Idempotency)
func TestStripAndRemintReachATokenInsideJSONEncodedText(t *testing.T) {
	f := newRedactFixture(t)
	tok := f.mint(t, testSessionID)
	inner, _ := json.Marshal(map[string]string{"sessionId": testSessionID, "state": "created", "uploadToken": tok})
	toolResult, _ := json.Marshal(map[string]any{"content": []map[string]string{{"type": "text", "text": string(inner)}}})
	rpc, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "result": json.RawMessage(toolResult)})

	for name, body := range map[string][]byte{"tool result": toolResult, "JSON-RPC response": rpc} {
		t.Run(name, func(t *testing.T) {
			stored := f.redactor.Strip(body)
			if strings.Contains(string(stored), tok) {
				t.Fatalf("stored body still holds the token: %s", stored)
			}
			if !json.Valid(stored) {
				t.Fatalf("stored body is not valid JSON: %s", stored)
			}
			if got := f.redactor.Remint(stored); string(got) != string(body) {
				t.Fatalf("Remint = %s, want the original %s", got, body)
			}
		})
	}
}

// TestRemintUsesTheOverlapKeyThatSignedTheToken checks that a replay after a
// rotation restores the token the displaced key signed, which still
// verifies inside the overlap window, rather than a token under the new key.
//
// spec: §11.5 (Idempotency), §7.1 (Normal Flow)
func TestRemintUsesTheOverlapKeyThatSignedTheToken(t *testing.T) {
	f := newRedactFixture(t)
	tok := f.mint(t, testSessionID)
	stored := f.redactor.Strip([]byte(tok))

	f.ring.Rotate(uploadtoken.SigningKey{KeyID: "k2", Secret: []byte("secret-2")})
	got := f.redactor.Remint(stored)
	if string(got) != tok {
		t.Fatalf("Remint after rotation = %q, want the original %q", got, tok)
	}
	if _, err := f.verifier.Verify(string(got), testSessionID); err != nil {
		t.Fatalf("Verify(reminted) = %v", err)
	}
}

// TestRemintLeavesTheReferenceWhenTheSigningKeyLeftTheRing checks that a
// replay whose signing key has expired from the ring returns the reference,
// which does not verify, as the original token would not.
//
// spec: §11.5 (Idempotency)
func TestRemintLeavesTheReferenceWhenTheSigningKeyLeftTheRing(t *testing.T) {
	f := newRedactFixture(t)
	tok := f.mint(t, testSessionID)
	stored := f.redactor.Strip([]byte(tok))

	f.ring.Rotate(uploadtoken.SigningKey{KeyID: "k2", Secret: []byte("secret-2")})
	f.ring.Expire("k1")
	if _, err := f.verifier.Verify(tok, testSessionID); !errors.Is(err, uploadtoken.ErrInvalid) {
		t.Fatalf("precondition: original token Verify = %v, want ErrInvalid", err)
	}
	if got := f.redactor.Remint(stored); string(got) != string(stored) {
		t.Fatalf("Remint = %q, want the reference %q unchanged", got, stored)
	}
}

// TestRemintRefusesAReferenceForgedFromTheTokenDigest checks that a reader
// of the token's SHA-256 digest, which the session row and the finalize
// audit event carry, cannot write a reference that a replay turns into the
// token.
//
// spec: §11.5 (Idempotency), §7.1 (Normal Flow)
func TestRemintRefusesAReferenceForgedFromTheTokenDigest(t *testing.T) {
	f := newRedactFixture(t)
	tok := f.mint(t, testSessionID)
	sum := sha256.Sum256([]byte(tok))
	prefix := tok[:strings.LastIndex(tok, ".")]
	forged := []byte(`{"metadata":{"note":"` + prefix + ".redacted." + hex.EncodeToString(sum[:]) + `"}}`)

	if got := f.redactor.Remint(forged); strings.Contains(string(got), tok) {
		t.Fatalf("Remint turned a forged reference into the token: %s", got)
	}
}

// TestRemintRefusesAReferenceCopiedIntoAnotherRequest checks that a
// reader of the idempotency table who copies a stored reference into the
// data of a request of its own, which the response echoes, cannot turn the
// replay of that request into the token.
//
// spec: §11.5 (Idempotency), §7.1 (Normal Flow)
func TestRemintRefusesAReferenceCopiedIntoAnotherRequest(t *testing.T) {
	f := newRedactFixture(t)
	victim := f.mint(t, testSessionID)
	storedVictim := f.redactor.Strip([]byte(victim))

	own := f.mint(t, "1b2c3d4e-0000-4000-8000-000000000001")
	echo := []byte(`{"id":"1b2c3d4e-0000-4000-8000-000000000001","uploadToken":"` + own +
		`","env":{"X":"` + string(storedVictim) + `"}}`)
	stored := f.redactor.Strip(echo)
	replayed := f.redactor.Remint(stored)
	if strings.Contains(string(replayed), victim) {
		t.Fatalf("replay turned a copied reference into the victim's token: %s", replayed)
	}
	if !strings.Contains(string(replayed), own) {
		t.Fatalf("replay lost the response's own token: %s", replayed)
	}
}

// TestStripRemovesATokenShapedValueThatVerifiesUnderNoKey checks that a
// value with the token's format that this ring did not sign is not stored
// either, and that no replay turns its reference back into a token.
//
// spec: §11.5 (Idempotency)
func TestStripRemovesATokenShapedValueThatVerifiesUnderNoKey(t *testing.T) {
	f := newRedactFixture(t)
	other := uploadtoken.NewIssuer(uploadtoken.NewKeyRing(uploadtoken.SigningKey{KeyID: "x", Secret: []byte("other")}), nil)
	foreign, err := other.Issue(testSessionID, time.Minute)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	stored := f.redactor.Strip([]byte(foreign))
	if strings.Contains(string(stored), foreign) {
		t.Fatalf("stored body still holds the foreign token: %s", stored)
	}
	if got := f.redactor.Remint(stored); string(got) != string(stored) {
		t.Fatalf("Remint = %q, want the reference unchanged", got)
	}
}

// TestStripAndRemintLeaveOtherBytesAlone checks that a body with no token
// and no reference passes through both, that Remint leaves a reference whose
// expiry is not in canonical form, and that Strip gives a reference it did
// not write the mark no key reproduces.
//
// spec: §11.5 (Idempotency)
func TestStripAndRemintLeaveOtherBytesAlone(t *testing.T) {
	f := newRedactFixture(t)
	for _, body := range []string{"", `{"id":"abc","state":"ready"}`} {
		if got := f.redactor.Strip([]byte(body)); string(got) != body {
			t.Errorf("Strip(%q) = %q, want unchanged", body, got)
		}
		if got := f.redactor.Remint([]byte(body)); string(got) != body {
			t.Errorf("Remint(%q) = %q, want unchanged", body, got)
		}
	}
	for _, body := range []string{
		testSessionID + ".0100.redacted." + strings.Repeat("a", 64),
		testSessionID + ".99999999999999999999.redacted." + strings.Repeat("a", 64),
	} {
		if got := f.redactor.Remint([]byte(body)); string(got) != body {
			t.Errorf("Remint(%q) = %q, want unchanged", body, got)
		}
	}
	foreign := testSessionID + ".1767225660.redacted." + strings.Repeat("a", 64)
	want := testSessionID + ".1767225660.redacted." + strings.Repeat("0", 64)
	if got := f.redactor.Strip([]byte(foreign)); string(got) != want {
		t.Errorf("Strip(%q) = %q, want %q", foreign, got, want)
	}
}

// TestARedactorWithNoRingFailsClosed checks that a Redactor wired with no
// ring still removes every token and restores none.
//
// spec: §11.5 (Idempotency)
func TestARedactorWithNoRingFailsClosed(t *testing.T) {
	f := newRedactFixture(t)
	tok := f.mint(t, testSessionID)
	var nilRedactor *uploadtoken.Redactor
	for name, r := range map[string]*uploadtoken.Redactor{"nil": nilRedactor, "no ring": uploadtoken.NewRedactor(nil)} {
		stored := r.Strip([]byte(tok))
		if strings.Contains(string(stored), tok) {
			t.Errorf("%s: Strip = %q, still holds the token", name, stored)
		}
		if got := r.Remint(stored); string(got) != string(stored) {
			t.Errorf("%s: Remint = %q, want the reference unchanged", name, got)
		}
	}
}

// TestOnlyTheCanonicalSpellingOfATokenVerifies checks that another spelling
// of a valid token, which would carry another digest and escape both the
// consumed tracker and the redactor, does not verify.
//
// spec: §7.1 (Normal Flow)
func TestOnlyTheCanonicalSpellingOfATokenVerifies(t *testing.T) {
	f := newRedactFixture(t)
	tok := f.mint(t, testSessionID)
	parts := strings.Split(tok, ".")
	for name, spelling := range map[string]string{
		"uppercase hex":  parts[0] + "." + parts[1] + "." + strings.ToUpper(parts[2]),
		"signed expiry":  parts[0] + ".+" + parts[1] + "." + parts[2],
		"padded expiry":  parts[0] + ".0" + parts[1] + "." + parts[2],
		"padded past 19": parts[0] + "." + strings.Repeat("0", 12) + parts[1] + "." + parts[2],
	} {
		if _, err := f.verifier.Verify(spelling, testSessionID); !errors.Is(err, uploadtoken.ErrInvalid) {
			t.Errorf("%s: Verify(%q) = %v, want ErrInvalid", name, spelling, err)
		}
	}
	if _, err := f.verifier.Verify(tok, testSessionID); err != nil {
		t.Fatalf("canonical token: Verify = %v", err)
	}
}

// TestStripAndRemintRunConcurrentlyWithRotation runs strip and remint
// against a ring that rotates underneath them; under -race it checks that
// the redactor reads the ring safely, and in every run that a token signed
// before the rotations still round-trips while its key is in the ring.
//
// spec: §11.5 (Idempotency)
func TestStripAndRemintRunConcurrentlyWithRotation(t *testing.T) {
	f := newRedactFixture(t)
	tok := f.mint(t, testSessionID)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				if got := f.redactor.Remint(f.redactor.Strip([]byte(tok))); string(got) != tok {
					t.Errorf("round trip = %q, want %q", got, tok)
					return
				}
			}
		}()
	}
	for i := 0; i < 20; i++ {
		f.ring.Rotate(uploadtoken.SigningKey{KeyID: fmt.Sprintf("r%d", i), Secret: []byte(fmt.Sprintf("s%d", i))})
	}
	wg.Wait()
}
