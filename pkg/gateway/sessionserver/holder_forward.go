// SPDX-License-Identifier: MIT

package sessionserver

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/http/httptrace"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/lennylabs/lenny/pkg/adapter/gatewaycontrol"
	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/gatewaycontrol/platformtools"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcp"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	idemmw "github.com/lennylabs/lenny/pkg/gateway/middleware/idempotency"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/mtls/certreload"
)

// HolderForwardDialBound bounds the TCP dial and the TLS handshake of one
// forward together. spec: §10.1.1 (Stateless Replicas and Per-Session
// Coordination) Replica address — "The forwarding replica bounds the TCP
// dial and the TLS handshake together at 2 seconds."
const HolderForwardDialBound = 2 * time.Second

// CallingSessionHeader carries the calling session's identifier on a
// forwarded platform tool call, which reached the serving replica over
// LNK-GWCONTROL with no client credential. Only the forward listener reads
// it. spec: §29.3 (Interactive message send) Forwarding rule 3.
const CallingSessionHeader = "X-Lenny-Calling-Session"

// forwardNoStoreHeader is the gateway-internal response mark the forward
// listener adds when the coordinator's handler refused the forward under
// §29.3 Forwarding rule 5. The forwarding replica strips it and keeps the
// refusal out of its §11.5 idempotency cache.
const forwardNoStoreHeader = "X-Lenny-Forward-No-Store"

// holderTokenBytes is the size of the random process token in a holder value.
const holderTokenBytes = 8

// HolderValue builds the REG-COORDLEASE holder value
// `<replica identity>.<process token>@<address>` for a replica whose forward
// listener is reachable at advertiseAddr. The process token is 8 bytes from
// crypto/rand, hex-encoded, drawn on every call, so a caller builds the value
// once at process start. advertiseAddr is host:port; one enclosing pair of
// brackets on the host is accepted and the address is rebuilt with
// net.JoinHostPort, so an unbracketed IPv6 pod IP gains its brackets.
//
// spec: §10.1.1 (Stateless Replicas and Per-Session Coordination) Replica
// address
func HolderValue(replicaID, advertiseAddr string) (string, error) {
	if replicaID == "" {
		return "", errors.New("holder value: replica identity is empty")
	}
	addr, err := normalizeForwardAddress(advertiseAddr)
	if err != nil {
		return "", err
	}
	var token [holderTokenBytes]byte
	if _, err := rand.Read(token[:]); err != nil {
		return "", fmt.Errorf("holder value: draw process token: %w", err)
	}
	return replicaID + "." + hex.EncodeToString(token[:]) + "@" + addr, nil
}

// normalizeForwardAddress validates a host:port forward address and rebuilds
// it in canonical form. It splits at the last colon, strips one enclosing
// pair of brackets from the host, and requires a non-empty host with no
// bracket and a decimal port from 1 to 65535.
func normalizeForwardAddress(addr string) (string, error) {
	i := strings.LastIndex(addr, ":")
	if i < 0 {
		return "", fmt.Errorf("forward address %q has no port", addr)
	}
	host, port := addr[:i], addr[i+1:]
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		host = host[1 : len(host)-1]
	}
	if host == "" || strings.ContainsAny(host, "[]") {
		return "", fmt.Errorf("forward address %q has no valid host", addr)
	}
	n, err := parsePort(port)
	if err != nil {
		return "", fmt.Errorf("forward address %q: %w", addr, err)
	}
	return net.JoinHostPort(host, strconv.Itoa(n)), nil
}

// parsePort parses a decimal port from 1 to 65535.
func parsePort(port string) (int, error) {
	if port == "" || strings.Trim(port, "0123456789") != "" {
		return 0, fmt.Errorf("port %q is not a decimal number", port)
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return 0, fmt.Errorf("port %q is outside 1 to 65535", port)
	}
	return n, nil
}

// HolderAddress returns the forward address a REG-COORDLEASE holder value
// carries: the text after its last `@`. It returns false when the value has
// no `@`, as a bare replica identity has none, or when that text is not a
// host:port with a non-empty host and port.
//
// spec: §10.1.1 (Stateless Replicas and Per-Session Coordination) Replica
// address
func HolderAddress(holder string) (string, bool) {
	i := strings.LastIndex(holder, "@")
	if i < 0 {
		return "", false
	}
	addr := holder[i+1:]
	host, port, err := net.SplitHostPort(addr)
	if err != nil || host == "" || port == "" {
		return "", false
	}
	return addr, true
}

