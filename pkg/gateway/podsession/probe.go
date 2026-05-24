// SPDX-License-Identifier: MIT

package podsession

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"k8s.io/client-go/rest"
)

// readyzProbeTimeout bounds the §4.6.1 admission-reachability probe. The
// probe is on the session-start hot path's fallback branch, so a slow
// or hung API server must fail fast rather than add latency to a
// fallback that would itself fail at the CRD CREATE.
const readyzProbeTimeout = time.Second

// NewReadyzProbe builds the §4.6.1 admission-reachability probe for the
// pod binder: a lightweight GET /readyz against the API server using the
// cluster rest config's transport (so it reuses the gateway's mTLS and
// auth). It returns a function suitable for Binder.APIServerReachable;
// the function returns nil when the API server answers 200 and an error
// otherwise. spec: §4.6.1 "Admission webhook reachability" — "The
// gateway probes API server reachability before initiating the fallback
// (a lightweight GET /readyz or equivalent); if the probe fails, the
// fallback is skipped."
func NewReadyzProbe(cfg *rest.Config) (func(context.Context) error, error) {
	httpClient, err := rest.HTTPClientFor(cfg)
	if err != nil {
		return nil, fmt.Errorf("podsession: build readyz probe client: %w", err)
	}
	base := strings.TrimRight(cfg.Host, "/")
	return func(ctx context.Context) error {
		cctx, cancel := context.WithTimeout(ctx, readyzProbeTimeout)
		defer cancel()
		req, err := http.NewRequestWithContext(cctx, http.MethodGet, base+"/readyz", nil)
		if err != nil {
			return err
		}
		resp, err := httpClient.Do(req)
		if err != nil {
			return err
		}
		defer func() {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("podsession: apiserver /readyz returned %d", resp.StatusCode)
		}
		return nil
	}, nil
}
