// SPDX-License-Identifier: MIT

package adapter_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/adapter/credfile"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

func credServer(t *testing.T) *adapter.Server {
	t.Helper()
	s := adapter.New("cred-test")
	s.CredentialsDir = t.TempDir()
	return s
}

func credLease(id, provider, payload string) *adapterv1.CredentialLease {
	return &adapterv1.CredentialLease{
		LeaseId:  id,
		Provider: provider,
		Payload:  []byte(payload),
	}
}

// credProviders reads the materialized credential file and indexes its
// entries by provider.
func credProviders(t *testing.T, dir string) map[string]map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, credfile.FileName))
	if err != nil {
		t.Fatalf("read credential file: %v", err)
	}
	var doc struct {
		Providers []map[string]any `json:"providers"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("decode credential file: %v", err)
	}
	byProvider := map[string]map[string]any{}
	for _, entry := range doc.Providers {
		name, _ := entry["provider"].(string)
		byProvider[name] = entry
	}
	return byProvider
}

func TestAssignCredentialsWritesTheCredentialFile(t *testing.T) {
	s := credServer(t)

	_, err := s.AssignCredentials(context.Background(), &adapterv1.AssignCredentialsRequest{
		SessionId: &adapterv1.SessionId{Value: "sess-1"},
		Leases: map[string]*adapterv1.CredentialLease{
			"anthropic": credLease("l1", "anthropic", `{"deliveryMode":"direct"}`),
		},
	})
	if err != nil {
		t.Fatalf("AssignCredentials: %v", err)
	}
	if _, ok := credProviders(t, s.CredentialsDir)["anthropic"]; !ok {
		t.Error("the credential file has no anthropic entry after AssignCredentials")
	}
}

func TestAssignCredentialsRequiresASessionID(t *testing.T) {
	s := credServer(t)

	_, err := s.AssignCredentials(context.Background(), &adapterv1.AssignCredentialsRequest{})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("error code = %v, want InvalidArgument", status.Code(err))
	}
}

func TestAssignCredentialsRequiresACredentialsDir(t *testing.T) {
	s := adapter.New("cred-test") // CredentialsDir left unset.

	_, err := s.AssignCredentials(context.Background(), &adapterv1.AssignCredentialsRequest{
		SessionId: &adapterv1.SessionId{Value: "sess-1"},
	})
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("error code = %v, want FailedPrecondition", status.Code(err))
	}
}

func TestAssignCredentialsRejectsADifferentSession(t *testing.T) {
	s := credServer(t)
	ctx := context.Background()

	if _, err := s.AssignCredentials(ctx, &adapterv1.AssignCredentialsRequest{
		SessionId: &adapterv1.SessionId{Value: "sess-A"},
	}); err != nil {
		t.Fatalf("first AssignCredentials: %v", err)
	}
	_, err := s.AssignCredentials(ctx, &adapterv1.AssignCredentialsRequest{
		SessionId: &adapterv1.SessionId{Value: "sess-B"},
	})
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("error code = %v, want FailedPrecondition for a second session", status.Code(err))
	}
}

func TestRotateCredentialsMergesProviders(t *testing.T) {
	s := credServer(t)
	ctx := context.Background()

	if _, err := s.AssignCredentials(ctx, &adapterv1.AssignCredentialsRequest{
		SessionId: &adapterv1.SessionId{Value: "sess-1"},
		Leases: map[string]*adapterv1.CredentialLease{
			"anthropic": credLease("l-anth-1", "anthropic", `{}`),
			"openai":    credLease("l-oai-1", "openai", `{}`),
		},
	}); err != nil {
		t.Fatalf("AssignCredentials: %v", err)
	}
	if _, err := s.RotateCredentials(ctx, &adapterv1.RotateCredentialsRequest{
		SessionId: &adapterv1.SessionId{Value: "sess-1"},
		Leases: map[string]*adapterv1.CredentialLease{
			"anthropic": credLease("l-anth-2", "anthropic", `{}`),
		},
	}); err != nil {
		t.Fatalf("RotateCredentials: %v", err)
	}

	got := credProviders(t, s.CredentialsDir)
	if got["anthropic"]["leaseId"] != "l-anth-2" {
		t.Errorf("anthropic leaseId = %v, want the rotated l-anth-2", got["anthropic"]["leaseId"])
	}
	if got["openai"]["leaseId"] != "l-oai-1" {
		t.Errorf("openai leaseId = %v, want the retained l-oai-1", got["openai"]["leaseId"])
	}
}

func TestRotateCredentialsRequiresAPriorAssignment(t *testing.T) {
	s := credServer(t)

	_, err := s.RotateCredentials(context.Background(), &adapterv1.RotateCredentialsRequest{
		SessionId: &adapterv1.SessionId{Value: "sess-1"},
	})
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("error code = %v, want FailedPrecondition with no prior AssignCredentials", status.Code(err))
	}
}

func TestRotateCredentialsRejectsADifferentSession(t *testing.T) {
	s := credServer(t)
	ctx := context.Background()

	if _, err := s.AssignCredentials(ctx, &adapterv1.AssignCredentialsRequest{
		SessionId: &adapterv1.SessionId{Value: "sess-A"},
	}); err != nil {
		t.Fatalf("AssignCredentials: %v", err)
	}
	_, err := s.RotateCredentials(ctx, &adapterv1.RotateCredentialsRequest{
		SessionId: &adapterv1.SessionId{Value: "sess-B"},
	})
	if status.Code(err) != codes.NotFound {
		t.Errorf("error code = %v, want NotFound for a mismatched session", status.Code(err))
	}
}

func TestRevokeCredentialsRemovesNamedProviders(t *testing.T) {
	s := credServer(t)
	ctx := context.Background()

	if _, err := s.AssignCredentials(ctx, &adapterv1.AssignCredentialsRequest{
		SessionId: &adapterv1.SessionId{Value: "sess-1"},
		Leases: map[string]*adapterv1.CredentialLease{
			"anthropic": credLease("l-anth", "anthropic", `{}`),
			"openai":    credLease("l-oai", "openai", `{}`),
		},
	}); err != nil {
		t.Fatalf("AssignCredentials: %v", err)
	}
	if _, err := s.RevokeCredentials(ctx, &adapterv1.RevokeCredentialsRequest{
		SessionId: &adapterv1.SessionId{Value: "sess-1"},
		Providers: []string{"openai"},
		Reason:    "lease expired",
	}); err != nil {
		t.Fatalf("RevokeCredentials: %v", err)
	}

	got := credProviders(t, s.CredentialsDir)
	if _, present := got["openai"]; present {
		t.Error("the openai entry remains after revocation")
	}
	if _, present := got["anthropic"]; !present {
		t.Error("the anthropic entry was removed though it was not revoked")
	}
}

func TestRevokeCredentialsRequiresAPriorAssignment(t *testing.T) {
	s := credServer(t)

	_, err := s.RevokeCredentials(context.Background(), &adapterv1.RevokeCredentialsRequest{
		SessionId: &adapterv1.SessionId{Value: "sess-1"},
		Providers: []string{"anthropic"},
	})
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("error code = %v, want FailedPrecondition with no prior AssignCredentials", status.Code(err))
	}
}
