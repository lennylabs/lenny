// SPDX-License-Identifier: MIT

package aws

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	awskms "github.com/aws/aws-sdk-go-v2/service/kms"
	"github.com/aws/aws-sdk-go-v2/service/kms/types"
	"github.com/aws/smithy-go"

	"github.com/lennylabs/lenny/pkg/kms"
	"github.com/lennylabs/lenny/pkg/kms/kmstest"
)

// signFake scripts the fake client's Sign and GetPublicKey answers.
type signFake struct {
	der     []byte
	err     error
	pubDER  []byte
	keySpec types.KeySpec
	calls   int
	lastIn  *awskms.SignInput
}

func (f *fakeKMS) Sign(ctx context.Context, in *awskms.SignInput, _ ...func(*awskms.Options)) (*awskms.SignOutput, error) {
	f.signing.calls++
	f.signing.lastIn = in
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if f.signing.err != nil {
		return nil, f.signing.err
	}
	return &awskms.SignOutput{Signature: f.signing.der}, nil
}

func (f *fakeKMS) GetPublicKey(ctx context.Context, _ *awskms.GetPublicKeyInput, _ ...func(*awskms.Options)) (*awskms.GetPublicKeyOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if f.signing.err != nil {
		return nil, f.signing.err
	}
	return &awskms.GetPublicKeyOutput{PublicKey: f.signing.pubDER, KeySpec: f.signing.keySpec}, nil
}

const signKeyID = "1234abcd-12ab-34cd-56ef-1234567890ab"

func newSignProvider(f *fakeKMS) *Provider {
	return newWithClient(f, map[string]string{kms.AliasJWTSigning: signKeyID})
}

// spec: 10.2 (Authentication)
// The discriminating case is a DER signature with a short r: a naive
// copy of the DER integers yields a signature that does not verify.
func TestAWSSignLeftPadsShortR_spec_10_2(t *testing.T) {
	key := kmstest.NewP256Key(t)
	digest := kmstest.Digest("header.payload")
	f := newFakeKMS()
	f.signing.der = kmstest.ShortRSignature(t, key, digest)
	sig, err := newSignProvider(f).Sign(context.Background(), kms.AliasJWTSigning, digest)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if !kmstest.VerifyRS(&key.PublicKey, digest, sig) {
		t.Fatal("r||s signature does not verify")
	}
	in := f.signing.lastIn
	if awssdk.ToString(in.KeyId) != signKeyID || in.MessageType != types.MessageTypeDigest ||
		in.SigningAlgorithm != types.SigningAlgorithmSpecEcdsaSha256 {
		t.Fatalf("Sign input = %+v, want DIGEST / ECDSA_SHA_256 under the mapped key", in)
	}
}

// spec: 10.2 (Authentication)
func TestAWSSignMapsThrottlingToErrThrottled_spec_10_2(t *testing.T) {
	for _, code := range []string{"ThrottlingException", "LimitExceededException"} {
		f := newFakeKMS()
		f.signing.err = &smithy.GenericAPIError{Code: code, Message: "slow down"}
		_, err := newSignProvider(f).Sign(context.Background(), kms.AliasJWTSigning, kmstest.Digest("x"))
		if !errors.Is(err, kms.ErrThrottled) {
			t.Errorf("%s: err=%v, want ErrThrottled", code, err)
		}
	}
	f := newFakeKMS()
	f.signing.err = &smithy.GenericAPIError{Code: "KMSInternalException"}
	if _, err := newSignProvider(f).Sign(context.Background(), kms.AliasJWTSigning, kmstest.Digest("x")); err == nil || errors.Is(err, kms.ErrThrottled) {
		t.Errorf("hard error err=%v, want a non-throttle error", err)
	}
	f.signing.err = &types.NotFoundException{Message: awssdk.String("gone")}
	if _, err := newSignProvider(f).PublicKey(context.Background(), kms.AliasJWTSigning); !errors.Is(err, kms.ErrUnknownKEK) {
		t.Errorf("NotFound err=%v, want ErrUnknownKEK", err)
	}
}

