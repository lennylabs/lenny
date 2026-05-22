// SPDX-License-Identifier: MIT

package dispatch

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

// SQSAPI is the subset of the SQS client surface awsDispatcher uses.
// It is exported so tests can plug a mock in via WithSQSClient.
type SQSAPI interface {
	ReceiveMessage(ctx context.Context, params *sqs.ReceiveMessageInput, optFns ...func(*sqs.Options)) (*sqs.ReceiveMessageOutput, error)
	DeleteMessage(ctx context.Context, params *sqs.DeleteMessageInput, optFns ...func(*sqs.Options)) (*sqs.DeleteMessageOutput, error)
	ChangeMessageVisibility(ctx context.Context, params *sqs.ChangeMessageVisibilityInput, optFns ...func(*sqs.Options)) (*sqs.ChangeMessageVisibilityOutput, error)
	SendMessage(ctx context.Context, params *sqs.SendMessageInput, optFns ...func(*sqs.Options)) (*sqs.SendMessageOutput, error)
}

// awsDispatcher is the SQS Dispatcher implementation.
type awsDispatcher struct {
	cfg       CloudConfig
	client    SQSAPI
	queueURL  string
	heartbeat int32 // visibility timeout reset value, seconds
}

func newAWS(ctx context.Context, c CloudConfig) (Dispatcher, error) {
	if c.QueueURL == "" {
		return nil, errors.New("dispatch.aws: CloudConfig.QueueURL is required")
	}
	awsCfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(c.Region))
	if err != nil {
		return nil, fmt.Errorf("dispatch.aws: load config: %w", err)
	}
	client := sqs.NewFromConfig(awsCfg)
	return &awsDispatcher{cfg: c, client: client, queueURL: c.QueueURL, heartbeat: 300}, nil
}

// NewAWSWithClient constructs an awsDispatcher with an injected client.
// Used by tests to plug a mock.
func NewAWSWithClient(c CloudConfig, client SQSAPI) Dispatcher {
	return &awsDispatcher{cfg: c, client: client, queueURL: c.QueueURL, heartbeat: 300}
}

// Receive polls SQS with long-poll. ctx cancellation returns
// ErrNoJob (matching the in-memory dispatcher).
func (d *awsDispatcher) Receive(ctx context.Context) (*Job, error) {
	out, err := d.client.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
		QueueUrl:            aws.String(d.queueURL),
		MaxNumberOfMessages: 1,
		WaitTimeSeconds:     20,
		VisibilityTimeout:   d.heartbeat,
		AttributeNames:      []types.QueueAttributeName{"All"},
	})
	if err != nil {
		if ctx.Err() != nil {
			return nil, ErrNoJob
		}
		return nil, fmt.Errorf("dispatch.aws: ReceiveMessage: %w", err)
	}
	if len(out.Messages) == 0 {
		return nil, ErrNoJob
	}
	msg := out.Messages[0]
	job, err := decodeSQSMessage(msg)
	if err != nil {
		return nil, fmt.Errorf("dispatch.aws: decode: %w", err)
	}
	return job, nil
}

// Ack deletes the message from SQS.
func (d *awsDispatcher) Ack(ctx context.Context, j *Job) error {
	if j == nil {
		return errors.New("dispatch.aws: Ack requires non-nil Job")
	}
	receipt := string(j.ReceiptToken)
	if receipt == "" {
		return ErrJobNotInFlight
	}
	_, err := d.client.DeleteMessage(ctx, &sqs.DeleteMessageInput{
		QueueUrl:      aws.String(d.queueURL),
		ReceiptHandle: aws.String(receipt),
	})
	if err != nil {
		return fmt.Errorf("dispatch.aws: DeleteMessage: %w", err)
	}
	return nil
}