// ForwardKind names the forwarding row of §29.3 a request falls under. It
// selects the session states in which the request is forwarded and how a
// forward that is not delivered is answered.
type ForwardKind int

const (
	// ForwardKindTerminate is a terminate or a delete.
	ForwardKindTerminate ForwardKind = iota + 1
	// ForwardKindMessage is a message request in which a message carries
	// inReplyTo. A forward that is not delivered fails closed.
	ForwardKindMessage
	// ForwardKindMessageNoReplyTo is a message request in which no message
	// carries inReplyTo. A forward that is not delivered is held by the
	// caller in the session DLQ (§29.3 Forwarding rule 6).
	ForwardKindMessageNoReplyTo
	// ForwardKindInterrupt is an interrupt.
	ForwardKindInterrupt
	// ForwardKindUpload is a mid-session upload.
	ForwardKindUpload
	// ForwardKindResume is a held-pod resume.
	ForwardKindResume
)

// ForwardOutcome is the result of a forward attempt.
type ForwardOutcome int

const (
	// ForwardLocal means this replica serves the request itself.
	ForwardLocal ForwardOutcome = iota + 1
	// ForwardDelivered means the coordinator answered and its response
	// was returned to the caller.
	ForwardDelivered
	// ForwardNotDelivered means the coordinator was not reached: no
	// readable holder value, no address, no client, or a failed dial or
	// handshake (§29.3 Forwarding rule 6).
	ForwardNotDelivered
	// ForwardUnknown means the forward failed after the connection to the
	// coordinator was established (§29.3 Forwarding rule 7).
	ForwardUnknown
)

// forwardDial is forwardTarget's internal answer that the request goes to
// the coordinator at the returned address. No caller ever receives it.
const forwardDial ForwardOutcome = 0

// stateSet is a set of persisted session states.
type stateSet map[session.State]bool

// terminateForwardStates are the states in which a terminate or a delete is
// forwarded. suspended counts only while the row names a pod, which
// forwardGated checks.
var terminateForwardStates = stateSet{
	session.StateStarting:             true,
	session.StateRunning:              true,
	session.StateInputRequired:        true,
	session.StateResumePending:        true,
	session.StateResuming:             true,
	session.StateAwaitingClientAction: true,
	session.StateSuspended:            true,
}

// messageForwardStates are the states in which a message or an interrupt is
// forwarded.
var messageForwardStates = stateSet{
	session.StateRunning:       true,
	session.StateInputRequired: true,
}

// forwardStates holds, for each kind, the persisted row states of the §29.3
// rows in which the session holds REG-COORDLEASE. The lookup matches the row
// value literally, so input_required and resuming are listed where a §29.3
// row names only running or resume_pending.
//
// spec: §29.3 (Interactive message send), §10.1.1 (Stateless Replicas and
// Per-Session Coordination)
var forwardStates = map[ForwardKind]stateSet{
	ForwardKindTerminate:        terminateForwardStates,
	ForwardKindMessage:          messageForwardStates,
	ForwardKindMessageNoReplyTo: messageForwardStates,
	ForwardKindInterrupt:        messageForwardStates,
	ForwardKindUpload:           {session.StateRunning: true},
	ForwardKindResume:           {session.StateSuspended: true},
}

// forwardGated reports whether row is in a state for which a §29.3 row of
// kind requires a forward. A suspended session is forwarded only while it
// holds its pod.
func (s *Server) forwardGated(row sessionstore.Session, kind ForwardKind) bool {
	if !forwardStates[kind][row.State] {
		return false
	}
	return row.State != session.StateSuspended || s.holdsPod(row)
}

type forwardedCtxKey struct{}

// WithForwarded marks ctx as carrying a request the inter-replica forward
// listener received. Only that listener sets the mark; no header carries it.
//
// spec: §29.3 (Interactive message send) Forwarding rules 4 and 5
func WithForwarded(ctx context.Context) context.Context {
	return context.WithValue(ctx, forwardedCtxKey{}, true)
}

// Forwarded reports whether ctx carries a forwarded request. A forwarded
// request is never forwarded again, and the coordinator does not apply the
// §11.1 rate limits, the §11.5 idempotency handling, or the §4.8
// PreToolResult phase to it.
//
// spec: §29.3 (Interactive message send) Forwarding rule 4
func Forwarded(ctx context.Context) bool {
	v, _ := ctx.Value(forwardedCtxKey{}).(bool)
	return v
}

