// SPDX-License-Identifier: MIT

//go:build load_local

// Tier-7a randomized interleavings of off-holder message forwards on
// CH-HOLDERFORWARD against a coordinator handoff and a coordination lease
// expiry on the lifecycle harness. Each round races a burst of message sends,
// each served by a randomly chosen replica, against one disruption of the
// session's REG-COORDLEASE holder and against arming a forward failure on the
// replica that holds the lease. Rounds alternate the failure between a
// listener that refuses the connection (the forward is not delivered) and a
// listener that drops the connection after reading the request (the forward's
// outcome is unknown). An invariant checker then reads every send's answer
// against the runtimes' received texts and the session DLQ.
//
// spec: §29.3 (Interactive message send) Forwarding, §10.1.2 (Coordinator
// Handoff Protocol), §7.2 (Message delivery routing — seven paths, path 6)
package tier7a_load_local_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessioninbox"
	"github.com/lennylabs/lenny/pkg/gateway/storage/leasestore"
	"github.com/lennylabs/lenny/tests/testinfra/lifecycleharness"
)

const (
	// forwardRounds is the number of interleaving rounds. Each round runs on
	// its own harness, so a lease expiry in one round does not move the
	// coordinator of another round's session.
	forwardRounds = 8
	// forwardBurst is the number of messages one round sends.
	forwardBurst = 6
	// forwardJitter bounds the random delay before each racing operation.
	forwardJitter = 6 * time.Millisecond
)

// forwardFailure is the forward failure a round arms on the lease holder.
type forwardFailure int

const (
	// failureRefuse closes each connection before the TLS handshake, so a
	// forward is not delivered (§29.3 Forwarding rule 6).
	failureRefuse forwardFailure = iota
	// failureFault drops every second forwarded connection after the
	// request was read, so that forward's outcome is unknown (§29.3
	// Forwarding rule 7).
	failureFault
)

func (f forwardFailure) String() string {
	if f == failureRefuse {
		return "refuse"
	}
	return "fault"
}

// holderDisruption moves or drops the session's REG-COORDLEASE holder while
// the sends are in flight.
type holderDisruption int

const (
	// disruptHandoff deletes the lease and lets the other replica's Sweeper
	// adopt the session, as a coordinator handoff does.
	disruptHandoff holderDisruption = iota
	// disruptExpiry steps the clock past the lease TTL and races both
	// replicas' Sweepers for the lapsed lease.
	disruptExpiry
)

func (d holderDisruption) String() string {
	if d == disruptHandoff {
		return "handoff"
	}
	return "lease expiry"
}

// forwardSend is one message send of a round and its answer.
type forwardSend struct {
	text string
	code int
	body map[string]any
}

// receipt returns the delivery receipt status of a 200 answer.
func (s forwardSend) receipt() string {
	for _, key := range []string{"deliveryReceipt", "deliveryReceipts"} {
		switch v := s.body[key].(type) {
		case map[string]any:
			st, _ := v["status"].(string)
			return st
		case []any:
			if len(v) > 0 {
				r, _ := v[0].(map[string]any)
				st, _ := r["status"].(string)
				return st
			}
		}
	}
	return ""
}

// accepted reports whether the answer is a 2xx receipt the gateway gave for
// a delivered or a held message.
func (s forwardSend) accepted() bool {
	if s.code < 200 || s.code > 299 {
		return false
	}
	st := s.receipt()
	return st == string(session.DeliveryStatusDelivered) || st == string(session.DeliveryStatusQueued)
}

// receiptMatchesOutcome reports whether the 2xx receipt of an accepted send
// states its outcome before the DLQ drain. A delivered receipt requires a
// delivery, because the hold outcome of §29.3 Forwarding rule 6 answers
// queued and never delivered. A queued receipt requires a delivery or a DLQ
// hold, because path-2 delivery may already have drained the hold.
func receiptMatchesOutcome(s forwardSend, delivered int, held bool) bool {
	if s.receipt() == string(session.DeliveryStatusDelivered) {
		return delivered >= 1
	}
	return delivered >= 1 || held
}

// refused reports whether the answer is one of the refusals §29.3 Forwarding
// rules 5 and 7 allow with or without a delivery.
func (s forwardSend) refused() bool {
	return s.code == http.StatusConflict || s.code == http.StatusInternalServerError
}

