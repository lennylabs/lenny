// SPDX-License-Identifier: MIT

package gcp

import (
	"context"
	"encoding/pem"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	cloudkms "cloud.google.com/go/kms/apiv1"
	"cloud.google.com/go/kms/apiv1/kmspb"
	"github.com/googleapis/gax-go/v2"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	"github.com/lennylabs/lenny/pkg/kms"
	"github.com/lennylabs/lenny/pkg/kms/kmstest"
)

// signFake scripts the fake client's AsymmetricSign and GetPublicKey
// answers.
type signFake struct {
	der    []byte
	err    error
	pem    string
	alg    kmspb.CryptoKeyVersion_CryptoKeyVersionAlgorithm
	calls  int
	lastIn *kmspb.AsymmetricSignRequest
}

func (f *fakeKMS) AsymmetricSign(ctx context.Context, req *kmspb.AsymmetricSignRequest, _ ...gax.CallOption) (*kmspb.AsymmetricSignResponse, error) {
	f.signing.calls++
	f.signing.lastIn = req
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if f.signing.err != nil {
		return nil, f.signing.err
	}
	return &kmspb.AsymmetricSignResponse{Signature: f.signing.der}, nil
}

func (f *fakeKMS) GetPublicKey(ctx context.Context, _ *kmspb.GetPublicKeyRequest, _ ...gax.CallOption) (*kmspb.PublicKey, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if f.signing.err != nil {
		return nil, f.signing.err
	}
	return &kmspb.PublicKey{Pem: f.signing.pem, Algorithm: f.signing.alg}, nil
}

const signKeyName = "projects/p/locations/global/keyRings/r/cryptoKeys/jwt/cryptoKeyVersions/1"

func newSignProvider(f *fakeKMS) *Provider {
	return newWithClient(f, map[string]string{kms.AliasJWTSigning: signKeyName})
}

func pemOf(der []byte) string {
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
}

// spec: 10.2 (Authentication)
func TestGCPSignLeftPadsShortR_spec_10_2(t *testing.T) {
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
	if f.signing.lastIn.GetName() != signKeyName || string(f.signing.lastIn.GetDigest().GetSha256()) != string(digest) {
		t.Fatalf("AsymmetricSign request = %v, want the mapped version and the SHA-256 digest", f.signing.lastIn)
	}
}

// spec: 10.2 (Authentication)
func TestGCPSignMapsResourceExhaustedToErrThrottled_spec_10_2(t *testing.T) {
	f := newFakeKMS()
	f.signing.err = status.Error(codes.ResourceExhausted, "quota")
	if _, err := newSignProvider(f).Sign(context.Background(), kms.AliasJWTSigning, kmstest.Digest("x")); !errors.Is(err, kms.ErrThrottled) {
		t.Errorf("ResourceExhausted err=%v, want ErrThrottled", err)
	}
	f.signing.err = status.Error(codes.Internal, "boom")
	if _, err := newSignProvider(f).Sign(context.Background(), kms.AliasJWTSigning, kmstest.Digest("x")); err == nil || errors.Is(err, kms.ErrThrottled) {
		t.Errorf("Internal err=%v, want a non-throttle error", err)
	}
	f.signing.err = status.Error(codes.NotFound, "gone")
	if _, err := newSignProvider(f).PublicKey(context.Background(), kms.AliasJWTSigning); !errors.Is(err, kms.ErrUnknownKEK) {
		t.Errorf("NotFound err=%v, want ErrUnknownKEK", err)
	}
}

