//go:build component

// SPDX-License-Identifier: MIT

package tokenproperties_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/credential/impersonation"
	"github.com/lennylabs/lenny/pkg/storage/issuedtokenstore"
	"github.com/lennylabs/lenny/pkg/tokenservice"
)

const (
	exchangeGrant   = "urn:ietf:params:oauth:grant-type:token-exchange"
	revokedType     = "urn:ietf:params:oauth:token-type:access_token:revoked"
	defaultAudience = "lenny-gateway"
	fullRevokeNote  = "user_invalidated"
)

// tb is the part of *testing.T and *rapid.T the world uses, so the
// generated test and the fixed scenarios share one implementation.
type tb interface {
	Helper()
	Logf(format string, args ...any)
	Fatalf(format string, args ...any)
}

// tokenKind is the operation that issued a token.
type tokenKind int

const (
	kindSignIn tokenKind = iota
	kindExchange
	kindChild
	kindPlayground
	kindImpersonation
)

func (k tokenKind) String() string {
	return [...]string{"sign-in", "exchange", "child", "playground", "impersonation"}[k]
}

// token is a bearer token the sequence produced, with the tokens the
// request that issued it presented and the users who hold its bytes.
type token struct {
	n        int
	raw      string
	c        jwt.Claims
	kind     tokenKind
	rotation bool
	// recorded reports whether a sign-in token was recorded in
	// issued_tokens when it was signed. Every minted token is recorded.
	recorded             bool
	subject, actor, call *token
	holders              map[int]bool
}

func (k *token) name() string { return fmt.Sprintf("T%d", k.n) }

func (k *token) isPlayground() bool { return k.c.Origin == jwt.OriginPlayground }

func (k *token) hasRole(roles ...auth.Role) bool {
	for _, have := range k.c.Roles {
		for _, want := range roles {
			if have == want {
				return true
			}
		}
	}
	return false
}

// user is an identity of the sequence: a sub in one of its tenants.
type user struct {
	name, sub string
	tenant    int
}

// opKind is one operation of a sequence.
type opKind int

const (
	opSignIn opKind = iota
	opExchange
	opChild
	opRevoke
	opImpersonate
	opPlayground
	opFullRevoke
	opLeak
)

func (k opKind) String() string {
	return [...]string{"sign-in", "exchange", "child", "revoke", "impersonate", "playground", "full-revoke", "leak"}[k]
}

// op is one operation with its parameters. Token fields index the
// world's tokens, and -1 means none.
type op struct {
	kind                   opKind
	user                   int
	caller, subject, actor int
	other                  int // impersonation target, full-revoke target, or leak recipient
	scope                  string
	scopeSet               bool // the request names scope (exchange and child)
	audience               string
	expiresIn              int64
	roles                  []auth.Role
	callerType             string
	expMinutes             int
	recorded               bool
	tokenAud               []string
	impersonationMinutes   int
}

func (o op) String() string {
	tok := func(i int) string {
		if i < 0 {
			return "-"
		}
		return fmt.Sprintf("T%d", i)
	}
	switch o.kind {
	case opSignIn:
		return fmt.Sprintf("sign-in user=%d scope=%q roles=%v caller_type=%q aud=%v exp=%dm recorded=%t",
			o.user, o.scope, o.roles, o.callerType, o.tokenAud, o.expMinutes, o.recorded)
	case opExchange:
		return fmt.Sprintf("exchange user=%d caller=%s subject=%s scope=%s audience=%q expires_in=%d",
			o.user, tok(o.caller), tok(o.subject), o.scopeText(), o.audience, o.expiresIn)
	case opChild:
		return fmt.Sprintf("child user=%d caller=%s subject=%s actor=%s scope=%s audience=%q expires_in=%d",
			o.user, tok(o.caller), tok(o.subject), tok(o.actor), o.scopeText(), o.audience, o.expiresIn)
	case opRevoke:
		return fmt.Sprintf("revoke user=%d caller=%s subject=%s", o.user, tok(o.caller), tok(o.subject))
	case opImpersonate:
		return fmt.Sprintf("impersonate user=%d caller=%s target=%d roles=%v duration=%dm",
			o.user, tok(o.caller), o.other, o.roles, o.impersonationMinutes)
	case opPlayground:
		return fmt.Sprintf("playground user=%d subject=%s", o.user, tok(o.subject))
	case opFullRevoke:
		return fmt.Sprintf("full-revoke user=%d caller=%s target=%d", o.user, tok(o.caller), o.other)
	default:
		return fmt.Sprintf("leak user=%d token=%s to=%d", o.user, tok(o.subject), o.other)
	}
}

func (o op) scopeText() string {
	if !o.scopeSet {
		return "absent"
	}
	return fmt.Sprintf("%q", o.scope)
}

