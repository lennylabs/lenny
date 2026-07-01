// SPDX-License-Identifier: MIT

package leasecontrol

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// makeJWT builds an unsigned three-segment JWT whose payload carries the
// given aud claim (encoded as a JSON value, so the caller controls
// string-vs-array shape). The signature segment is a placeholder; the
// interceptor decodes the payload without verifying the signature.
func makeJWT(t *testing.T, audJSON string) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"aud":` + audJSON + `}`))
	return header + "." + payload + ".sig"
}

func ctxWithBearer(token string) context.Context {
	md := metadata.New(map[string]string{"authorization": "Bearer " + token})
	return metadata.NewIncomingContext(context.Background(), md)
}

func passHandler() (grpc.UnaryHandler, *bool) {
	called := false
	return func(context.Context, any) (any, error) {
		called = true
		return "ok", nil
	}, &called
}

const testAud = "lenny-gateway-acme"

// spec: §10.3 line 334 — an empty configured audience disables the
// check; every call passes through (local-development path).
func TestSATokenInterceptorEmptyAudiencePasses_spec_10_3_334(t *testing.T) {
	h, called := passHandler()
	itc := RequireSATokenInterceptor("", nil)
	if _, err := itc(context.Background(), nil, &grpc.UnaryServerInfo{}, h); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !*called {
		t.Fatal("handler must be invoked when no audience is configured")
	}
}

// spec: §10.3 line 334 — a token whose aud (string form) matches the
// deployment audience is admitted.
func TestSATokenInterceptorStringAudienceMatches_spec_10_3_334(t *testing.T) {
	h, called := passHandler()
	itc := RequireSATokenInterceptor(testAud, nil)
	ctx := ctxWithBearer(makeJWT(t, `"`+testAud+`"`))
	if _, err := itc(ctx, nil, &grpc.UnaryServerInfo{}, h); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !*called {
		t.Fatal("handler must be invoked on a matching audience")
	}
}

// spec: §10.3 line 334 — RFC 7519 allows aud as an array; a match within
// the array is admitted.
func TestSATokenInterceptorArrayAudienceMatches_spec_10_3_334(t *testing.T) {
	h, called := passHandler()
	itc := RequireSATokenInterceptor(testAud, nil)
	ctx := ctxWithBearer(makeJWT(t, `["other",`+`"`+testAud+`"]`))
	if _, err := itc(ctx, nil, &grpc.UnaryServerInfo{}, h); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !*called {
		t.Fatal("handler must be invoked when the array aud contains the deployment audience")
	}
}

// spec: §10.3 line 334 — a token minted for another deployment's gateway
// is rejected (cross-deployment replay protection).
func TestSATokenInterceptorAudienceMismatchRejected_spec_10_3_334(t *testing.T) {
	h, called := passHandler()
	itc := RequireSATokenInterceptor(testAud, nil)
	ctx := ctxWithBearer(makeJWT(t, `"lenny-gateway-globex"`))
	_, err := itc(ctx, nil, &grpc.UnaryServerInfo{}, h)
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected Unauthenticated, got %v", err)
	}
	if *called {
		t.Fatal("handler must not be invoked on an audience mismatch")
	}
}

// spec: §10.3 line 334 — a request with no SA token is rejected when the
// audience check is active.
func TestSATokenInterceptorMissingTokenRejected_spec_10_3_334(t *testing.T) {
	h, called := passHandler()
	itc := RequireSATokenInterceptor(testAud, nil)
	_, err := itc(context.Background(), nil, &grpc.UnaryServerInfo{}, h)
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected Unauthenticated for a missing token, got %v", err)
	}
	if *called {
		t.Fatal("handler must not be invoked when the token is absent")
	}
}

// spec: §10.3 line 334 — a non-JWT bearer value is rejected rather than
// silently admitted.
func TestSATokenInterceptorMalformedTokenRejected_spec_10_3_334(t *testing.T) {
	h, _ := passHandler()
	itc := RequireSATokenInterceptor(testAud, nil)
	ctx := ctxWithBearer("not-a-jwt")
	_, err := itc(ctx, nil, &grpc.UnaryServerInfo{}, h)
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected Unauthenticated for a malformed token, got %v", err)
	}
}

func TestJWTAudiencesParsesBothShapes(t *testing.T) {
	single, err := jwtAudiences(makeJWT(t, `"a"`))
	if err != nil || len(single) != 1 || single[0] != "a" {
		t.Fatalf("string aud: got %v err=%v", single, err)
	}
	many, err := jwtAudiences(makeJWT(t, `["a","b"]`))
	if err != nil || len(many) != 2 {
		t.Fatalf("array aud: got %v err=%v", many, err)
	}
	// An absent aud claim yields no audiences and no error.
	header := base64.RawURLEncoding.EncodeToString([]byte(`{}`))
	payload := base64.RawURLEncoding.EncodeToString(mustJSON(t, map[string]string{"sub": "x"}))
	none, err := jwtAudiences(header + "." + payload + ".sig")
	if err != nil || len(none) != 0 {
		t.Fatalf("absent aud: got %v err=%v", none, err)
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}
