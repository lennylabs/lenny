// SPDX-License-Identifier: MIT

package dispatch

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"cloud.google.com/go/pubsub"
)

// PubSubClient is the subset of *pubsub.Client the gcpDispatcher uses.
// Exported so tests can plug a fake.
type PubSubClient interface {
	Subscription(name string) PubSubSubscription
	Topic(name string) PubSubTopic
	Close() error
}

// PubSubSubscription is the subset of *pubsub.Subscription the
// dispatcher uses.
type PubSubSubscription interface {
	Receive(ctx context.Context, f func(context.Context, PubSubMessage)) error
}

// PubSubTopic is the subset of *pubsub.Topic the SubmitGCP helper uses.
type PubSubTopic interface {
	Publish(ctx context.Context, msg *pubsub.Message) PubSubResult
	Stop()
}

// PubSubResult mirrors the synchronous side of *pubsub.PublishResult.
type PubSubResult interface {
	Get(ctx context.Context) (string, error)
}

// PubSubMessage is the receive-side message surface.
type PubSubMessage interface {
	Data() []byte
	ID() string
	Ack()
	Nack()
}

// gcpDispatcher consumes one Pub/Sub subscription. Pub/Sub does not
// have an explicit "receive one" API; we wrap the streaming
// subscriber in a channel-based front so the Dispatcher contract
// (Receive returns one Job at a time) is satisfied.
type gcpDispatcher struct {
	client PubSubClient
	sub    PubSubSubscription
	cfg    CloudConfig

	startOnce sync.Once
	inbox     chan inflightMsg
	stop      context.CancelFunc

	mu       sync.Mutex
	inFlight map[string]PubSubMessage
}

type inflightMsg struct {
	job *Job
	msg PubSubMessage
}

func newGCP(ctx context.Context, c CloudConfig) (Dispatcher, error) {
	if c.QueueURL == "" {
		return nil, errors.New("dispatch.gcp: CloudConfig.QueueURL (subscription) is required")
	}
	// QueueURL has the form projects/<p>/subscriptions/<s>.
	parts := strings.Split(c.QueueURL, "/")
	if len(parts) < 4 || parts[0] != "projects" || parts[2] != "subscriptions" {
		return nil, fmt.Errorf("dispatch.gcp: malformed QueueURL %q (want projects/<p>/subscriptions/<s>)", c.QueueURL)
	}
	project := parts[1]
	subName := parts[3]
	raw, err := pubsub.NewClient(ctx, project)
	if err != nil {
		return nil, fmt.Errorf("dispatch.gcp: NewClient: %w", err)
	}
	wrap := &pubsubClientAdapter{client: raw}
	return &gcpDispatcher{
		client:   wrap,
		sub:      wrap.Subscription(subName),
		cfg:      c,
		inbox:    make(chan inflightMsg, 32),
		inFlight: make(map[string]PubSubMessage),
	}, nil
}

// NewGCPWithClient constructs a gcpDispatcher with an injected client.
// Used by tests to plug a fake. subName is the bare subscription name
// (the trailing component of projects/<p>/subscriptions/<s>).
func NewGCPWithClient(c CloudConfig, client PubSubClient, subName string) Dispatcher {
	return &gcpDispatcher{
		client:   client,
		sub:      client.Subscription(subName),
		cfg:      c,
		inbox:    make(chan inflightMsg, 32),
		inFlight: make(map[string]PubSubMessage),
	}
}

func (d *gcpDispatcher) startStream(ctx context.Context) {
	d.startOnce.Do(func() {
		streamCtx, cancel := context.WithCancel(context.Background())
		d.stop = cancel
		go func() {
			_ = d.sub.Receive(streamCtx, func(_ context.Context, msg PubSubMessage) {
				job, err := decodeGCP(msg)
				if err != nil {
					msg.Nack()
					return
				}
				d.mu.Lock()
				d.inFlight[string(job.ReceiptToken)] = msg
				d.mu.Unlock()
				select {
				case d.inbox <- inflightMsg{job: job, msg: msg}:
				case <-streamCtx.Done():
					msg.Nack()
				}
			})
		}()
	})
}