// outcome is the result of one operation: the HTTP status the surface
// returned (or 0 when the operation's precondition outside the surface
// under test refused it), the error reason, and the token it issued.
type outcome struct {
	status int
	reason string
	issued *token
}

// world is the state of one sequence: two fresh tenants, three users,
// the tokens issued so far, and the issued_tokens rows after the last
// step.
type world struct {
	h       *harness
	tenants []string
	users   []user
	tokens  []*token
	byJTI   map[string]*token
	rows    map[string]rowState
	log     []string
	stats   map[string]int
}

func newWorld(t tb, h *harness, stats map[string]int) *world {
	t.Helper()
	suffix := randomID()[:12]
	w := &world{
		h:       h,
		tenants: []string{"acme-" + suffix, "globex-" + suffix},
		byJTI:   map[string]*token{},
		stats:   stats,
	}
	for _, tenant := range w.tenants {
		h.seedTenant(t, tenant)
	}
	w.users = []user{
		{name: "alice", sub: "alice@" + w.tenants[0] + ".test", tenant: 0},
		{name: "bob", sub: "bob@" + w.tenants[0] + ".test", tenant: 0},
		{name: "carol", sub: "carol@" + w.tenants[1] + ".test", tenant: 1},
	}
	w.rows = h.snapshot(t, w.tenants)
	return w
}

// held lists the tokens u holds, in issue order.
func (w *world) held(u int) []*token {
	var out []*token
	for _, k := range w.tokens {
		if k.holders[u] {
			out = append(out, k)
		}
	}
	return out
}

func (w *world) tok(i int) *token {
	if i < 0 || i >= len(w.tokens) {
		return nil
	}
	return w.tokens[i]
}

// sequence renders the operations applied so far, one per line.
func (w *world) sequence() string { return strings.Join(w.log, "\n") }

// step applies o, reads the rows it left, records it in the log, and
// checks every property against the step.
func (w *world) step(t tb, o op) outcome {
	t.Helper()
	before := w.rows
	out := w.run(t, o)
	w.rows = w.h.snapshot(t, w.tenants)
	line := fmt.Sprintf("%2d. %s -> %d %s", len(w.log)+1, o, out.status, out.reason)
	if out.issued != nil {
		line += " issued " + out.issued.name()
	}
	w.log = append(w.log, line)
	t.Logf("%s", line)
	if out.status >= http.StatusInternalServerError {
		t.Fatalf("step %d returned a server error\nsequence:\n%s", len(w.log), w.sequence())
	}
	w.count(o, out, before)
	checkStep(t, w, o, out, before)
	return out
}

func (w *world) run(t tb, o op) outcome {
	switch o.kind {
	case opSignIn:
		return w.signIn(t, o)
	case opExchange, opChild:
		return w.exchange(t, o)
	case opRevoke:
		return w.revoke(t, o)
	case opImpersonate:
		return w.impersonate(t, o)
	case opPlayground:
		return w.playgroundMint(t, o)
	case opFullRevoke:
		return w.fullRevoke(t, o)
	default:
		w.tok(o.subject).holders[o.other] = true
		return outcome{status: http.StatusOK}
	}
}

// add records a token the sequence received and its holder.
func (w *world) add(t tb, raw string, kind tokenKind, holder int) *token {
	t.Helper()
	c, err := w.h.signer.Verify(raw)
	if err != nil {
		t.Fatalf("verify issued %s token: %v", kind, err)
	}
	k := &token{n: len(w.tokens), raw: raw, c: c, kind: kind, holders: map[int]bool{holder: true}}
	w.tokens = append(w.tokens, k)
	w.byJTI[c.JWTID] = k
	return k
}

// signIn signs a root token for the user, as an identity provider or a
// mint outside /v1/oauth/token would, and records it in issued_tokens when
// o.recorded is set.
func (w *world) signIn(t tb, o op) outcome {
	t.Helper()
	u := w.users[o.user]
	now := time.Now().UTC().Truncate(time.Second)
	claims := jwt.Claims{
		Issuer: "https://idp.test", Subject: u.sub, TenantID: w.tenants[u.tenant], JWTID: randomID(),
		Typ: auth.TokenUserBearer, CallerType: o.callerType, Scope: o.scope, Roles: o.roles,
		Audience: o.tokenAud, IssuedAt: now.Add(-time.Minute).Unix(),
		Expiry: now.Add(time.Duration(o.expMinutes) * time.Minute).Unix(),
	}
	raw, err := w.h.signer.Sign(claims)
	if err != nil {
		t.Fatalf("sign sign-in token: %v", err)
	}
	if o.recorded {
		sum := sha256.Sum256([]byte(raw))
		if err := w.h.store.Record(context.Background(), issuedtokenstore.IssuedToken{
			JTI: claims.JWTID, TenantID: claims.TenantID, Subject: claims.Subject, TokenHash: sum[:],
			Scope: strings.Fields(claims.Scope), Audience: strings.Join(claims.Audience, " "),
			Audiences: append([]string{}, claims.Audience...), IssuedAt: now,
			ExpiresAt: time.Unix(claims.Expiry, 0).UTC(),
		}); err != nil {
			t.Fatalf("record sign-in token: %v", err)
		}
	}
	k := w.add(t, raw, kindSignIn, o.user)
	k.recorded = o.recorded
	return outcome{status: http.StatusOK, issued: k}
}

