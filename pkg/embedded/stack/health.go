// SPDX-License-Identifier: MIT

package stack

import (
	"context"
	"fmt"
	"net/http"
)

// probeHealthz issues a single GET to the gateway's unauthenticated
// liveness endpoint and returns nil when it answers 2xx. The §17.4
// in-cluster gateway serves the same /healthz endpoint it serves in
// production; lenny status reaches it through the host-side forwarder to
// report whether the gateway answers. The bring-up readiness wait gates on
// the gateway Deployment reporting Ready (waitDeploymentReady) rather than
// this HTTP probe.
//
// spec: §17.4 (lenny up reports the gateway ready when it answers).
func probeHealthz(ctx context.Context, baseURL string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/healthz", nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("gateway healthz returned HTTP %d", resp.StatusCode)
	}
	return nil
}