func (d *gcpDispatcher) Receive(ctx context.Context) (*Job, error) {
	d.startStream(ctx)
	select {
	case <-ctx.Done():
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, ErrNoJob
		}
		return nil, ctx.Err()
	case m := <-d.inbox:
		return m.job, nil
	}
}

func (d *gcpDispatcher) Ack(ctx context.Context, j *Job) error {
	msg, err := d.takeInFlight(j)
	if err != nil {
		return err
	}
	msg.Ack()
	return nil
}

func (d *gcpDispatcher) Nack(ctx context.Context, j *Job, reason string) error {
	msg, err := d.takeInFlight(j)
	if err != nil {
		return err
	}
	msg.Nack()
	return nil
}

// Heartbeat is a no-op on Pub/Sub; ack deadline extension is
// automatic via the client library. Documented behaviour so callers
// can dispatch the same heartbeat loop across clouds.
func (d *gcpDispatcher) Heartbeat(ctx context.Context, j *Job) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.inFlight[string(j.ReceiptToken)]; !ok {
		return ErrJobNotInFlight
	}
	return nil
}

func (d *gcpDispatcher) Close() error {
	if d.stop != nil {
		d.stop()
	}
	return d.client.Close()
}

func (d *gcpDispatcher) takeInFlight(j *Job) (PubSubMessage, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if j == nil {
		return nil, errors.New("dispatch.gcp: nil Job")
	}
	key := string(j.ReceiptToken)
	msg, ok := d.inFlight[key]
	if !ok {
		return nil, ErrJobNotInFlight
	}
	delete(d.inFlight, key)
	return msg, nil
}

// SubmitGCP publishes a Job to topic.
func SubmitGCP(ctx context.Context, topic PubSubTopic, j *Job) error {
	body, err := encodeJob(j)
	if err != nil {
		return err
	}
	res := topic.Publish(ctx, &pubsub.Message{Data: body})
	_, err = res.Get(ctx)
	return err
}

func decodeGCP(msg PubSubMessage) (*Job, error) {
	if len(msg.Data()) == 0 {
		return nil, errors.New("empty pubsub message")
	}
	var p sqsJobPayload // wire format is identical
	if err := json.Unmarshal(msg.Data(), &p); err != nil {
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
		ReceiptToken: []byte(msg.ID()),
	}, nil
}

// --- adapters around the real pubsub types ---------------------------

type pubsubClientAdapter struct {
	client *pubsub.Client
}

func (a *pubsubClientAdapter) Subscription(name string) PubSubSubscription {
	return &pubsubSubAdapter{sub: a.client.Subscription(name)}
}
func (a *pubsubClientAdapter) Topic(name string) PubSubTopic {
	return &pubsubTopicAdapter{topic: a.client.Topic(name)}
}
func (a *pubsubClientAdapter) Close() error { return a.client.Close() }

type pubsubSubAdapter struct {
	sub *pubsub.Subscription
}

func (a *pubsubSubAdapter) Receive(ctx context.Context, f func(context.Context, PubSubMessage)) error {
	return a.sub.Receive(ctx, func(c context.Context, m *pubsub.Message) {
		f(c, &pubsubMsgAdapter{msg: m})
	})
}

type pubsubTopicAdapter struct {
	topic *pubsub.Topic
}

func (a *pubsubTopicAdapter) Publish(ctx context.Context, m *pubsub.Message) PubSubResult {
	return &pubsubResultAdapter{result: a.topic.Publish(ctx, m)}
}
func (a *pubsubTopicAdapter) Stop() { a.topic.Stop() }

type pubsubResultAdapter struct {
	result *pubsub.PublishResult
}

func (a *pubsubResultAdapter) Get(ctx context.Context) (string, error) { return a.result.Get(ctx) }

type pubsubMsgAdapter struct {
	msg *pubsub.Message
}

func (a *pubsubMsgAdapter) Data() []byte { return a.msg.Data }
func (a *pubsubMsgAdapter) ID() string   { return a.msg.ID }
func (a *pubsubMsgAdapter) Ack()         { a.msg.Ack() }
func (a *pubsubMsgAdapter) Nack()        { a.msg.Nack() }
