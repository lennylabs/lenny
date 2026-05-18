// SPDX-License-Identifier: MIT

package credassign

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/lennylabs/lenny/pkg/credential"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// proxyMaterializedConfig is the §4.9 proxy-mode materializedConfig: the
// uniform, provider-agnostic configuration a proxy-mode lease carries
// instead of a real upstream credential. It is the materializedConfig
// object the adapter writes verbatim into the runtime credential file.
type proxyMaterializedConfig struct {
	ProxyURL      string `json:"proxyUrl"`
	ProxyDialect  string `json:"proxyDialect"`
	LeaseToken    string `json:"leaseToken"`
	UpstreamModel string `json:"upstreamModel,omitempty"`
}

// credentialPayload is the JSON object the adapter unmarshals from a
// CredentialLease.Payload: the §4.7 credential-file entry's deliveryMode
// and materializedConfig. The adapter merges the lease's typed fields
// (leaseId, provider, expiresAt) over it when writing the file.
type credentialPayload struct {
	DeliveryMode       string `json:"deliveryMode"`
	MaterializedConfig any    `json:"materializedConfig"`
}

// AssignProto leases a credential from the named pool to the session
// and returns the wire-form adapterv1.CredentialLease the gateway
// pushes to the pod via the adapter's AssignCredentials RPC (§4.7 item
// 4). It is Assign followed by ProtoLease: the lease is recorded in the
// credential-lease store and the upstream credential cached for the
// §4.9 LLM proxy, then converted to the adapter wire form.
//
// AssignProto satisfies the gateway session path's credential-assigner
// dependency, so the §4.7 binder can mint and deliver a session's
// leases without depending on the lease internals.
func (s *Service) AssignProto(poolName, sessionID, spiffeURI string) (*adapterv1.CredentialLease, error) {
	lease, err := s.Assign(poolName, sessionID, spiffeURI)
	if err != nil {
		return nil, err
	}
	return ProtoLease(lease)
}

// ProtoLease converts a minted §4.9 credential lease into the wire-form
// adapterv1.CredentialLease the gateway pushes to a pod's adapter via
// AssignCredentials (§4.7 item 4). The lease's typed fields map to the
// proto's typed fields; the §4.9 materializedConfig is JSON-encoded into
// the proto payload, which the adapter writes into the runtime
// credential file.
//
// Only proxy-mode leases are convertible here: a proxy-mode lease's
// ProxyConfig fully describes its materializedConfig, so no upstream
// secret is needed. A direct-mode lease's materializedConfig is the real
// upstream credential, which the credential.Lease does not carry;
// ProtoLease returns an error for a direct-mode lease rather than
// emitting an incomplete credential file.
func ProtoLease(lease credential.Lease) (*adapterv1.CredentialLease, error) {
	if lease.DeliveryMode != credential.DeliveryProxy {
		return nil, fmt.Errorf(
			"credassign: lease %s uses delivery mode %q; only proxy-mode leases are convertible to the adapter wire form",
			lease.LeaseID, lease.DeliveryMode,
		)
	}
	if lease.Proxy == nil {
		return nil, fmt.Errorf("credassign: proxy-mode lease %s has no proxy materializedConfig", lease.LeaseID)
	}

	payload, err := json.Marshal(credentialPayload{
		DeliveryMode: string(credential.DeliveryProxy),
		MaterializedConfig: proxyMaterializedConfig{
			ProxyURL:      lease.Proxy.ProxyURL,
			ProxyDialect:  lease.Proxy.ProxyDialect,
			LeaseToken:    lease.Proxy.LeaseToken,
			UpstreamModel: lease.Proxy.UpstreamModel,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("credassign: encode credential payload for lease %s: %w", lease.LeaseID, err)
	}

	return &adapterv1.CredentialLease{
		LeaseId:           lease.LeaseID,
		Provider:          string(lease.Provider),
		ExpiresAtUnixMs:   unixMs(lease.ExpiresAt),
		RenewBeforeUnixMs: unixMs(lease.RenewBefore),
		Payload:           payload,
	}, nil
}

// unixMs returns t as Unix milliseconds, or 0 for the zero time so an
// unset timestamp leaves the proto field at its default.
func unixMs(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}