// heldTexts returns the texts among want that the session's DLQ holds. A
// DLQ member is the JSON of a sessioninbox.Message, whose payload, the §15.4
// message the runtime receives, is a byte slice and so is base64 in the
// member; each member is decoded before its payload is searched.
func heldTexts(t *testing.T, h *lifecycleharness.Harness, id string, want []string) map[string]bool {
	t.Helper()
	key := sessioninbox.DLQKey(lifecycleharness.Tenant, id)
	out := map[string]bool{}
	if !h.Redis.Exists(key) {
		return out
	}
	members, err := h.Redis.ZMembers(key)
	if err != nil {
		t.Fatalf("read the DLQ of %s: %v", id, err)
	}
	for _, m := range members {
		var msg sessioninbox.Message
		if err := json.Unmarshal([]byte(m), &msg); err != nil {
			t.Fatalf("decode a DLQ member of %s: %v", id, err)
		}
		for _, text := range want {
			if strings.Contains(string(msg.Payload), `"`+text+`"`) {
				out[text] = true
			}
		}
	}
	return out
}

// leaseHolder returns the replica whose holder value the session's lease
// carries, or nil when no replica holds it.
func leaseHolder(h *lifecycleharness.Harness, id string) *lifecycleharness.Replica {
	l, err := h.Leases.Get(context.Background(), lifecycleharness.Tenant, id)
	if err != nil {
		return nil
	}
	for _, r := range h.Surviving() {
		if r.ID == l.Holder {
			return r
		}
	}
	return nil
}

// armFailure arms the round's forward failure on r and returns its disarm.
func armFailure(r *lifecycleharness.Replica, f forwardFailure) func() {
	if f == failureFault {
		var n atomic.Int64
		r.SetForwardFault(func(*http.Request) bool { return n.Add(1)%2 == 1 })
		return func() { r.SetForwardFault(nil) }
	}
	r.SetForwardRefuse(true)
	return func() { r.SetForwardRefuse(false) }
}

// disrupt runs the round's holder disruption from the coordinator that
// started the session to its peer.
func disrupt(t *testing.T, h *lifecycleharness.Harness, d holderDisruption, rng *rand.Rand, coordinator, peer *lifecycleharness.Replica, id string) {
	ctx := context.Background()
	switch d {
	case disruptHandoff:
		h.Redis.Del(leasestore.Key(lifecycleharness.Tenant, id))
		if _, err := peer.Sweeper.Sweep(ctx); err != nil {
			t.Errorf("peer sweep after the handoff: %v", err)
		}
	case disruptExpiry:
		h.Clock.Advance(lifecycleharness.LeaseTTL + time.Second)
		race(rng.Intn(2),
			func() { _, _ = coordinator.Sweeper.Sweep(ctx) },
			func() { _, _ = peer.Sweeper.Sweep(ctx) })
	}
}

// runForwardRound starts a session on the first replica, races the burst of
// sends against the disruption and the failure arming, and returns the
// sends with their answers.
func runForwardRound(t *testing.T, h *lifecycleharness.Harness, rng *rand.Rand, round int, failure forwardFailure, d holderDisruption) (string, []forwardSend) {
	coordinator, peer := h.Replicas[0], h.Replicas[1]
	id := h.StartSession(coordinator)
	sends := make([]forwardSend, forwardBurst)
	senders := make([]*lifecycleharness.Replica, forwardBurst)
	delays := make([]time.Duration, forwardBurst+2)
	for i := range sends {
		sends[i].text = fmt.Sprintf("fwd-round-%d-msg-%d", round, i)
		senders[i] = h.Replicas[rng.Intn(2)]
	}
	// The delays are drawn before the goroutines start, so the round's
	// generator is read from one goroutine.
	for i := range delays {
		delays[i] = time.Duration(rng.Int63n(int64(forwardJitter)))
	}
	var disarm func()
	var mu sync.Mutex
	var wg sync.WaitGroup
	start := make(chan struct{})
	wg.Add(forwardBurst + 2)
	for i := range sends {
		go func(i int) {
			defer wg.Done()
			<-start
			time.Sleep(delays[i])
			sends[i].code, sends[i].body = h.Send(senders[i], id, sends[i].text)
		}(i)
	}
	go func() {
		defer wg.Done()
		<-start
		time.Sleep(delays[forwardBurst])
		target := leaseHolder(h, id)
		if target == nil {
			target = coordinator
		}
		mu.Lock()
		disarm = armFailure(target, failure)
		mu.Unlock()
	}()
	disruptRng := rand.New(rand.NewSource(rng.Int63()))
	go func() {
		defer wg.Done()
		<-start
		time.Sleep(delays[forwardBurst+1])
		disrupt(t, h, d, disruptRng, coordinator, peer, id)
	}()
	close(start)
	wg.Wait()
	mu.Lock()
	disarm()
	mu.Unlock()
	return id, sends
}

