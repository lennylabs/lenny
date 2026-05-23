// SPDX-License-Identifier: MIT

package dispatch

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"cloud.google.com/go/pubsub"
)

// gcpSubmitter publishes jobs to a Pub/Sub topic.
type gcpSubmitter struct {
	client *pubsub.Client
	topic  PubSubTopic
}

func newGCPSubmitter(ctx context.Context, c CloudConfig) (Submitter, error) {
	// QueueURL has the form projects/<p>/topics/<t>.
	parts := strings.Split(c.QueueURL, "/")
	if len(parts) < 4 || parts[0] != "projects" || parts[2] != "topics" {
		return nil, fmt.Errorf("dispatch.gcp: malformed QueueURL %q (want projects/<p>/topics/<t>)", c.QueueURL)
	}
	project := parts[1]
	topicName := parts[3]
	client, err := pubsub.NewClient(ctx, project)
	if err != nil {
		return nil, fmt.Errorf("dispatch.gcp: NewClient: %w", err)
	}
	return &gcpSubmitter{client: client, topic: &pubsubTopicAdapter{topic: client.Topic(topicName)}}, nil
}

// NewGCPSubmitterWithTopic lets tests inject a fake topic.
func NewGCPSubmitterWithTopic(t PubSubTopic) Submitter {
	return &gcpSubmitter{topic: t}
}

func (s *gcpSubmitter) Submit(ctx context.Context, j *Job) error {
	if s.topic == nil {
		return errors.New("dispatch.gcp: topic is nil")
	}
	return SubmitGCP(ctx, s.topic, j)
}

func (s *gcpSubmitter) Close() error {
	if s.topic != nil {
		s.topic.Stop()
	}
	if s.client != nil {
		return s.client.Close()
	}
	return nil
}
