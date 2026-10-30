// SPDX-License-Identifier: MIT

package dispatch

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/messaging/azservicebus"
)

// AzureSBReceiver is the subset of *azservicebus.Receiver the
// dispatcher uses. Exported so tests can mock it.
type AzureSBReceiver interface {
	ReceiveMessages(ctx context.Context, maxMessages int, options *azservicebus.ReceiveMessagesOptions) ([]*azservicebus.ReceivedMessage, error)
	CompleteMessage(ctx context.Context, message *azservicebus.ReceivedMessage, options *azservicebus.CompleteMessageOptions) error
	AbandonMessage(ctx context.Context, message *azservicebus.ReceivedMessage, options *azservicebus.AbandonMessageOptions) error
	RenewMessageLock(ctx context.Context, msg *azservicebus.ReceivedMessage, options *azservicebus.RenewMessageLockOptions) error
	Close(ctx context.Context) error
}

// AzureSBSender is the subset of *azservicebus.Sender used by the
// SubmitAzure helper.
type AzureSBSender interface {
	SendMessage(ctx context.Context, message *azservicebus.Message, options *azservicebus.SendMessageOptions) error
	Close(ctx context.Context) error
}

type azureDispatcher struct {
	cfg      CloudConfig
	receiver AzureSBReceiver

	mu       sync.Mutex
	inFlight map[string]*azservicebus.ReceivedMessage
}

func newAzure(ctx context.Context, c CloudConfig) (Dispatcher, error) {
	if c.QueueURL == "" {
		return nil, errors.New("dispatch.azure: CloudConfig.QueueURL is required (namespace/queue)")
	}
	// QueueURL has the form <namespace>.servicebus.windows.net/<queue>.
	// We parse with the Azure SDK convention.
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
	rec, err := client.NewReceiverForQueue(queue, nil)
	if err != nil {
		return nil, fmt.Errorf("dispatch.azure: NewReceiverForQueue: %w", err)
	}
	return &azureDispatcher{cfg: c, receiver: rec, inFlight: make(map[string]*azservicebus.ReceivedMessage)}, nil
}

// NewAzureWithReceiver constructs an azureDispatcher with an injected
// receiver. Used by tests.
func NewAzureWithReceiver(c CloudConfig, r AzureSBReceiver) Dispatcher {
	return &azureDispatcher{cfg: c, receiver: r, inFlight: make(map[string]*azservicebus.ReceivedMessage)}
}

func parseAzureURL(s string) (host, queue string, err error) {
	// "<namespace>.servicebus.windows.net/<queue>"
	idx := -1
	for i := 0; i < len(s); i++ {
		if s[i] == '/' {
			idx = i
			break
		}
	}
	if idx < 0 || idx == len(s)-1 {
		return "", "", fmt.Errorf("dispatch.azure: malformed QueueURL %q (want <namespace>.servicebus.windows.net/<queue>)", s)
	}
	host = "sb://" + s[:idx] + "/"
	queue = s[idx+1:]
	return host, queue, nil
}

func (d *azureDispatcher) Receive(ctx context.Context) (*Job, error) {
	msgs, err := d.receiver.ReceiveMessages(ctx, 1, nil)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ErrNoJob
		}
		return nil, fmt.Errorf("dispatch.azure: ReceiveMessages: %w", err)
	}
	if len(msgs) == 0 {
		return nil, ErrNoJob
	}
	msg := msgs[0]
	job, err := decodeAzure(msg)
	if err != nil {
		_ = d.receiver.AbandonMessage(ctx, msg, nil)
		return nil, fmt.Errorf("dispatch.azure: decode: %w", err)
	}
	d.mu.Lock()
	d.inFlight[string(job.ReceiptToken)] = msg
	d.mu.Unlock()
	return job, nil
}

func (d *azureDispatcher) Ack(ctx context.Context, j *Job) error {
	msg, err := d.take(j)
	if err != nil {
		return err
	}
	return d.receiver.CompleteMessage(ctx, msg, nil)
}

func (d *azureDispatcher) Nack(ctx context.Context, j *Job, reason string) error {
	msg, err := d.take(j)
	if err != nil {
		return err
	}
	return d.receiver.AbandonMessage(ctx, msg, nil)
}

func (d *azureDispatcher) Heartbeat(ctx context.Context, j *Job) error {
	d.mu.Lock()
	msg, ok := d.inFlight[string(j.ReceiptToken)]
	d.mu.Unlock()
	if !ok {
		return ErrJobNotInFlight
	}
	return d.receiver.RenewMessageLock(ctx, msg, nil)
}

func (d *azureDispatcher) Close() error {
	return d.receiver.Close(context.Background())
}

func (d *azureDispatcher) take(j *Job) (*azservicebus.ReceivedMessage, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if j == nil {
		return nil, errors.New("dispatch.azure: nil Job")
	}
	msg, ok := d.inFlight[string(j.ReceiptToken)]
	if !ok {
		return nil, ErrJobNotInFlight
	}
	delete(d.inFlight, string(j.ReceiptToken))
	return msg, nil
}

func decodeAzure(m *azservicebus.ReceivedMessage) (*Job, error) {
	if len(m.Body) == 0 {
		return nil, errors.New("empty servicebus message")
	}
	var p sqsJobPayload
	if err := json.Unmarshal(m.Body, &p); err != nil {
		return nil, err
	}
	auth, _ := base64.StdEncoding.DecodeString(p.AuthBundle)
	return &Job{
		RunID:        p.RunID,
		Scenario:     p.Scenario,
		ScriptURL:    p.ScriptURL,
		TargetURL:    p.TargetURL,
		VUs:          p.VUs,
		Rate:         p.Rate,
		Duration:     time.Duration(p.DurationNs),
		AuthBundle:   auth,
		ReceiptToken: []byte(m.MessageID),
	}, nil
}

// SubmitAzure publishes a Job through sender.
func SubmitAzure(ctx context.Context, sender AzureSBSender, j *Job) error {
	body, err := encodeJob(j)
	if err != nil {
		return err
	}
	return sender.SendMessage(ctx, &azservicebus.Message{Body: body}, nil)
}