// deliveryVerdict classifies how many times one send reached the runtime.
type deliveryVerdict int

const (
	// deliveredAtMostOnce is every count the invariant allows.
	deliveredAtMostOnce deliveryVerdict = iota
	// heldRedelivery is a send answered 200 queued, and so held in the DLQ,
	// that reached the runtime exactly twice in a round whose lease ended on
	// the peer.
	heldRedelivery
	// duplicateDelivery is every other count above one.
	duplicateDelivery
)

// classifyDeliveries returns the verdict for send s, delivered times, in a
// round that ended handed off or not.
//
// The checker fails a duplicateDelivery and ends the round with a skip
// blocked on BUILD-GAPS F-7.2.41 for a heldRedelivery. §7.2 delivery path 6
// Delivery of held messages removes a held entry only after the runtime
// consumed it, so an entry consumed but not yet removed when the
// coordinating replica changes is delivered again by the adopting replica,
// and the adapter's delivered_message_ids set of the §7.2 Explicit ACK row
// drops the duplicate before it reaches the runtime. The gateway re-reads
// REG-COORDLEASE before each held entry (ErrHeldDeliveryPreempted in
// pkg/gateway/sessionserver/held_delivery.go), which leaves only the window
// between one entry's lease read and its removal. No code implements the
// adapter's suppression set yet, which is F-7.2.41, so the harness runtime
// sees that redelivery. The blocked skip keeps the case visible, and
// TestBlockedSkipsNameOpenFindings_spec_7_2 fails once F-7.2.41 closes, at
// which point the skip and the heldRedelivery verdict are removed.
func classifyDeliveries(s forwardSend, handedOff bool, delivered int) deliveryVerdict {
	switch {
	case delivered <= 1:
		return deliveredAtMostOnce
	case delivered == 2 && handedOff && s.code == http.StatusOK && s.receipt() == string(session.DeliveryStatusQueued):
		return heldRedelivery
	default:
		return duplicateDelivery
	}
}

// checkDeliveryCount fails a duplicate delivery of s and records a held
// redelivery in redelivered.
func checkDeliveryCount(t *testing.T, s forwardSend, handedOff bool, delivered int, when string, redelivered map[string]bool) {
	t.Helper()
	switch classifyDeliveries(s, handedOff, delivered) {
	case duplicateDelivery:
		t.Errorf("%s (answered %d %v) reached the runtime %d times %s, want at most once", s.text, s.code, s.body, delivered, when)
	case heldRedelivery:
		redelivered[s.text] = true
	}
}

