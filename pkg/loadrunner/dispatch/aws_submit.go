// SPDX-License-Identifier: MIT

package dispatch

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
)

// awsSubmitter publishes jobs to an SQS queue.
type awsSubmitter struct {
	client   SQSAPI
	queueURL string
}

func newAWSSubmitter(ctx context.Context, c CloudConfig) (Submitter, error) {
	if c.QueueURL == "" {
		return nil, errors.New("dispatch.aws: CloudConfig.QueueURL is required")
	}
	awsCfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(c.Region))
	if err != nil {
		return nil, fmt.Errorf("dispatch.aws: load config: %w", err)
	}
	return &awsSubmitter{client: sqs.NewFromConfig(awsCfg), queueURL: c.QueueURL}, nil
}

// NewAWSSubmitterWithClient lets tests inject a SQS mock.
func NewAWSSubmitterWithClient(c CloudConfig, client SQSAPI) Submitter {
	return &awsSubmitter{client: client, queueURL: c.QueueURL}
}

func (s *awsSubmitter) Submit(ctx context.Context, j *Job) error {
	return SubmitAWS(ctx, s.client, s.queueURL, j)
}

func (s *awsSubmitter) Close() error { return nil }
