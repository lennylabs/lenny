// SPDX-License-Identifier: MIT

package journeys

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These tests exercise contributor tooling and carry no spec annotation.

func TestRedactorRemovesKnownValuesAndSecretForms(t *testing.T) {
	r := NewRedactor("admin-token-value-123", "short")
	in := strings.Join([]string{
		`{"type":"user","message":{"content":"LENNY_API_TOKEN=admin-token-value-123"}}`,
		`token as base64: ` + base64.StdEncoding.EncodeToString([]byte("admin-token-value-123")),
		`Authorization: Bearer eyJhbGciOiJIUzI1NiJ9.payload.sig`,
		`users:\n- user:\n    token: abcdefghijklmnopqrstuvwxyz\n    client-key-data: LS0tLS1CRUdJTiBSU0EgUFJJVkFURQ==`,
		`"client-certificate-data":"Q0VSVElGSUNBVEUtREFUQS1WQUxVRQ=="`,
		`-----BEGIN RSA PRIVATE KEY-----\nMIIEow\n-----END RSA PRIVATE KEY-----`,
		`CLAUDE_CODE_OAUTH_TOKEN=sk-ant-oat01-abc`,
		`the word short stays`,
	}, "\n")
	out := r.String(in)
	for _, secret := range []string{
		"admin-token-value-123", base64.StdEncoding.EncodeToString([]byte("admin-token-value-123")),
		"eyJhbGciOiJIUzI1NiJ9", "abcdefghijklmnopqrstuvwxyz", "LS0tLS1CRUdJTiBSU0EgUFJJVkFURQ",
		"Q0VSVElGSUNBVEUtREFUQS1WQUxVRQ", "MIIEow", "sk-ant-oat01-abc",
	} {
		if strings.Contains(out, secret) {
			t.Errorf("the redacted text keeps %q:\n%s", secret, out)
		}
	}
	if !strings.Contains(out, "the word short stays") {
		t.Error("a short value was redacted, blanking unrelated text")
	}
	var nilRedactor *Redactor
	if nilRedactor.String("x") != "x" {
		t.Error("a nil redactor changed the text")
	}
}

func TestRedactorWriterRedactsEachWrite(t *testing.T) {
	var buf bytes.Buffer
	w := NewRedactor("secret-value-abc").Writer(&buf)
	n, err := w.Write([]byte("line with secret-value-abc\n"))
	if err != nil || n != len("line with secret-value-abc\n") {
		t.Fatalf("write = %d, %v", n, err)
	}
	if buf.String() != "line with "+Redacted+"\n" {
		t.Fatalf("written = %q", buf.String())
	}
}

func TestKubeconfigSecretsReadsEveryUserCredential(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kubeconfig")
	if err := os.WriteFile(path, []byte(internalKubeconfig+"- name: other\n  user:\n    token: other-token-value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(KubeconfigSecrets(path), "|")
	for _, want := range []string{base64.StdEncoding.EncodeToString([]byte("certificate-data")), "certificate-data", fixtureKeyData, "key-data-fixture", "other-token-value"} {
		if !strings.Contains(got, want) {
			t.Errorf("secrets lack %q: %s", want, got)
		}
	}
	if KubeconfigSecrets(filepath.Join(t.TempDir(), "missing")) != nil {
		t.Error("a missing kubeconfig has secrets")
	}
	if KubeconfigSecretsFrom([]byte(":::")) != nil {
		t.Error("an unparseable kubeconfig has secrets")
	}
}

func TestReadTokenFileRequiresAPrivateFileWithOneToken(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, TokenFileName)
	if _, err := ReadTokenFile(path); err == nil || !strings.Contains(err.Error(), "claude setup-token") {
		t.Fatalf("a missing file: %v", err)
	}
	if err := os.WriteFile(path, []byte("sk-ant-oat01-token\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadTokenFile(path); err == nil || !strings.Contains(err.Error(), "chmod 600") {
		t.Fatalf("a world-readable file: %v", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	tok, err := ReadTokenFile(path)
	if err != nil || tok != "sk-ant-oat01-token" {
		t.Fatalf("token = %q, %v", tok, err)
	}
	if err := os.WriteFile(path, []byte("two tokens\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadTokenFile(path); err == nil {
		t.Error("a file holding two words was accepted")
	}
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadTokenFile(path); err == nil {
		t.Error("an empty token file was accepted")
	}
}
