// SPDX-License-Identifier: MIT

package kms

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	pkgkms "github.com/lennylabs/lenny/pkg/kms"
)

// AWSServer is a local HTTP server that answers the AWS KMS JSON protocol
// for one asymmetric P-256 signing key and one symmetric key, so a real
// cmd/lenny-gateway or cmd/lenny-token-service binary started with
// --kms-provider=aws signs through it. SetFailing makes every request
// fail hard with KMSInternalException, which models a KMS outage the
// §10.2 signing breaker counts.
//
// It answers GetPublicKey, Sign, Encrypt, Decrypt, and DescribeKey. Every
// other operation is refused with UnsupportedOperationException.
type AWSServer struct {
	srv     *httptest.Server
	key     *ecdsa.PrivateKey
	aead    cipher.AEAD
	failing atomic.Bool
	signs   atomic.Int64
}

// awsKeyID is the immutable key-ID reference the server answers for. A
// bare key ID passes the §10.2 reference-immutability check on AWS.
const awsKeyID = "1234abcd-12ab-34cd-56ef-1234567890ab"

// NewAWSServer starts the server with a fresh signing key. The cleanup of
// t closes it.
func NewAWSServer(t testing.TB) *AWSServer {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("stub kms: generate signing key: %v", err)
	}
	sym := make([]byte, 32)
	if _, err := rand.Read(sym); err != nil {
		t.Fatalf("stub kms: generate symmetric key: %v", err)
	}
	block, err := aes.NewCipher(sym)
	if err != nil {
		t.Fatalf("stub kms: aes: %v", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatalf("stub kms: gcm: %v", err)
	}
	s := &AWSServer{key: key, aead: aead}
	s.srv = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.srv.Close)
	return s
}

// SetFailing makes every request fail hard while v is true.
func (s *AWSServer) SetFailing(v bool) { s.failing.Store(v) }

// SignRequests reports how many Sign requests reached the server,
// including the ones it failed.
func (s *AWSServer) SignRequests() int64 { return s.signs.Load() }

// PublicKey returns the signing key's public half.
func (s *AWSServer) PublicKey() *ecdsa.PublicKey { return &s.key.PublicKey }

// KeyID returns the key reference to map platform:jwt-signing to.
func (s *AWSServer) KeyID() string { return awsKeyID }

// Args returns the flags that select the AWS provider and map
// platform:jwt-signing, and the tenant:<id> KEK alias of each tenant in
// tenants, to the server's key. The aliases go in one --kms-alias flag,
// because a repeated flag keeps only its last value.
func (s *AWSServer) Args(tenants ...string) []string {
	pairs := []string{pkgkms.AliasJWTSigning + "=" + awsKeyID}
	for _, tenant := range tenants {
		pairs = append(pairs, "tenant:"+tenant+"="+awsKeyID)
	}
	return []string{
		"--kms-provider=aws",
		"--kms-aws-region=us-east-1",
		"--kms-alias=" + strings.Join(pairs, ","),
	}
}

// Env returns the environment that points the AWS SDK at the server with
// static dummy credentials. Appended after the process's own
// environment, each entry overrides a developer setting of the same name.
func (s *AWSServer) Env() []string {
	return []string{
		"AWS_ACCESS_KEY_ID=stub-access-key",
		"AWS_SECRET_ACCESS_KEY=stub-secret-key",
		"AWS_SESSION_TOKEN=",
		"AWS_PROFILE=",
		"AWS_REGION=us-east-1",
		"AWS_EC2_METADATA_DISABLED=true",
		"AWS_ENDPOINT_URL_KMS=" + s.srv.URL,
	}
}

// serve dispatches one AWS KMS request on its X-Amz-Target header.
func (s *AWSServer) serve(w http.ResponseWriter, r *http.Request) {
	op := strings.TrimPrefix(r.Header.Get("X-Amz-Target"), "TrentService.")
	if op == "Sign" {
		s.signs.Add(1)
	}
	if s.failing.Load() {
		writeAWSError(w, http.StatusInternalServerError, "KMSInternalException", "stub KMS outage")
		return
	}
	var in map[string]any
	raw, err := io.ReadAll(r.Body)
	if err == nil && len(raw) > 0 {
		err = json.Unmarshal(raw, &in)
	}
	if err != nil {
		writeAWSError(w, http.StatusBadRequest, "ValidationException", err.Error())
		return
	}
	out, status, code, msg := s.dispatch(op, in)
	if status != http.StatusOK {
		writeAWSError(w, status, code, msg)
		return
	}
	w.Header().Set("Content-Type", "application/x-amz-json-1.1")
	_ = json.NewEncoder(w).Encode(out)
}

