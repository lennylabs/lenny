// SPDX-License-Identifier: MIT

package providerflags

import (
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
)

// loadAzureCredential resolves an Azure credential through the
// standard chain (NewDefaultAzureCredential): managed identity inside
// AKS, workload identity, env vars, az login. The KMS adapter calls
// Encrypt/Decrypt on the Key Vault key with the resolved credential.
func loadAzureCredential() (azcore.TokenCredential, error) {
	return azidentity.NewDefaultAzureCredential(nil)
}