// forwardTarget decides where a request of kind on row runs. It returns
// ForwardLocal when this replica serves it, ForwardNotDelivered when the
// coordinator cannot be reached, and otherwise the coordinator's address with
// forwardDial.
//
// spec: §29.3 (Interactive message send) Forwarding rules 1, 5, and 6,
// §10.1.1 (Stateless Replicas and Per-Session Coordination) Replica address
func (s *Server) forwardTarget(ctx context.Context, row sessionstore.Session, kind ForwardKind) (string, ForwardOutcome) {
	if !s.forwardGated(row, kind) {
		return "", ForwardLocal
	}
	if Forwarded(ctx) {
		// Rule 5: a forwarded request runs only on the holder and is
		// never forwarded again.
		if s.coordinatesSession(ctx, row.TenantID, row.ID) {
			return "", ForwardLocal
		}
		return "", ForwardNotDelivered
	}
	if s.coordinatesEverySession() {
		return "", ForwardLocal
	}
	lease, err := s.leaseStore.Get(ctx, row.TenantID, row.ID)
	if err != nil || lease.Holder == "" {
		return "", ForwardNotDelivered
	}
	if lease.Holder == s.replicaID {
		return "", ForwardLocal
	}
	addr, ok := HolderAddress(lease.Holder)
	if !ok || s.holderForward == nil {
		return "", ForwardNotDelivered
	}
	return addr, forwardDial
}

// forwardToHolder forwards the request r on row to the session's
// coordinating replica when a §29.3 row of kind requires it, and writes the
// coordinator's response, or the refusal, to w. body is the request body:
// the bytes the caller already read, or the unread r.Body. A caller that
// receives any outcome other than ForwardLocal returns at once, except on
// ForwardNotDelivered for ForwardKindMessageNoReplyTo, where nothing was
// written and the caller holds the message.
//
// spec: §29.3 (Interactive message send) Forwarding, §10.1.1 (Stateless
// Replicas and Per-Session Coordination) Replica address
func (s *Server) forwardToHolder(w http.ResponseWriter, r *http.Request, row sessionstore.Session, kind ForwardKind, body io.Reader) ForwardOutcome {
	ctx := r.Context()
	addr, outcome := s.forwardTarget(ctx, row, kind)
	if outcome != forwardDial {
		if outcome == ForwardNotDelivered {
			s.refuseForward(w, ctx, row, kind, outcome)
		}
		return outcome
	}
	req, err := newForwardRequest(ctx, r.Method, "https://"+addr+r.URL.RequestURI(), body, r.ContentLength, r.Header)
	if err != nil {
		s.refuseForward(w, ctx, row, kind, ForwardNotDelivered)
		return ForwardNotDelivered
	}
	resp, outcome := s.sendForward(req)
	if outcome != ForwardDelivered {
		s.refuseForward(w, ctx, row, kind, outcome)
		return outcome
	}
	defer func() { _ = resp.Body.Close() }()
	return copyForwardResponse(ctx, w, resp)
}

// refuseForward answers a forward that was not delivered or whose outcome is
// unknown with 409 TARGET_NOT_READY carrying currentState, and keeps the
// refusal out of the §11.5 idempotency cache. A not-delivered message with
// no inReplyTo is left to its caller, which holds it.
//
// spec: §29.3 (Interactive message send) Forwarding rules 6 and 7, §11.5
// (Idempotency) Mechanism
func (s *Server) refuseForward(w http.ResponseWriter, ctx context.Context, row sessionstore.Session, kind ForwardKind, outcome ForwardOutcome) {
	if outcome == ForwardNotDelivered && kind == ForwardKindMessageNoReplyTo {
		return
	}
	idemmw.DoNotStore(ctx)
	s.writeError(w, http.StatusConflict, "TARGET_NOT_READY",
		"the session's coordinating gateway replica could not be reached; retry the request",
		map[string]any{"currentState": string(row.State)})
}

// forwardRequestHeaders are the client headers a forwarded request carries
// besides the credential. spec: §29.3 Forwarding rule 2.
var forwardRequestHeaders = []string{"Content-Type", "Accept"}

