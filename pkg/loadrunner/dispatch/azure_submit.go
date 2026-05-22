// SPDX-License-Identifier: MIT

package dispatch

import (
	"context"
	"errors"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/messaging/azservicebus"
)

// azureSubmitter publishes jobs to a Service Bus queue.
type azureSubmitter struct {
	client *azservicebus.Client
	sender AzureSBSender
}

func newAzureSubmitter(ctx context.Context, c CloudConfig) (Submitter, error) {
	if c.QueueURL == "" {
		return nil, errors.New("dispatch.azure: CloudConfig.QueueURL required (namespace/queue)")
	}
	host, queue, err := parseAzureURL(c.QueueURL)
	if err != nil {
		return nil, err
	}
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return nil, fmt.Errorf("dispatch.azure: NewDefaultAzureCredential: %w", err)
	}
	client, err := azservicebus.NewClient(host, cred, nil)
	if err != nil {
		return nil, fmt.Errorf("dispatch.azure: NewClient: %w", err)
	}
	sender, err := client.NewSender(queue, nil)
	if err != nil {
		_ = client.Close(ctx)
		return nil, fmt.Errorf("dispatch.azure: NewSender: %w", err)
	}
	return &azureSubmitter{client: client, sender: sender}, nil
}

// NewAzureSubmitterWithSender lets tests inject a fake sender.
func NewAzureSubmitterWithSender(s AzureSBSender) Submitter {
	return &azureSubmitter{sender: s}
}

func (s *azureSubmitter) Submit(ctx context.Context, j *Job) error {
	if s.sender == nil {
		return errors.New("dispatch.azure: sender is nil")
	}
	return SubmitAzure(ctx, s.sender, j)
}

func (s *azureSubmitter) Close() error {
	if s.sender != nil {
		_ = s.sender.Close(context.Background())
	}
	if s.client != nil {
		return s.client.Close(context.Background())
	}
	return nil
}