// dispatch runs op. It returns the response body, or a status, error
// type, and message when the operation fails.
func (s *AWSServer) dispatch(op string, in map[string]any) (map[string]any, int, string, string) {
	switch op {
	case "GetPublicKey":
		der, err := x509.MarshalPKIXPublicKey(&s.key.PublicKey)
		if err != nil {
			return nil, http.StatusInternalServerError, "KMSInternalException", err.Error()
		}
		return map[string]any{
			"KeyId": awsKeyID, "PublicKey": der, "KeySpec": "ECC_NIST_P256",
			"KeyUsage": "SIGN_VERIFY", "SigningAlgorithms": []string{"ECDSA_SHA_256"},
		}, http.StatusOK, "", ""
	case "Sign":
		return s.sign(in)
	case "Encrypt":
		return s.encrypt(in)
	case "Decrypt":
		return s.decrypt(in)
	case "DescribeKey":
		return map[string]any{"KeyMetadata": map[string]any{
			"KeyId": awsKeyID, "Enabled": true, "KeyState": "Enabled",
		}}, http.StatusOK, "", ""
	default:
		return nil, http.StatusBadRequest, "UnsupportedOperationException", fmt.Sprintf("stub KMS does not answer %q", op)
	}
}

// sign answers Sign with an ASN.1 DER ECDSA signature over the digest.
func (s *AWSServer) sign(in map[string]any) (map[string]any, int, string, string) {
	digest, err := blobField(in, "Message")
	if err != nil {
		return nil, http.StatusBadRequest, "ValidationException", err.Error()
	}
	sig, err := ecdsa.SignASN1(rand.Reader, s.key, digest)
	if err != nil {
		return nil, http.StatusInternalServerError, "KMSInternalException", err.Error()
	}
	return map[string]any{"KeyId": awsKeyID, "Signature": sig, "SigningAlgorithm": "ECDSA_SHA_256"}, http.StatusOK, "", ""
}

// encrypt answers Encrypt by sealing the plaintext with AES-GCM.
func (s *AWSServer) encrypt(in map[string]any) (map[string]any, int, string, string) {
	pt, err := blobField(in, "Plaintext")
	if err != nil {
		return nil, http.StatusBadRequest, "ValidationException", err.Error()
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, http.StatusInternalServerError, "KMSInternalException", err.Error()
	}
	ct := s.aead.Seal(nonce, nonce, pt, nil)
	return map[string]any{"KeyId": awsKeyID, "CiphertextBlob": ct, "EncryptionAlgorithm": "SYMMETRIC_DEFAULT"}, http.StatusOK, "", ""
}

// decrypt answers Decrypt by opening a blob encrypt sealed.
func (s *AWSServer) decrypt(in map[string]any) (map[string]any, int, string, string) {
	ct, err := blobField(in, "CiphertextBlob")
	if err != nil || len(ct) < s.aead.NonceSize() {
		return nil, http.StatusBadRequest, "InvalidCiphertextException", "malformed ciphertext"
	}
	n := s.aead.NonceSize()
	pt, err := s.aead.Open(nil, ct[:n], ct[n:], nil)
	if err != nil {
		return nil, http.StatusBadRequest, "InvalidCiphertextException", err.Error()
	}
	return map[string]any{"KeyId": awsKeyID, "Plaintext": pt, "EncryptionAlgorithm": "SYMMETRIC_DEFAULT"}, http.StatusOK, "", ""
}

// blobField decodes the base64 blob field name of an AWS JSON request.
func blobField(in map[string]any, name string) ([]byte, error) {
	v, ok := in[name].(string)
	if !ok {
		return nil, fmt.Errorf("%s is required", name)
	}
	var b []byte
	if err := json.Unmarshal([]byte(`"`+v+`"`), &b); err != nil {
		return nil, fmt.Errorf("%s is not base64: %w", name, err)
	}
	return b, nil
}

// writeAWSError writes an AWS JSON protocol error.
func writeAWSError(w http.ResponseWriter, status int, typ, msg string) {
	w.Header().Set("Content-Type", "application/x-amz-json-1.1")
	w.Header().Set("X-Amzn-Errortype", typ)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"__type": typ, "message": msg})
}
