// SPDX-License-Identifier: MIT

package lifecycleharness

import (
	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credassign"
)

// ProxyPool is the harness's proxy-mode credential pool.
func ProxyPool() credassign.Pool {
	creds := make([]credassign.PoolCredential, 0, 8)
	for _, id := range []string{"key-1", "key-2", "key-3", "key-4", "key-5", "key-6", "key-7", "key-8"} {
		creds = append(creds, credassign.PoolCredential{ID: id, APIKey: "sk-" + id, Healthy: true})
	}
	return credassign.Pool{
		Name:         "claude-prod",
		Provider:     credential.ProviderAnthropicDirect,
		DeliveryMode: credential.DeliveryProxy,
		Strategy:     credential.StrategyLeastLoaded,
		ProxyURL:     "https://gateway-internal:8443/llm-proxy",
		ProxyDialect: "anthropic",
		Credentials:  creds,
	}
}