// spec: 10.2 (Authentication)
func TestAWSSignRejectsBadDigestUnmappedAliasAndCancelledContext_spec_10_2(t *testing.T) {
	f := newFakeKMS()
	p := newSignProvider(f)
	if _, err := p.Sign(context.Background(), kms.AliasJWTSigning, make([]byte, 31)); !errors.Is(err, kms.ErrInvalidDigest) {
		t.Errorf("31-byte digest err=%v, want ErrInvalidDigest", err)
	}
	if f.signing.calls != 0 {
		t.Errorf("a rejected digest reached the provider %d times", f.signing.calls)
	}
	if _, err := p.Sign(context.Background(), "platform:other", kmstest.Digest("x")); !errors.Is(err, kms.ErrUnknownKEK) {
		t.Errorf("unmapped alias err=%v, want ErrUnknownKEK", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.Sign(ctx, kms.AliasJWTSigning, kmstest.Digest("x")); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled Sign err=%v, want context.Canceled", err)
	}
	if _, err := p.PublicKey(ctx, kms.AliasJWTSigning); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled PublicKey err=%v, want context.Canceled", err)
	}
}

// spec: 10.2 (Authentication)
func TestAWSPublicKeyRequiresP256_spec_10_2(t *testing.T) {
	key := kmstest.NewP256Key(t)
	f := newFakeKMS()
	f.signing.pubDER = kmstest.PKIX(t, &key.PublicKey)
	f.signing.keySpec = types.KeySpecEccNistP256
	pub, err := newSignProvider(f).PublicKey(context.Background(), kms.AliasJWTSigning)
	if err != nil || !pub.Equal(&key.PublicKey) {
		t.Fatalf("P-256 PublicKey: pub=%v err=%v", pub, err)
	}
	f.signing.pubDER = kmstest.P384PKIX(t)
	f.signing.keySpec = types.KeySpecEccNistP384
	if _, err := newSignProvider(f).PublicKey(context.Background(), kms.AliasJWTSigning); !errors.Is(err, kms.ErrNotP256) {
		t.Errorf("P-384 key spec err=%v, want ErrNotP256", err)
	}
	// A key spec that claims P-256 over a P-384 body is still rejected.
	f.signing.keySpec = types.KeySpecEccNistP256
	if _, err := newSignProvider(f).PublicKey(context.Background(), kms.AliasJWTSigning); !errors.Is(err, kms.ErrNotP256) {
		t.Errorf("mislabelled P-384 body err=%v, want ErrNotP256", err)
	}
}

// throttleServer answers every AWS KMS request with ThrottlingException,
// which the SDK's standard retryer retries, and counts the requests.
func throttleServer(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n.Add(1)
		w.Header().Set("Content-Type", "application/x-amz-json-1.1")
		w.Header().Set("X-Amzn-Errortype", "ThrottlingException")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"__type":"ThrottlingException","message":"Rate exceeded"}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &n
}

// realSDKClient builds the real AWS KMS client against srv with a
// zero-delay retryer so the control case runs fast.
func realSDKClient(srv *httptest.Server) *awskms.Client {
	return awskms.New(awskms.Options{
		Region:       "us-east-1",
		BaseEndpoint: awssdk.String(srv.URL),
		Credentials:  awssdk.AnonymousCredentials{},
		Retryer: retry.NewStandard(func(o *retry.StandardOptions) {
			o.Backoff = retry.BackoffDelayerFunc(func(int, error) (time.Duration, error) { return 0, nil })
			o.RateLimiter = noRateLimit{}
		}),
	})
}

// noRateLimit disables the retry token bucket so the control case is
// not capped by retry quota.
type noRateLimit struct{}

func (noRateLimit) GetToken(context.Context, uint) (func() error, error) {
	return func() error { return nil }, nil
}
func (noRateLimit) AddTokens(uint) error { return nil }

// spec: 10.2 (Authentication)
// Sign through the real SDK client must reach the provider once: the
// fake-client tests bypass the SDK retry middleware, so the request
// count against a local server is the discriminating assertion.
func TestAWSSignRealClientMakesOneRequestOnThrottle_spec_10_2(t *testing.T) {
	srv, n := throttleServer(t)
	client := realSDKClient(srv)

	// Control: the same client without the per-call override retries.
	_, _ = client.Sign(context.Background(), &awskms.SignInput{
		KeyId: awssdk.String(signKeyID), Message: kmstest.Digest("x"),
		MessageType: types.MessageTypeDigest, SigningAlgorithm: types.SigningAlgorithmSpecEcdsaSha256,
	})
	if got := n.Load(); got < 2 {
		t.Fatalf("control: default client made %d requests, want a retry", got)
	}

	n.Store(0)
	p := newWithClient(client, map[string]string{kms.AliasJWTSigning: signKeyID})
	_, err := p.Sign(context.Background(), kms.AliasJWTSigning, kmstest.Digest("x"))
	if !errors.Is(err, kms.ErrThrottled) {
		t.Fatalf("Sign err=%v, want ErrThrottled", err)
	}
	if got := n.Load(); got != 1 {
		t.Fatalf("Sign made %d provider requests, want exactly 1", got)
	}
}