// Nack returns the message immediately by zeroing the visibility
// window. SQS then redelivers on the next Receive.
func (d *awsDispatcher) Nack(ctx context.Context, j *Job, reason string) error {
	if j == nil {
		return errors.New("dispatch.aws: Nack requires non-nil Job")
	}
	receipt := string(j.ReceiptToken)
	if receipt == "" {
		return ErrJobNotInFlight
	}
	_, err := d.client.ChangeMessageVisibility(ctx, &sqs.ChangeMessageVisibilityInput{
		QueueUrl:          aws.String(d.queueURL),
		ReceiptHandle:     aws.String(receipt),
		VisibilityTimeout: 0,
	})
	if err != nil {
		return fmt.Errorf("dispatch.aws: ChangeMessageVisibility: %w", err)
	}
	return nil
}

// Heartbeat extends the visibility window.
func (d *awsDispatcher) Heartbeat(ctx context.Context, j *Job) error {
	if j == nil {
		return errors.New("dispatch.aws: Heartbeat requires non-nil Job")
	}
	receipt := string(j.ReceiptToken)
	if receipt == "" {
		return ErrJobNotInFlight
	}
	_, err := d.client.ChangeMessageVisibility(ctx, &sqs.ChangeMessageVisibilityInput{
		QueueUrl:          aws.String(d.queueURL),
		ReceiptHandle:     aws.String(receipt),
		VisibilityTimeout: d.heartbeat,
	})
	if err != nil {
		return fmt.Errorf("dispatch.aws: ChangeMessageVisibility (heartbeat): %w", err)
	}
	return nil
}

func (d *awsDispatcher) Close() error { return nil }

// SubmitAWS publishes a Job to the queue. Used by the loadctl side
// of the AWS topology to enqueue a scenario.
func SubmitAWS(ctx context.Context, client SQSAPI, queueURL string, j *Job) error {
	body, err := encodeJob(j)
	if err != nil {
		return err
	}
	_, err = client.SendMessage(ctx, &sqs.SendMessageInput{
		QueueUrl:    aws.String(queueURL),
		MessageBody: aws.String(string(body)),
	})
	return err
}

// --- wire format -----------------------------------------------------

type sqsJobPayload struct {
	RunID       string `json:"run_id"`
	Scenario    string `json:"scenario"`
	ScriptURL   string `json:"script_url"`
	TargetURL   string `json:"target_url"`
	VUs         int    `json:"vus"`
	Rate        int    `json:"rate"`
	DurationNs  int64  `json:"duration_ns"`
	AuthBundle  string `json:"auth_bundle,omitempty"`
}

func encodeJob(j *Job) ([]byte, error) {
	p := sqsJobPayload{
		RunID:      j.RunID,
		Scenario:   j.Scenario,
		ScriptURL:  j.ScriptURL,
		TargetURL:  j.TargetURL,
		VUs:        j.VUs,
		Rate:       j.Rate,
		DurationNs: j.Duration.Nanoseconds(),
	}
	if len(j.AuthBundle) > 0 {
		p.AuthBundle = base64.StdEncoding.EncodeToString(j.AuthBundle)
	}
	return json.Marshal(p)
}

func decodeSQSMessage(m types.Message) (*Job, error) {
	if m.Body == nil {
		return nil, errors.New("empty message body")
	}
	var p sqsJobPayload
	if err := json.Unmarshal([]byte(*m.Body), &p); err != nil {
		return nil, err
	}
	auth, _ := base64.StdEncoding.DecodeString(p.AuthBundle)
	j := &Job{
		RunID:        p.RunID,
		Scenario:     p.Scenario,
		ScriptURL:    p.ScriptURL,
		TargetURL:    p.TargetURL,
		VUs:          p.VUs,
		Rate:         p.Rate,
		Duration:     time.Duration(p.DurationNs),
		AuthBundle:   auth,
		ReceiptToken: []byte(aws.ToString(m.ReceiptHandle)),
	}
	return j, nil
}

// Silence the dependency-pull only-used-in-comment linter complaint.
var _ = strconv.Itoa