// newForwardRequest builds the outbound forward of a request to url on ctx.
// It carries the method, the target, the body, the content headers, Accept,
// and the client credential the auth middleware recorded on ctx, or, for a
// platform tool call from a session's pod, the calling session's identifier.
// It carries no other client header. contentLength is used when body is not
// a buffer whose length the request derives itself.
//
// spec: §29.3 (Interactive message send) Forwarding rules 2 and 3
func newForwardRequest(ctx context.Context, method, url string, body io.Reader, contentLength int64, src http.Header) (*http.Request, error) {
	if body == nil {
		body = http.NoBody
	}
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, fmt.Errorf("build forward request: %w", err)
	}
	switch body.(type) {
	case *bytes.Reader, *bytes.Buffer, *strings.Reader:
		// NewRequestWithContext derived the length from the buffer.
	default:
		if body != http.NoBody {
			req.ContentLength = contentLength
		}
	}
	for _, name := range forwardRequestHeaders {
		if v := src.Get(name); v != "" {
			req.Header.Set(name, v)
		}
	}
	setForwardIdentity(ctx, req.Header)
	return req, nil
}

// setForwardIdentity writes the credential the auth middleware recorded on
// ctx to h. With no credential, a principal whose caller type is agent
// contributes its session identifier in CallingSessionHeader instead.
func setForwardIdentity(ctx context.Context, h http.Header) {
	if cred, ok := authmw.CredentialFrom(ctx); ok {
		if cred.Authorization != "" {
			h.Set("Authorization", cred.Authorization)
		}
		for name, values := range cred.DevHeaders {
			h[name] = append([]string(nil), values...)
		}
		return
	}
	if p, ok := authmw.FromContext(ctx); ok && p.CallerType == platformtools.AgentCallerType && p.SessionID != "" {
		h.Set(CallingSessionHeader, p.SessionID)
	}
}

// sendForward sends req on the shared client and classifies a failure. A
// failure before the client obtained a connection is not delivered; a
// failure after it, including one on a reused keep-alive connection, has an
// unknown outcome. The helper never retries.
//
// spec: §29.3 (Interactive message send) Forwarding rules 6, 7, and 8
func (s *Server) sendForward(req *http.Request) (*http.Response, ForwardOutcome) {
	var connected atomic.Bool
	trace := &httptrace.ClientTrace{GotConn: func(httptrace.GotConnInfo) { connected.Store(true) }}
	req = req.WithContext(httptrace.WithClientTrace(req.Context(), trace))
	resp, err := s.holderForward.Do(req)
	if err != nil {
		if connected.Load() {
			return nil, ForwardUnknown
		}
		return nil, ForwardNotDelivered
	}
	return resp, ForwardDelivered
}

// hopByHopHeaders are the RFC 9110 connection-scoped headers a forward does
// not copy back to its caller.
var hopByHopHeaders = []string{
	"Connection", "Proxy-Connection", "Keep-Alive", "Proxy-Authenticate",
	"Proxy-Authorization", "Te", "Trailer", "Transfer-Encoding", "Upgrade",
}

// copyForwardResponse copies the coordinator's status, headers other than
// hop-by-hop headers, and body to w. A response carrying the forward
// listener's no-store mark is kept out of the §11.5 cache, and the mark is
// not copied. Only a live stream is flushed as it arrives; any other body is
// copied without a flush, so the serving replica's §11.5 middleware buffers
// and stores it as it stores a local response. A body that fails part-way
// gives an unknown outcome; the status is already written then.
//
// spec: §29.3 (Interactive message send) Forwarding rule 4, §11.5
// (Idempotency) Mechanism
func copyForwardResponse(ctx context.Context, w http.ResponseWriter, resp *http.Response) ForwardOutcome {
	live := isLiveStream(resp)
	h := resp.Header.Clone()
	if h.Get(forwardNoStoreHeader) != "" {
		idemmw.DoNotStore(ctx)
	}
	for _, name := range h.Values("Connection") {
		for _, f := range strings.Split(name, ",") {
			h.Del(strings.TrimSpace(f))
		}
	}
	for _, name := range append(hopByHopHeaders, forwardNoStoreHeader) {
		h.Del(name)
	}
	for name, values := range h {
		w.Header()[name] = values
	}
	w.WriteHeader(resp.StatusCode)
	if err := copyBody(w, resp.Body, live); err != nil {
		return ForwardUnknown
	}
	return ForwardDelivered
}

// isLiveStream reports whether the coordinator's response is a live stream:
// an event stream, or a chunked body with no declared length.
func isLiveStream(resp *http.Response) bool {
	if mt, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type")); err == nil && mt == "text/event-stream" {
		return true
	}
	return resp.ContentLength < 0 && slices.Contains(resp.TransferEncoding, "chunked")
}

