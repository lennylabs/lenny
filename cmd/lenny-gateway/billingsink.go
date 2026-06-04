// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"log"

	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/billingsink"
)

// billingSinkDeadLetter logs a §11.2.1 dead-lettered billing-event
// delivery at CRITICAL. A dead-letter means a sink exhausted its retry
// budget; the event is durably in Postgres, so the loss is delivery-side
// only and an operator reconciles via the §15.1 metering replay API.
func billingSinkDeadLetter(sink string, meta billingsink.EventMeta, _ []byte, err error) {
	log.Printf("CRITICAL lenny-gateway: §11.2.1 billing sink %q dead-lettered event tenant=%s seq=%d type=%s: %v",
		sink, meta.TenantID, meta.SequenceNumber, meta.EventType, err)
}

// buildBillingPublisher assembles the §11.2.1 billing-event delivery
// fan-out from the configured sinks. V1 wires the webhook sink; the
// message-queue sink (SQS / Pub/Sub / Kafka) is available in
// pkg/gateway/billingsink and plugs in once a broker QueuePublisher is
// supplied. An empty configuration returns a nil publisher so the
// gateway leaves the billing write path unwrapped. spec: §11.2.1 lines
// 132-138. F-11.2.14.
func buildBillingPublisher(webhookURL string, webhookSecret []byte) (*billingsink.Publisher, error) {
	if webhookURL == "" {
		return nil, nil
	}
	hook, err := billingsink.NewWebhookSink(billingsink.WebhookOptions{
		URL:        webhookURL,
		Secret:     webhookSecret,
		DeadLetter: billingSinkDeadLetter,
	})
	if err != nil {
		return nil, err
	}
	return billingsink.NewPublisher([]billingsink.Sink{hook}, func(sink string, meta billingsink.EventMeta, err error) {
		log.Printf("lenny-gateway: §11.2.1 billing sink %q delivery failed tenant=%s seq=%d: %v",
			sink, meta.TenantID, meta.SequenceNumber, err)
	}), nil
}

// approverWebhookNotifier adapts a billingsink.WebhookSink to the
// admin.ApproverNotifier interface so the §11.2.1 dual-control approval
// notification rides the same HMAC-signed, retried, dead-lettered
// webhook delivery as billing events. spec: §11.2.1 line 175. F-11.2.14.
type approverWebhookNotifier struct {
	sink *billingsink.WebhookSink
}

// NotifyApprovers implements admin.ApproverNotifier.
func (a approverWebhookNotifier) NotifyApprovers(ctx context.Context, payload []byte) error {
	return a.sink.Deliver(ctx, payload, billingsink.EventMeta{EventType: "billing.correction_approval_requested"})
}

// buildApproverNotifier builds the §11.2.1 approver-notification channel
// from billing.approverNotificationWebhook. A nil return leaves the
// channel unconfigured.
func buildApproverNotifier(webhookURL string, webhookSecret []byte) (admin.ApproverNotifier, error) {
	if webhookURL == "" {
		return nil, nil
	}
	hook, err := billingsink.NewWebhookSink(billingsink.WebhookOptions{
		URL:        webhookURL,
		Secret:     webhookSecret,
		DeadLetter: billingSinkDeadLetter,
	})
	if err != nil {
		return nil, err
	}
	return approverWebhookNotifier{sink: hook}, nil
}
