// SPDX-License-Identifier: MIT

package runtime

import (
	"context"
	"io"
	"log"
	"net"
	"os"
	"time"
)

// integrationLevel is the §15.4.3 integration level the SDK runs at.
type integrationLevel int

const (
	levelBasic integrationLevel = iota
	levelStandard
	levelFull
)

// config is the resolved Run configuration. It is assembled from the
// Option values passed to Run.
type config struct {
	level integrationLevel

	socketTransport bool
	dialTimeout     time.Duration

	manifestPath    string
	credentialsPath string

	reader io.Reader
	writer io.Writer

	lifecycleHooks lifecycleHooks

	logger func(format string, args ...any)
}

// defaultConfig is the Basic-level configuration: stdin/stdout with
// socket-transport fallback, the standard §4.7 manifest and credential
// paths, and stderr diagnostics.
func defaultConfig() config {
	return config{
		level:           levelBasic,
		socketTransport: true,
		dialTimeout:     5 * time.Second,
		manifestPath:    envOr(manifestEnvVar, defaultManifestPath),
		credentialsPath: defaultCredentialsPath,
		logger:          func(f string, a ...any) { log.Printf(f, a...) },
	}
}

// logf writes a diagnostic line through the configured logger.
func (c config) logf(format string, args ...any) {
	if c.logger != nil {
		c.logger(format, args...)
	}
}

// Option configures Run. Options compose; later options override
// earlier ones.
type Option func(*config)

// WithStandardLevel runs the SDK at the §15.4.3 Standard level. The SDK
// reads the adapter manifest, dials the platform MCP server and every
// connector MCP server with the manifest-nonce handshake, and exposes
// the §8.5 platform tool helpers through the Tools value. A manifest
// without a platform MCP server is a hard error at this level.
func WithStandardLevel() Option {
	return func(c *config) {
		if c.level < levelStandard {
			c.level = levelStandard
		}
	}
}

// WithFullLevel runs the SDK at the §15.4.3 Full level. It implies
// Standard level and additionally opens the lifecycle channel,
// completing the lifecycle_capabilities / lifecycle_support handshake
// and surfacing checkpoint, interrupt, credential rotation, and deadline
// events.
func WithFullLevel() Option {
	return func(c *config) { c.level = levelFull }
}

// WithSocketTransport enables or disables the §4.7 abstract-Unix-socket
// transport fallback. When enabled (the default) and LENNY_ADAPTER_SOCKET
// is set, Run dials that socket instead of using stdin/stdout.
func WithSocketTransport(enabled bool) Option {
	return func(c *config) { c.socketTransport = enabled }
}

// WithManifestPath overrides the §4.7 adapter manifest path. The default
// is the LENNY_ADAPTER_MANIFEST environment variable when set, otherwise
// /run/lenny/adapter-manifest.json.
func WithManifestPath(path string) Option {
	return func(c *config) { c.manifestPath = path }
}

// WithCredentialsPath overrides the §4.7 runtime credential file path.
// The default is /run/lenny/credentials.json.
func WithCredentialsPath(path string) Option {
	return func(c *config) { c.credentialsPath = path }
}

// WithLogger sets the diagnostic sink for SDK-internal messages
// (unknown frame types, handler errors). The default writes to the
// standard logger. Pass nil to silence diagnostics.
func WithLogger(logf func(format string, args ...any)) Option {
	return func(c *config) { c.logger = logf }
}

// WithStreams overrides the §15.4.1 byte transport with explicit
// streams. It is intended for in-process testing; production runtimes
// use the default stdin/stdout or socket transport.
func WithStreams(r io.Reader, w io.Writer) Option {
	return func(c *config) {
		c.reader = r
		c.writer = w
	}
}

// envOr returns the environment value of key, or fallback when unset.
func envOr(key, fallback string) string {
	if v := osGetenv(key); v != "" {
		return v
	}
	return fallback
}

// --- net indirection ---------------------------------------------------
//
// dialContext and netConn are package-level indirections over net so the
// socket dial path is exercised by tests with an in-memory listener.

// netConn is the subset of net.Conn the SDK uses.
type netConn = net.Conn

// dialContext dials a network address. It is a variable so tests can
// substitute an in-memory transport.
var dialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, network, address)
}

// osGetenv is a variable indirection over os.Getenv for tests.
var osGetenv = os.Getenv