// exchange sends an exchange, with an actor_token for a child mint.
func (w *world) exchange(t tb, o op) outcome {
	t.Helper()
	c, s := w.tok(o.caller), w.tok(o.subject)
	req := tokenservice.Request{GrantType: exchangeGrant, SubjectToken: s.raw, Audience: o.audience}
	if o.scopeSet {
		req.Scope = o.scope
	}
	if o.expiresIn > 0 {
		req.ExpiresIn = &o.expiresIn
	}
	var a *token
	if o.kind == opChild {
		a = w.tok(o.actor)
		req.ActorToken = a.raw
	}
	reply := w.h.postToken(t, c.raw, req)
	if reply.status != http.StatusOK {
		return outcome{status: reply.status, reason: reply.reason}
	}
	kind := kindExchange
	if a != nil {
		kind = kindChild
	}
	k := w.add(t, reply.token, kind, o.user)
	k.subject, k.actor, k.call = s, a, c
	k.rotation = a == nil && c.c.JWTID == s.c.JWTID &&
		sameSet(strings.Fields(k.c.Scope), strings.Fields(s.c.Scope)) &&
		sameSet(k.c.Audience, subjectAudience(s.c.Audience))
	return outcome{status: reply.status, issued: k}
}

// revoke sends a recursive-revocation request.
func (w *world) revoke(t tb, o op) outcome {
	t.Helper()
	c, s := w.tok(o.caller), w.tok(o.subject)
	reply := w.h.postToken(t, c.raw, tokenservice.Request{
		GrantType: exchangeGrant, SubjectToken: s.raw, RequestedTokenType: revokedType,
	})
	return outcome{status: reply.status, reason: reply.reason}
}

// impersonate starts an impersonation session as the admin API handler
// does once it has authenticated the caller's bearer and found the
// platform-admin role on it, stamping the target's roles (o.roles) on the
// bearer as the handler stamps the target user's. A caller without the
// role yields status 0. A playground bearer also yields status 0: the
// handler checks only the role, and the gateway's scope gate refuses a
// playground bearer on this route because playground_allowed_scope does
// not grant the route's scope, which this harness does not run (TOKX-I21).
func (w *world) impersonate(t tb, o op) outcome {
	t.Helper()
	c := w.tok(o.caller)
	if !c.hasRole(auth.RolePlatformAdmin) || c.isPlayground() {
		return outcome{reason: "precondition: no platform-admin caller"}
	}
	target := w.users[o.other]
	_, bearer, err := w.h.imp.Issue(context.Background(), impersonation.IssueRequest{
		AdminSub: c.c.Subject, TargetTenantID: w.tenants[target.tenant], TargetUserID: target.sub,
		Reason: "support", TicketRef: "TCK-1", Duration: time.Duration(o.impersonationMinutes) * time.Minute,
		TargetRoles:    o.roles,
		CallerTenantID: c.c.TenantID, CallerJTI: c.c.JWTID, CallerExpiry: c.c.ExpiryTime(), CallerScope: c.c.Scope,
	})
	if errors.Is(err, impersonation.ErrIssuanceRefused) {
		return outcome{status: http.StatusForbidden, reason: err.Error()}
	}
	if err != nil {
		return outcome{status: http.StatusInternalServerError, reason: err.Error()}
	}
	k := w.add(t, bearer, kindImpersonation, o.user)
	k.call = c
	return outcome{status: http.StatusOK, issued: k}
}

// playgroundMint pastes the subject into the apiKey-mode playground mint.
func (w *world) playgroundMint(t tb, o op) outcome {
	t.Helper()
	s := w.tok(o.subject)
	reply := w.h.postPlayground(t, s.raw)
	if reply.status != http.StatusOK {
		return outcome{status: reply.status, reason: reply.reason}
	}
	k := w.add(t, reply.token, kindPlayground, o.user)
	k.subject, k.call = s, s
	return outcome{status: reply.status, issued: k}
}