// copyBody copies src to w, streaming with a flush per chunk when live is
// set and copying in one pass otherwise.
func copyBody(w http.ResponseWriter, src io.Reader, live bool) error {
	if live {
		return streamBody(w, src)
	}
	_, err := io.Copy(w, src)
	return err
}

// streamBody copies src to w, flushing after each chunk so a streamed
// response reaches the caller as the coordinator writes it.
func streamBody(w http.ResponseWriter, src io.Reader) error {
	flusher, _ := w.(http.Flusher)
	buf := make([]byte, 32<<10)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return werr
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// maxForwardedToolResponse caps the coordinator response a forwarded tool
// call reads, matching the MCP request bound of the /mcp endpoint.
const maxForwardedToolResponse = 16 << 20

// ForwardToolCall forwards a tools/call of tool with arguments on row to the
// session's coordinating replica when a §29.3 row of kind requires it, and
// returns the coordinator's result or error as the calling handler's own. It
// writes nothing. The returned error is nil or a *mcp.ToolError:
//
//  1. ForwardLocal, and ForwardNotDelivered for ForwardKindMessageNoReplyTo,
//     return a zero result and a nil error; the caller runs its local path
//     or holds the message.
//  2. A forward that is not delivered or whose outcome is unknown returns
//     the tool error TARGET_NOT_READY carrying currentState.
//  3. A delivered result whose isError is false returns that result.
//  4. A delivered tool error or JSON-RPC error returns its lenny error
//     envelope as a tool error.
//  5. A delivered non-2xx §15.1 error envelope, which the forward listener's
//     auth middleware writes, returns that envelope as a tool error.
//  6. A delivered response none of these decodes has an unknown outcome and
//     takes rule 2.
//
// spec: §29.3 (Interactive message send) Forwarding rule 2, §15.2 (MCP API),
// §15.2.1 (REST/MCP consistency contract)
func (s *Server) ForwardToolCall(ctx context.Context, row sessionstore.Session, kind ForwardKind, tool string, arguments json.RawMessage) (mcp.ToolResult, ForwardOutcome, error) {
	addr, outcome := s.forwardTarget(ctx, row, kind)
	switch {
	case outcome == ForwardLocal:
		return mcp.ToolResult{}, ForwardLocal, nil
	case outcome == ForwardNotDelivered && kind == ForwardKindMessageNoReplyTo:
		return mcp.ToolResult{}, ForwardNotDelivered, nil
	case outcome != forwardDial:
		return mcp.ToolResult{}, outcome, refuseToolForward(ctx, row)
	}
	req, err := newToolForwardRequest(ctx, addr, tool, arguments)
	if err != nil {
		return mcp.ToolResult{}, ForwardNotDelivered, refuseToolForward(ctx, row)
	}
	resp, outcome := s.sendForward(req)
	if outcome != ForwardDelivered {
		if outcome == ForwardNotDelivered && kind == ForwardKindMessageNoReplyTo {
			return mcp.ToolResult{}, ForwardNotDelivered, nil
		}
		return mcp.ToolResult{}, outcome, refuseToolForward(ctx, row)
	}
	defer func() { _ = resp.Body.Close() }()
	result, te, ok := decodeToolForwardResponse(ctx, resp)
	if !ok {
		return mcp.ToolResult{}, ForwardUnknown, refuseToolForward(ctx, row)
	}
	if te != nil {
		return mcp.ToolResult{}, ForwardDelivered, te
	}
	return result, ForwardDelivered, nil
}

// refuseToolForward returns the TARGET_NOT_READY tool error carrying
// currentState and keeps the refusal out of the §11.5 idempotency cache.
//
// spec: §29.3 (Interactive message send) Forwarding rules 6 and 7
func refuseToolForward(ctx context.Context, row sessionstore.Session) error {
	idemmw.DoNotStore(ctx)
	return mcp.NewToolError("TARGET_NOT_READY",
		"the session's coordinating gateway replica could not be reached; retry the tool call",
		map[string]any{"currentState": string(row.State)})
}

// toolForwardBody is the tools/call request a forwarded tool call posts.
type toolForwardBody struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      int            `json:"id"`
	Method  string         `json:"method"`
	Params  toolCallParams `json:"params"`
}

type toolCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// newToolForwardRequest builds the POST of a tools/call for tool with
// arguments to the coordinator's /mcp, carrying the credential or the
// calling session as newForwardRequest does.
func newToolForwardRequest(ctx context.Context, addr, tool string, arguments json.RawMessage) (*http.Request, error) {
	if len(arguments) == 0 {
		arguments = json.RawMessage("{}")
	}
	body, err := json.Marshal(toolForwardBody{
		JSONRPC: "2.0", ID: 1, Method: "tools/call",
		Params: toolCallParams{Name: tool, Arguments: arguments},
	})
	if err != nil {
		return nil, fmt.Errorf("encode forwarded tool call %s: %w", tool, err)
	}
	h := http.Header{}
	h.Set("Content-Type", "application/json")
	h.Set("Accept", "application/json")
	return newForwardRequest(ctx, http.MethodPost, "https://"+addr+"/mcp", bytes.NewReader(body), int64(len(body)), h)
}

// decodeToolForwardResponse decodes the coordinator's response to a
// forwarded tool call under ForwardToolCall's rules 3 to 5. ok is false when
// none of them decodes the response.
func decodeToolForwardResponse(ctx context.Context, resp *http.Response) (mcp.ToolResult, *mcp.ToolError, bool) {
	if resp.Header.Get(forwardNoStoreHeader) != "" {
		idemmw.DoNotStore(ctx)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxForwardedToolResponse))
	if err != nil {
		return mcp.ToolResult{}, nil, false
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		result, te, derr := mcp.DecodeToolCallResponse(body)
		if derr != nil {
			return mcp.ToolResult{}, nil, false
		}
		return result, te, true
	}
	var env errorEnvelope
	if err := json.Unmarshal(body, &env); err != nil || env.Error.Code == "" {
		return mcp.ToolResult{}, nil, false
	}
	return mcp.ToolResult{}, mcp.NewToolError(env.Error.Code, env.Error.Message, env.Error.Details), true
}

// NewHolderForwardClient builds the shared CH-HOLDERFORWARD client. It
// presents the gateway replica certificate reloader serves, verifies the
// coordinator against roots under the gateway DNS SAN, because it dials a pod
// IP rather than the Service name, and bounds the TCP dial and the TLS
// handshake together at HolderForwardDialBound. It sets no overall timeout:
// a forward runs under its caller's request context (§29.3 Forwarding rule
// 8). It follows no redirect.
//
// spec: §10.1.1 (Stateless Replicas and Per-Session Coordination) Replica
// address, §10.3 (mTLS PKI) NET-072
func NewHolderForwardClient(reloader *certreload.Reloader, roots *x509.CertPool) *http.Client {
	return newHolderForwardClient(holderForwardClientTLS(reloader, roots), HolderForwardDialBound)
}

// holderForwardClientTLS is the client side of NET-072 peer validation.
func holderForwardClientTLS(reloader *certreload.Reloader, roots *x509.CertPool) *tls.Config {
	return &tls.Config{
		GetClientCertificate: reloader.GetClientCertificate,
		RootCAs:              roots,
		ServerName:           gatewaycontrol.GatewayDNSName,
		MinVersion:           tls.VersionTLS13,
	}
}

// newHolderForwardClient builds the client over cfg with the given dial and
// handshake bound. A transport with DialTLSContext set ignores
// TLSClientConfig and TLSHandshakeTimeout, so cfg is applied inside the dial
// function, and one context bounds both steps.
func newHolderForwardClient(cfg *tls.Config, bound time.Duration) *http.Client {
	transport := &http.Transport{
		DialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return dialHolderTLS(ctx, network, addr, cfg, bound)
		},
		MaxIdleConnsPerHost: 16,
		IdleConnTimeout:     90 * time.Second,
	}
	return &http.Client{
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// dialHolderTLS dials addr and completes the TLS handshake under one context
// derived from ctx with the bound as its timeout.
func dialHolderTLS(ctx context.Context, network, addr string, cfg *tls.Config, bound time.Duration) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(ctx, bound)
	defer cancel()
	var d net.Dialer
	raw, err := d.DialContext(ctx, network, addr)
	if err != nil {
		return nil, fmt.Errorf("dial coordinating replica %s: %w", addr, err)
	}
	conn := tls.Client(raw, cfg.Clone())
	if err := conn.HandshakeContext(ctx); err != nil {
		_ = raw.Close()
		return nil, fmt.Errorf("handshake with coordinating replica %s: %w", addr, err)
	}
	return conn, nil
}
