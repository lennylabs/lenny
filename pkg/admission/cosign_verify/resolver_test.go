// SPDX-License-Identifier: MIT

package cosign_verify

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadStaticResolverReadsPolicyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cosign-policy.json")
	policy := `{
	  "images": {
	    "ghcr.io/lennylabs/runtime@sha256:aaa": {
	      "digest": "sha256:1111111111111111111111111111111111111111111111111111111111111111",
	      "signature": "QUJD"
	    }
	  }
	}`
	if err := os.WriteFile(path, []byte(policy), 0o600); err != nil {
		t.Fatalf("write policy file: %v", err)
	}

	r, err := LoadStaticResolver(path)
	if err != nil {
		t.Fatalf("load policy file: %v", err)
	}
	sd, err := r.Resolve(context.Background(), "ghcr.io/lennylabs/runtime@sha256:aaa")
	if err != nil {
		t.Fatalf("resolve recorded image: %v", err)
	}
	if sd.Signature != "QUJD" {
		t.Errorf("signature = %q, want QUJD", sd.Signature)
	}
}

func TestLoadStaticResolverErrorsOnMissingFile(t *testing.T) {
	_, err := LoadStaticResolver(filepath.Join(t.TempDir(), "absent.json"))
	if err == nil {
		t.Fatalf("a missing policy file must be a load error, not a silent empty resolver")
	}
}

func TestLoadStaticResolverErrorsOnCorruptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "corrupt.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if _, err := LoadStaticResolver(path); err == nil {
		t.Fatalf("a corrupt policy file must be a load error")
	}
}

func TestStaticResolverRejectsUnrecordedImage(t *testing.T) {
	r := NewStaticResolver(map[string]SignedDigest{})
	_, err := r.Resolve(context.Background(), "ghcr.io/lennylabs/runtime@sha256:zzz")
	if err == nil {
		t.Fatalf("an unrecorded image must resolve to an error so it is treated as unsigned")
	}
	if !strings.Contains(err.Error(), "no signature recorded") {
		t.Errorf("error should explain the absent entry, got %v", err)
	}
}