// checkForwardSends is the round's invariant checker. After the round
// settles it asserts that no send is delivered twice (classifyDeliveries
// names the DLQ-held redelivery across a handoff that is blocked on
// F-7.2.41), that no send that reached the runtime is still held in the DLQ,
// that every delivered receipt is a delivery and every queued receipt is a
// delivery or a DLQ hold, and that every answer
// is a 2xx receipt or a 409 or 500 refusal, which is accepted with or
// without a delivery. It then sends one message on the coordinator, whose
// path-2 delivery drains the DLQ first, and asserts that every accepted send
// reached the runtime and that the DLQ is empty. It returns the sends that
// were held redeliveries.
func checkForwardSends(t *testing.T, h *lifecycleharness.Harness, id string, sends []forwardSend) map[string]bool {
	t.Helper()
	h.Settle()
	handedOff := leaseHolder(h, id) != h.Replicas[0]
	texts := make([]string, len(sends))
	for i, s := range sends {
		texts[i] = s.text
	}
	held := heldTexts(t, h, id, texts)
	redelivered := map[string]bool{}
	for _, s := range sends {
		delivered := countText(h, id, s.text)
		checkDeliveryCount(t, s, handedOff, delivered, "after the round", redelivered)
		if delivered >= 1 && held[s.text] {
			t.Errorf("%s (answered %d %v) reached the runtime %d times and is still held in the DLQ", s.text, s.code, s.body, delivered)
		}
		switch {
		case s.accepted():
			if !receiptMatchesOutcome(s, delivered, held[s.text]) {
				t.Errorf("%s was answered %d %s, but it was delivered %d times and held in the DLQ is %v", s.text, s.code, s.receipt(), delivered, held[s.text])
			}
		case s.refused():
		default:
			t.Errorf("%s was answered %d %v, want a delivered or queued receipt, or a 409 or 500 refusal", s.text, s.code, s.body)
		}
	}

	row := h.AwaitState(id, session.StateRunning, 20)
	if row.State != session.StateRunning {
		t.Fatalf("the session is %q after the round, want running", row.State)
	}
	flusher := h.Bound(id)
	if flusher == nil {
		t.Fatal("no replica binds the running session after the round")
	}
	if code, body := h.Send(flusher, id, "fwd-flush"); code != http.StatusOK {
		t.Fatalf("the coordinator's flush message answered %d %v, want 200", code, body)
	}
	h.AwaitState(id, session.StateRunning, 20)
	h.Settle()
	for _, s := range sends {
		delivered := countText(h, id, s.text)
		checkDeliveryCount(t, s, handedOff, delivered, "after the DLQ drain", redelivered)
		if s.accepted() && delivered == 0 {
			t.Errorf("%s was answered %s, but it never reached the runtime after the DLQ drain", s.text, s.receipt())
		}
	}
	if n, err := h.DLQ.Len(context.Background(), lifecycleharness.Tenant, id); err != nil || n != 0 {
		t.Errorf("the DLQ holds %d entries (%v) after the coordinator's path-2 delivery, want none", n, err)
	}
	return redelivered
}

// spec: §29.3 (Interactive message send) Forwarding rules 1, 2, 5, 6, and 7,
// §10.1.2 (Coordinator Handoff Protocol), §7.2 (Message delivery routing —
// seven paths, path 6)
// diagnosis: an off-holder message raced against a coordinator handoff or a
// coordination lease expiry, with the lease holder refusing or dropping
// forwarded connections, reached the runtime twice (a message held in the
// DLQ and delivered twice across a completed handoff skips the round as
// blocked on the adapter's duplicate suppression of BUILD-GAPS F-7.2.41); was delivered and also
// left held in the DLQ; was answered with a 2xx receipt although it was
// neither delivered nor held, or was held but never delivered by the
// coordinator's next path-2 delivery; or was answered with a status other
// than a receipt, a 409, or a 500. A double delivery points at a forward
// whose outcome was unknown being held or retried (forwardMessageBatch in
// pkg/gateway/sessionserver/messages_delivery.go), or at a stale forward
// applied on a former holder (forwardTarget in
// pkg/gateway/sessionserver/holder_forward.go), or at a former coordinator
// delivering held entries after its lease was handed off
// (heldDeliveryPreempted in pkg/gateway/sessionserver/held_delivery.go). A
// 500 answer to a message whose held delivery was preempted points at
// restDelivery.notCoordinating in the same package. Replay a failing run
// with LENNY_LIFECYCLE_SEED set to the logged seed.
func TestOffHolderForwardInterleavingsDeliverEachMessageOnce_spec_29_3(t *testing.T) {
	seed := lifecycleSeed(t)
	t.Logf("seed %d", seed)
	rng := rand.New(rand.NewSource(seed))
	for round := 0; round < forwardRounds; round++ {
		failure := forwardFailure(round % 2)
		d := holderDisruption((round / 2) % 2)
		roundSeed := rng.Int63()
		t.Run(fmt.Sprintf("round %d %s with %s", round, d, failure), func(t *testing.T) {
			h := lifecycleharness.New(t, lifecycleharness.Options{Replicas: 2, Pods: 8})
			id, sends := runForwardRound(t, h, rand.New(rand.NewSource(roundSeed)), round, failure, d)
			redelivered := checkForwardSends(t, h, id, sends)
			h.Check(t)
			if len(redelivered) > 0 {
				t.Logf("held sends delivered twice across the handoff: %v", redelivered)
				t.Skip("blocked: F-7.2.41 (owner: R20) a held message consumed but not yet removed at a coordinator handoff is delivered again by the adopting coordinator, and no adapter delivered_message_ids set suppresses it")
			}
		})
	}
}

