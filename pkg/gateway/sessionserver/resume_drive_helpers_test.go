// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
)

// resumeAndDrive posts /resume for the acme session id and, when the call
// recorded the resume as a store write (200 with the row in resume_pending),
// drives the restore as the watchdog's resume_pending sweep does and waits
// for the attempt to return. It returns the POST's recorder. The restore runs
// in the resume driver, so a test of the restore's effects reads them after
// this call rather than from the POST's answer.
//
// spec: §7.3 (Resume driver; Retry and Resume)
func resumeAndDrive(t *testing.T, srv *sessionserver.Server, id string) *httptest.ResponseRecorder {
	t.Helper()
	rr := postSessionStep(t, srv.Handler(), "/v1/sessions/"+id+"/resume", nil)
	if rr.Code != http.StatusOK {
		return rr
	}
	var body struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode resume answer: %v", err)
	}
	if body.State == string(session.StateResumePending) {
		srv.DriveResumeAndWait(context.Background(), sessionstore.Session{TenantID: "acme", ID: id})
	}
	return rr
}
