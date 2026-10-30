// SPDX-License-Identifier: MIT

package dispatch

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

// fakeSQS records calls and serves canned responses.
type fakeSQS struct {
	mu        sync.Mutex
	queue     []types.Message
	deletedRH []string
	visChange []sqs.ChangeMessageVisibilityInput
	sent      []sqs.SendMessageInput
}

func (f *fakeSQS) ReceiveMessage(ctx context.Context, in *sqs.ReceiveMessageInput, _ ...func(*sqs.Options)) (*sqs.ReceiveMessageOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.queue) == 0 {
		return &sqs.ReceiveMessageOutput{}, nil
	}
	msg := f.queue[0]
	f.queue = f.queue[1:]
	return &sqs.ReceiveMessageOutput{Messages: []types.Message{msg}}, nil
}

func (f *fakeSQS) DeleteMessage(ctx context.Context, in *sqs.DeleteMessageInput, _ ...func(*sqs.Options)) (*sqs.DeleteMessageOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deletedRH = append(f.deletedRH, aws.ToString(in.ReceiptHandle))
	return &sqs.DeleteMessageOutput{}, nil
}

func (f *fakeSQS) ChangeMessageVisibility(ctx context.Context, in *sqs.ChangeMessageVisibilityInput, _ ...func(*sqs.Options)) (*sqs.ChangeMessageVisibilityOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.visChange = append(f.visChange, *in)
	return &sqs.ChangeMessageVisibilityOutput{}, nil
}

func (f *fakeSQS) SendMessage(ctx context.Context, in *sqs.SendMessageInput, _ ...func(*sqs.Options)) (*sqs.SendMessageOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, *in)
	return &sqs.SendMessageOutput{MessageId: aws.String("m-" + aws.ToString(in.QueueUrl))}, nil
}

func TestAWSReceiveDecodesJob(t *testing.T) {
	f := &fakeSQS{}
	job := &Job{RunID: "r1", Scenario: "session_throughput", VUs: 10, Duration: 30 * time.Second}
	body, err := encodeJob(job)
	if err != nil {
		t.Fatal(err)
	}
	f.queue = []types.Message{{
		Body:          aws.String(string(body)),
		ReceiptHandle: aws.String("rh-1"),
	}}

	d := NewAWSWithClient(CloudConfig{QueueURL: "q1"}, f)
	got, err := d.Receive(context.Background())
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if got.RunID != "r1" || got.Scenario != "session_throughput" {
		t.Errorf("decode mismatch: %+v", got)
	}
	if string(got.ReceiptToken) != "rh-1" {
		t.Errorf("receipt token = %q want rh-1", got.ReceiptToken)
	}
}

func TestAWSReceiveNoJob(t *testing.T) {
	f := &fakeSQS{}
	d := NewAWSWithClient(CloudConfig{QueueURL: "q1"}, f)
	_, err := d.Receive(context.Background())
	if !errors.Is(err, ErrNoJob) {
		t.Errorf("err = %v want ErrNoJob", err)
	}
}

func TestAWSAckDeletes(t *testing.T) {
	f := &fakeSQS{}
	d := NewAWSWithClient(CloudConfig{QueueURL: "q1"}, f)
	job := &Job{ReceiptToken: []byte("rh-2")}
	if err := d.Ack(context.Background(), job); err != nil {
		t.Fatalf("Ack: %v", err)
	}
	if len(f.deletedRH) != 1 || f.deletedRH[0] != "rh-2" {
		t.Errorf("DeleteMessage receipts = %v want [rh-2]", f.deletedRH)
	}
}

func TestAWSNackZerosVisibility(t *testing.T) {
	f := &fakeSQS{}
	d := NewAWSWithClient(CloudConfig{QueueURL: "q1"}, f)
	job := &Job{ReceiptToken: []byte("rh-3")}
	if err := d.Nack(context.Background(), job, "test"); err != nil {
		t.Fatalf("Nack: %v", err)
	}
	if len(f.visChange) != 1 {
		t.Fatalf("ChangeMessageVisibility calls = %d want 1", len(f.visChange))
	}
	if aws.ToInt32(&f.visChange[0].VisibilityTimeout) != 0 {
		t.Errorf("VisibilityTimeout = %d want 0", f.visChange[0].VisibilityTimeout)
	}
}

func TestAWSHeartbeatExtends(t *testing.T) {
	f := &fakeSQS{}
	d := NewAWSWithClient(CloudConfig{QueueURL: "q1"}, f).(*awsDispatcher)
	d.heartbeat = 120
	job := &Job{ReceiptToken: []byte("rh-4")}
	if err := d.Heartbeat(context.Background(), job); err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	if f.visChange[0].VisibilityTimeout != 120 {
		t.Errorf("Heartbeat VisibilityTimeout = %d want 120", f.visChange[0].VisibilityTimeout)
	}
}

func TestAWSSubmitEncodes(t *testing.T) {
	f := &fakeSQS{}
	job := &Job{RunID: "r5", Scenario: "delegation_fanout", VUs: 5}
	if err := SubmitAWS(context.Background(), f, "q1", job); err != nil {
		t.Fatalf("SubmitAWS: %v", err)
	}
	if len(f.sent) != 1 {
		t.Fatalf("SendMessage calls = %d want 1", len(f.sent))
	}
	if aws.ToString(f.sent[0].MessageBody) == "" {
		t.Error("MessageBody empty")
	}
}