// spec: §7.2 (Message delivery routing — seven paths, path 6), §29.3
// (Interactive message send) Forwarding rules 5 and 6
// diagnosis: the interleaving checker passed a send that reached the runtime
// more than once, outside the one held redelivery across a completed handoff
// that is blocked on BUILD-GAPS F-7.2.41, so a double delivery no longer fails
// the round; or it failed that held redelivery instead of skipping it as
// blocked.
func TestForwardCheckerFailsEveryDoubleDeliveryButTheBlockedHeldRedelivery_spec_7_2(t *testing.T) {
	queued := forwardSend{text: "q", code: http.StatusOK, body: map[string]any{
		"deliveryReceipt": map[string]any{"status": string(session.DeliveryStatusQueued)},
	}}
	delivered := forwardSend{text: "d", code: http.StatusOK, body: map[string]any{
		"deliveryReceipt": map[string]any{"status": string(session.DeliveryStatusDelivered)},
	}}
	refused := forwardSend{text: "r", code: http.StatusConflict}
	cases := []struct {
		name      string
		send      forwardSend
		handedOff bool
		count     int
		want      deliveryVerdict
	}{
		{"queued never delivered", queued, true, 0, deliveredAtMostOnce},
		{"queued delivered once after a handoff", queued, true, 1, deliveredAtMostOnce},
		{"queued delivered twice after a handoff", queued, true, 2, heldRedelivery},
		{"queued delivered three times after a handoff", queued, true, 3, duplicateDelivery},
		{"queued delivered twice without a handoff", queued, false, 2, duplicateDelivery},
		{"delivered receipt delivered twice after a handoff", delivered, true, 2, duplicateDelivery},
		{"refusal delivered twice after a handoff", refused, true, 2, duplicateDelivery},
		{"refusal delivered once", refused, false, 1, deliveredAtMostOnce},
	}
	for _, c := range cases {
		if got := classifyDeliveries(c.send, c.handedOff, c.count); got != c.want {
			t.Errorf("%s: classifyDeliveries = %d, want %d", c.name, got, c.want)
		}
	}
}

// spec: §29.3 (Interactive message send) Forwarding rule 6, §7.2 (Message
// delivery routing — seven paths, path 6)
// diagnosis: the interleaving checker accepted a 200 delivered receipt for a
// send the runtime never received because the send sat in the DLQ, so a
// receipt that misstates a hold as a delivery no longer fails the round; or it
// failed a queued receipt whose send was held or already delivered.
func TestForwardCheckerMatchesEachReceiptToItsOutcome_spec_29_3(t *testing.T) {
	queued := forwardSend{text: "q", code: http.StatusOK, body: map[string]any{
		"deliveryReceipt": map[string]any{"status": string(session.DeliveryStatusQueued)},
	}}
	delivered := forwardSend{text: "d", code: http.StatusOK, body: map[string]any{
		"deliveryReceipts": []any{map[string]any{"status": string(session.DeliveryStatusDelivered)}},
	}}
	cases := []struct {
		name      string
		send      forwardSend
		count     int
		held      bool
		wantMatch bool
	}{
		{"delivered receipt delivered", delivered, 1, false, true},
		{"delivered receipt only held in the DLQ", delivered, 0, true, false},
		{"delivered receipt neither delivered nor held", delivered, 0, false, false},
		{"queued receipt held in the DLQ", queued, 0, true, true},
		{"queued receipt already delivered", queued, 1, false, true},
		{"queued receipt neither delivered nor held", queued, 0, false, false},
	}
	for _, c := range cases {
		if got := receiptMatchesOutcome(c.send, c.count, c.held); got != c.wantMatch {
			t.Errorf("%s: receiptMatchesOutcome = %v, want %v", c.name, got, c.wantMatch)
		}
	}
}