// fullRevoke runs step 5 of a full revoke of the target user in the
// caller's tenant, as the admin API's user revoker does, for a caller the
// gateway authenticates as an administrator: a live, non-playground token
// with the tenant-admin or platform-admin role. A platform-admin caller
// also revokes the impersonation bearers the target started. A caller the
// gateway would refuse yields status 0.
func (w *world) fullRevoke(t tb, o op) outcome {
	t.Helper()
	c := w.tok(o.caller)
	if c.isPlayground() || w.revoked(w.rows, c) ||
		!c.hasRole(auth.RoleTenantAdmin, auth.RolePlatformAdmin) {
		return outcome{reason: "precondition: no live administrator caller"}
	}
	target := w.users[o.other]
	ctx := context.Background()
	now := time.Now().UTC()
	if _, err := w.h.store.RevokeBySubject(ctx, c.c.TenantID, target.sub, fullRevokeNote, now); err != nil {
		return outcome{status: http.StatusInternalServerError, reason: err.Error()}
	}
	if c.hasRole(auth.RolePlatformAdmin) {
		if _, err := w.h.store.RevokeImpersonationsByActor(ctx, target.sub, fullRevokeNote, now); err != nil {
			return outcome{status: http.StatusInternalServerError, reason: err.Error()}
		}
	}
	return outcome{status: http.StatusOK}
}

// revoked reports whether k's issued_tokens row in rows is revoked. A
// token without a row is not revoked.
func (w *world) revoked(rows map[string]rowState, k *token) bool {
	r, ok := rows[k.c.JWTID]
	return ok && r.revoked
}

// count tallies the scenarios the sequences reached, so the generated
// test can show that no property held only because its premise never
// occurred.
func (w *world) count(o op, out outcome, before map[string]rowState) {
	w.stats["op "+o.kind.String()]++
	if out.status != http.StatusOK {
		w.stats["refused "+o.kind.String()]++
		if reason := strings.Fields(out.reason); len(reason) > 0 {
			w.stats["refused "+o.kind.String()+": "+strings.Join(reason[:min(len(reason), 3)], " ")]++
		}
		return
	}
	if k := out.issued; k != nil {
		switch {
		case k.rotation:
			w.stats["accepted rotation"]++
		case k.kind == kindExchange && differentCaller(k.call, k.subject):
			w.stats["accepted exchange by another caller"]++
		case k.kind == kindExchange:
			w.stats["accepted exchange by the subject's caller"]++
		case k.kind == kindChild && k.subject.c.Subject != k.actor.c.Subject:
			w.stats["accepted child of another subject"]++
		case k.kind == kindChild:
			w.stats["accepted child"]++
		case k.kind == kindImpersonation && k.c.TenantID != k.call.c.TenantID:
			w.stats["accepted cross-tenant impersonation"]++
		default:
			w.stats["accepted "+k.kind.String()]++
		}
	}
	newly := newlyRevoked(before, w.rows)
	switch {
	case o.kind == opRevoke && len(newly) > 1:
		w.stats["revocation reaching descendants"]++
	case o.kind == opRevoke:
		w.stats["revocation"]++
	case o.kind == opFullRevoke && len(newly) > 0:
		w.stats["full revoke revoking tokens"]++
	}
	if o.kind == opRevoke {
		if _, had := before[w.tok(o.subject).c.JWTID]; !had {
			w.stats["revocation of a root without a row"]++
		}
		for jti := range newly {
			if r := w.rows[jti]; r.impJTI != "" {
				w.stats["revocation ending an impersonation"]++
			}
		}
	}
}

// newlyRevoked lists the jti values revoked in after and not in before.
func newlyRevoked(before, after map[string]rowState) map[string]bool {
	out := map[string]bool{}
	for jti, r := range after {
		if r.revoked && !before[jti].revoked {
			out[jti] = true
		}
	}
	return out
}

// sortedKeys returns the keys of m in order, for stable messages.
func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sameSet(a, b []string) bool {
	set := map[string]bool{}
	for _, v := range a {
		set[v] = true
	}
	other := map[string]bool{}
	for _, v := range b {
		other[v] = true
		if !set[v] {
			return false
		}
	}
	return len(set) == len(other)
}

// subjectAudience is the audience a token grants: its aud claim, or the
// gateway audience when it carries none.
func subjectAudience(aud []string) []string {
	if len(aud) == 0 {
		return []string{defaultAudience}
	}
	return aud
}

// differentCaller reports whether the caller is another principal than the
// subject: another sub, or another caller_type when both carry one.
func differentCaller(c, s *token) bool {
	if c.c.Subject != s.c.Subject {
		return true
	}
	return c.c.CallerType != "" && s.c.CallerType != "" && c.c.CallerType != s.c.CallerType
}
