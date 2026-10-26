// SPDX-License-Identifier: MIT

package stack

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"k8s.io/client-go/rest"
)

// apiServerAnswering returns a rest.Config for a kube-apiserver stand-in
// that answers every request with status and a Status body naming reason.
func apiServerAnswering(t *testing.T, status int, reason string) *rest.Config {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = fmt.Fprintf(w, `{"kind":"Status","apiVersion":"v1","status":"Failure","reason":%q,"code":%d}`, reason, status)
	}))
	t.Cleanup(srv.Close)
	return &rest.Config{Host: srv.URL}
}

// TestCreateMasterKeySecretKeepsAnExistingKeyAndReportsFailures pins the
// lenny up master key Secret step: an unreadable kubeconfig and a create
// the kube-apiserver refuses are errors, so bring-up stops before the
// Deployments mount a Secret that does not exist, and an AlreadyExists
// answer leaves the existing key in place rather than failing.
//
// spec: 17.4 (Local Development Mode), 10.2 (Authentication) "Signing key and algorithm"
func TestCreateMasterKeySecretKeepsAnExistingKeyAndReportsFailures(t *testing.T) {
	ctx := context.Background()
	missing := filepath.Join(t.TempDir(), "absent-kubeconfig")
	if err := createMasterKeySecret(ctx, missing); err == nil || !strings.Contains(err.Error(), "load kubeconfig") {
		t.Errorf("unreadable kubeconfig: err = %v, want a load kubeconfig error", err)
	}
	if err := createMasterKeySecretFromConfig(ctx, apiServerAnswering(t, http.StatusForbidden, "Forbidden")); err == nil ||
		!strings.Contains(err.Error(), "create "+devKMSMasterKeySecretName) {
		t.Errorf("refused create: err = %v, want a create error naming the Secret", err)
	}
	if err := createMasterKeySecretFromConfig(ctx, apiServerAnswering(t, http.StatusConflict, "AlreadyExists")); err != nil {
		t.Errorf("existing Secret: err = %v, want nil so the existing key is kept", err)
	}
}
