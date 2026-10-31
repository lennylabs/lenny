// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// testSignature is the signature query value every capability in these
// tests carries, so an assertion can search an error string for it.
const testSignature = "c0ffee5eed5ig"

// capabilityURL is a presigned-style URL for chunk n on base.
func capabilityURL(base string, n int) string {
	return base + "/acme/checkpoints/sess-derived/ck/chunk-0000" + string(rune('0'+n)) +
		".tar?X-Amz-Credential=AKIAEXAMPLE%2F20261030&X-Amz-Signature=" + testSignature
}

// requireNoCapability fails when text carries a capability URL, its
// signature, or a presigned query parameter name.
func requireNoCapability(t *testing.T, where, text string, urls ...string) {
	t.Helper()
	for _, u := range urls {
		if strings.Contains(text, u) {
			t.Errorf("%s carries the capability URL: %q", where, text)
		}
	}
	for _, needle := range []string{testSignature, "X-Amz-Signature", "X-Amz-Credential"} {
		if strings.Contains(text, needle) {
			t.Errorf("%s carries %q: %q", where, needle, text)
		}
	}
}

// deniedStore answers every request with an S3-style 403 AccessDenied.
func deniedStore(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, "<Error><Code>AccessDenied</Code></Error>")
	}))
	t.Cleanup(srv.Close)
	return srv
}

// closedStoreURL is the base URL of a listener that no longer accepts
// connections, so a request to it fails at the transport.
func closedStoreURL(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.NotFoundHandler())
	base := srv.URL
	srv.Close()
	return base
}

// TestCheckpointTransportErrorsCarryNoCapability_spec_13_2_3 drives the
// production object-store transport at a store that answers 403, at a
// store that is unreachable, and with a URL that does not parse, on both
// the GET and the PUT, and pins that no returned error carries the
// presigned URL or its signature. A cancelled request still unwraps to
// context.Canceled.
//
// spec: §13.2.3 (Checkpoint Capability Model); §7.1 (Derive restore semantics)
// diagnosis: an object-store request error repeated the presigned URL, so the signed capability reached the adapter's gRPC status, its span, and the gateway's error chain. Check httpCheckpointTransport and withoutCapability in pkg/adapter/checkpointtransport.go.
func TestCheckpointTransportErrorsCarryNoCapability_spec_13_2_3(t *testing.T) {
	tr, err := NewHTTPCheckpointTransport("")
	if err != nil {
		t.Fatalf("NewHTTPCheckpointTransport: %v", err)
	}
	denied := capabilityURL(deniedStore(t).URL, 0)
	unreachable := capabilityURL(closedStoreURL(t), 1)
	malformed := "http://[::1" + capabilityURL("", 2)

	for _, tc := range []struct {
		name, url string
	}{
		{name: "403 from the store", url: denied},
		{name: "unreachable store", url: unreachable},
		{name: "URL that does not parse", url: malformed},
	} {
		t.Run("GET/"+tc.name, func(t *testing.T) {
			rc, gerr := tr.GetChunk(context.Background(), tc.url, nil)
			if gerr == nil {
				_ = rc.Close()
				t.Fatal("GetChunk succeeded, want an error")
			}
			requireNoCapability(t, "GetChunk error", gerr.Error(), tc.url)
		})
		t.Run("PUT/"+tc.name, func(t *testing.T) {
			_, _, perr := tr.PutChunk(context.Background(), tc.url, nil, 1, strings.NewReader("x"))
			if perr != nil {
				requireNoCapability(t, "PutChunk error", perr.Error(), tc.url)
			}
		})
	}

	t.Run("GET 403 names the status and the store's code", func(t *testing.T) {
		_, gerr := tr.GetChunk(context.Background(), denied, nil)
		if gerr == nil || !strings.Contains(gerr.Error(), "http 403") || !strings.Contains(gerr.Error(), "AccessDenied") {
			t.Errorf("GetChunk error = %v, want it to name http 403 and AccessDenied", gerr)
		}
	})
	t.Run("cancelled GET unwraps to context.Canceled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, gerr := tr.GetChunk(ctx, denied, nil)
		if !errors.Is(gerr, context.Canceled) {
			t.Errorf("GetChunk error = %v, want it to wrap context.Canceled", gerr)
		}
		requireNoCapability(t, "cancelled GetChunk error", gerr.Error(), denied)
	})
}

// TestFinalizeWorkspaceRestoreFetchFailureCarriesNoCapability_spec_7_1
// restores through the production transport while the store denies one
// chunk or cannot be reached, and pins that the bind fails UNAVAILABLE
// naming the chunk by index, and that neither the gRPC status message nor
// the session.finalize_workspace span status carries a capability URL or
// its signature.
//
// spec: §7.1 (Derive restore semantics); §13.2.3 (Checkpoint Capability Model); §16.3
// diagnosis: a failed first-bind restore copied the presigned GET URL into the FinalizeWorkspace status or its span. Check withoutCapability in pkg/adapter/checkpointtransport.go and the fetch error in chunkStream in pkg/adapter/resume.go.
func TestFinalizeWorkspaceRestoreFetchFailureCarriesNoCapability_spec_7_1(t *testing.T) {
	for _, tc := range []struct {
		name string
		base func(t *testing.T) string
		want string
	}{
		{name: "store denies the chunk", base: func(t *testing.T) string { return deniedStore(t).URL }, want: "http 403"},
		{name: "store unreachable", base: closedStoreURL, want: "checkpoint GET"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := installInternalSpanRecorder(t)
			tr, err := NewHTTPCheckpointTransport("")
			if err != nil {
				t.Fatalf("NewHTTPCheckpointTransport: %v", err)
			}
			srv := restoreServer(t, tr)
			u := capabilityURL(tc.base(t), 0)
			grants := []*adapterv1.ChunkGrant{{Index: 0, Url: u, Length: 1}}

			_, ferr := srv.FinalizeWorkspace(context.Background(), restoreFinalizeReq("sess-derived", grants))
			if status.Code(ferr) != codes.Unavailable {
				t.Fatalf("err = %v, want UNAVAILABLE", ferr)
			}
			msg := status.Convert(ferr).Message()
			for _, part := range []string{"fetch chunk 0", tc.want} {
				if !strings.Contains(msg, part) {
					t.Errorf("message = %q, want it to name %q", msg, part)
				}
			}
			requireNoCapability(t, "FinalizeWorkspace status", msg, u)

			span := endedSpanNamed(rec.Ended(), "session.finalize_workspace")
			if span == nil {
				t.Fatal("session.finalize_workspace span not recorded")
			}
			requireNoCapability(t, "span status", span.Status().Description, u)
			for _, ev := range span.Events() {
				for _, kv := range ev.Attributes {
					requireNoCapability(t, "span event "+ev.Name, kv.Value.Emit(), u)
				}
			}
		})
	}
}
