// SPDX-License-Identifier: MIT

package providerflags

import (
	"context"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
)

// loadAWSConfig returns the resolved AWS SDK config for the AWS KMS
// adapter. When region is empty the SDK falls back to AWS_REGION,
// AWS_DEFAULT_REGION, the IMDS instance profile, etc.
func loadAWSConfig(ctx context.Context, region string) (awssdk.Config, error) {
	if region != "" {
		return awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region))
	}
	return awsconfig.LoadDefaultConfig(ctx)
}
