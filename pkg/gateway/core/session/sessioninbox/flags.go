// SPDX-License-Identifier: MIT

package sessioninbox

import (
	"flag"

	"github.com/lennylabs/lenny/pkg/common/envdefault"
)

// Flags holds the flags only the session inbox and its dead-letter queue read.
type Flags struct {
	// DurableInbox is the value of --messaging-durable-inbox.
	DurableInbox *bool

	// MaxInboxSize is the value of --messaging-max-inbox-size.
	MaxInboxSize *int

	// MaxDLQSize is the value of --messaging-max-dlq-size.
	MaxDLQSize *int
}

// RegisterFlags registers the flags of the session inbox and its dead-letter
// queue on fs and returns their values, which fs fills when it parses.
func RegisterFlags(fs *flag.FlagSet) *Flags {
	f := &Flags{}
	f.DurableInbox = fs.Bool("messaging-durable-inbox", envdefault.ParseBool("LENNY_MESSAGING_DURABLE_INBOX", false),
		"§7.2 durableInbox: back the session inbox with a Redis list (t:{tenant}:session:{id}:inbox) so undelivered inter-session messages survive coordinator failover. Requires Redis. Default false (in-memory inbox). Override via LENNY_MESSAGING_DURABLE_INBOX.")
	f.MaxInboxSize = fs.Int("messaging-max-inbox-size", envdefault.Int("LENNY_MESSAGING_MAX_INBOX_SIZE", 500),
		"§7.2 maxInboxSize: per-session inbox capacity before the oldest buffered message is evicted with a message_dropped(inbox_overflow) receipt. Override via LENNY_MESSAGING_MAX_INBOX_SIZE.")
	f.MaxDLQSize = fs.Int("messaging-max-dlq-size", envdefault.Int("LENNY_MESSAGING_MAX_DLQ_SIZE", 500),
		"§7.2 maxDLQSize: per-session dead-letter-queue capacity before the oldest entry is evicted with a message_dropped(dlq_overflow) receipt. Override via LENNY_MESSAGING_MAX_DLQ_SIZE.")
	return f
}