// spec: 10.2 (Authentication)
func TestGCPSignRejectsBadDigestUnmappedAliasAndCancelledContext_spec_10_2(t *testing.T) {
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
func TestGCPPublicKeyRequiresP256_spec_10_2(t *testing.T) {
	key := kmstest.NewP256Key(t)
	f := newFakeKMS()
	f.signing.pem = pemOf(kmstest.PKIX(t, &key.PublicKey))
	f.signing.alg = kmspb.CryptoKeyVersion_EC_SIGN_P256_SHA256
	pub, err := newSignProvider(f).PublicKey(context.Background(), kms.AliasJWTSigning)
	if err != nil || !pub.Equal(&key.PublicKey) {
		t.Fatalf("P-256 PublicKey: pub=%v err=%v", pub, err)
	}
	f.signing.pem = pemOf(kmstest.P384PKIX(t))
	f.signing.alg = kmspb.CryptoKeyVersion_EC_SIGN_P384_SHA384
	if _, err := newSignProvider(f).PublicKey(context.Background(), kms.AliasJWTSigning); !errors.Is(err, kms.ErrNotP256) {
		t.Errorf("P-384 algorithm err=%v, want ErrNotP256", err)
	}
	f.signing.alg = kmspb.CryptoKeyVersion_EC_SIGN_P256_SHA256
	if _, err := newSignProvider(f).PublicKey(context.Background(), kms.AliasJWTSigning); !errors.Is(err, kms.ErrNotP256) {
		t.Errorf("mislabelled P-384 body err=%v, want ErrNotP256", err)
	}
	f.signing.pem = "not pem"
	if _, err := newSignProvider(f).PublicKey(context.Background(), kms.AliasJWTSigning); err == nil {
		t.Error("a response with no PEM block was accepted")
	}
}

// failingKMSServer answers AsymmetricSign with a fixed gRPC code and
// counts the requests.
type failingKMSServer struct {
	kmspb.UnimplementedKeyManagementServiceServer
	code  codes.Code
	calls atomic.Int32
}

func (s *failingKMSServer) AsymmetricSign(context.Context, *kmspb.AsymmetricSignRequest) (*kmspb.AsymmetricSignResponse, error) {
	s.calls.Add(1)
	return nil, status.Error(s.code, "injected")
}

// realSDKClient starts srv on a loopback listener and returns the real
// Cloud KMS client dialled to it.
func realSDKClient(t *testing.T, srv *failingKMSServer) *cloudkms.KeyManagementClient {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	g := grpc.NewServer()
	kmspb.RegisterKeyManagementServiceServer(g, srv)
	go func() { _ = g.Serve(lis) }()
	t.Cleanup(g.Stop)
	c, err := cloudkms.NewKeyManagementClient(
		context.Background(),
		option.WithEndpoint(lis.Addr().String()),
		option.WithoutAuthentication(),
		option.WithGRPCDialOption(grpc.WithTransportCredentials(insecure.NewCredentials())),
	)
	if err != nil {
		t.Fatalf("NewKeyManagementClient: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// spec: 10.2 (Authentication)
// codes.Unavailable is in the client's default AsymmetricSign retry set,
// so the request count is the discriminating assertion for the disabled
// retry.
func TestGCPSignRealClientMakesOneRequestOnUnavailable_spec_10_2(t *testing.T) {
	srv := &failingKMSServer{code: codes.Unavailable}
	client := realSDKClient(t, srv)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, _ = client.AsymmetricSign(ctx, &kmspb.AsymmetricSignRequest{Name: signKeyName})
	if got := srv.calls.Load(); got < 2 {
		t.Fatalf("control: default client made %d requests, want a retry", got)
	}

	srv.calls.Store(0)
	p := newWithClient(&clientAdapter{inner: client}, map[string]string{kms.AliasJWTSigning: signKeyName})
	if _, err := p.Sign(context.Background(), kms.AliasJWTSigning, kmstest.Digest("x")); err == nil {
		t.Fatal("Sign succeeded against an unavailable server")
	}
	if got := srv.calls.Load(); got != 1 {
		t.Fatalf("Sign made %d provider requests, want exactly 1", got)
	}
}

// spec: 10.2 (Authentication)
// ResourceExhausted crosses the real client's error wrapping and still
// maps to kms.ErrThrottled.
func TestGCPSignRealClientMapsResourceExhausted_spec_10_2(t *testing.T) {
	srv := &failingKMSServer{code: codes.ResourceExhausted}
	p := newWithClient(&clientAdapter{inner: realSDKClient(t, srv)}, map[string]string{kms.AliasJWTSigning: signKeyName})
	if _, err := p.Sign(context.Background(), kms.AliasJWTSigning, kmstest.Digest("x")); !errors.Is(err, kms.ErrThrottled) {
		t.Fatalf("Sign err=%v, want ErrThrottled", err)
	}
	if got := srv.calls.Load(); got != 1 {
		t.Fatalf("Sign made %d provider requests, want exactly 1", got)
	}
}
