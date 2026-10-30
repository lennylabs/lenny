// SPDX-License-Identifier: MIT

//go:build e2e_kind

// Tier-5 e2e Kind test that the §25.7 runbook index is actually built
// on the deployed lenny-ops binary. The index is unit- and
// component-tested against a directory fixture
// (pkg/ops/opsserver/runbooks_test.go, tests/tier2_component/runbooks),
// but nothing previously asserted that the live, chart-installed
// lenny-ops running in a container resolves its --runbook-dir default
// to a directory that actually exists. cmd/lenny-ops/flags.go defaults
// --runbook-dir to the relative path "docs/runbooks", which only
// resolves when the process's working directory is the repository
// root (true for the tests/testinfra/opsprocess subprocess harness);
// the distroless runtime image built by the repository Dockerfile
// carries no working-directory setup and, before this fix, no
// docs/runbooks payload at all, so the deployed pod's default flag
// value pointed at a nonexistent directory and s.runbooks stayed nil.
//
// spec: §25.7 ("The index is built at startup by scanning the bundled
// docs/runbooks/*.md files and parsing their front matter. No Postgres
// storage — runbooks are read-only artifacts shipped with the
// binary."); §25.7 Path A ("GET /v1/admin/runbooks | List all runbooks
// with full front matter", "GET /v1/admin/runbooks/{name} | Get full
// runbook content (rendered markdown)").
package tier5_e2e_kind_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
)

// spec: §25.7 ("The index is built at startup by scanning the bundled
// docs/runbooks/*.md files ... runbooks are read-only artifacts shipped
// with the binary."); §25.7 Path A table ("GET /v1/admin/runbooks |
// List all runbooks with full front matter").
//
// diagnosis: a 503 RUNBOOK_INDEX_UNAVAILABLE (or an empty "runbooks"
// list on a 200) here means the deployed lenny-ops container was built
// or deployed without its docs/runbooks corpus reachable at the
// process's --runbook-dir (cmd/lenny-ops/flags.go default
// "docs/runbooks", resolved relative to the container's working
// directory) — either the image does not bundle docs/runbooks/*.md, or
// the container's working directory does not put that relative path
// where the bundled files landed. The §25.7 Path A "follow the link"
// half additionally requires a specific known runbook (warm-pool-
// exhaustion, cited by name in the §25.7 Path B example) to resolve by
// name at GET /v1/admin/runbooks/{name}; a 404 there while the list
// endpoint is healthy means the index built but a specific expected
// file is missing from the bundle.
func TestRunbookIndexServedOnDeployedBinary(t *testing.T) {
	c := kind.InstallLenny(t)

	if !t5DeploymentReady(t, c, "lenny-ops") {
		t.Skip("precondition not met: lenny-ops is not Ready; the §25.7 runbook index is served by lenny-ops")
	}

	baseURL, stop := c.PortForward(t, "svc/lenny-ops", t5SystemNS, opsHTTPPort)
	defer stop()

	var listed struct {
		Runbooks []struct {
			Name string `json:"name"`
		} `json:"runbooks"`
	}
	t.Run("index/non-empty", func(t *testing.T) {
		code, body := ripGet(t, baseURL+"/v1/admin/runbooks")
		if code != http.StatusOK {
			t.Fatalf("GET /v1/admin/runbooks: status = %d, want 200; body: %s", code, body)
		}
		if err := json.Unmarshal(body, &listed); err != nil {
			t.Fatalf("unmarshal /v1/admin/runbooks response: %v; body: %s", err, body)
		}
		if len(listed.Runbooks) == 0 {
			t.Fatalf("GET /v1/admin/runbooks: returned an empty index; §25.7 requires the index to be built from the bundled docs/runbooks/*.md corpus, which is non-empty in this repository")
		}
	})

	// The §25.7 Path A "follow the link" hop: a known runbook name
	// (the same "warm-pool-exhaustion" name the §25.7 Path B example
	// names as the runbook field for WARM_POOL_EXHAUSTED) resolves to
	// its full markdown content rather than RUNBOOK_NOT_FOUND.
	t.Run("known-runbook/resolves-by-name", func(t *testing.T) {
		code, body := ripGet(t, baseURL+"/v1/admin/runbooks/warm-pool-exhaustion")
		if code != http.StatusOK {
			t.Fatalf("GET /v1/admin/runbooks/warm-pool-exhaustion: status = %d, want 200; body: %s", code, body)
		}
		var doc map[string]any
		if err := json.Unmarshal(body, &doc); err != nil {
			t.Fatalf("unmarshal /v1/admin/runbooks/warm-pool-exhaustion response: %v; body: %s", err, body)
		}
	})
}

// ripGet issues an authenticated GET against the deployed lenny-ops and
// returns its status code and raw response body. The e2e ops binary
// runs with production=false and no bearer-trust key configured by
// default on this endpoint's read path, but the dev-mode identity
// headers are carried regardless so the request is admitted the same
// way the sibling tier-5 tests authenticate against lenny-ops.
func ripGet(t *testing.T, url string) (int, []byte) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("build request GET %s: %v", url, err)
	}
	req.Header.Set("X-Lenny-Tenant-ID", "platform")
	req.Header.Set("X-Lenny-Roles", "platform-admin")
	req.Header.Set("X-Lenny-User-ID", "alice")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response body GET %s: %v", url, err)
	}
	return resp.StatusCode, body
}
