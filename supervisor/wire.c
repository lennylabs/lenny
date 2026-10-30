/* SPDX-License-Identifier: MIT */
/*
 * CH-SUPERVISE wire handling: the bounded line reader, the strict JSON
 * subset parser for the request schema, and the reply templates.
 *
 * spec: §28.5.3 (CH-SUPERVISE, Messages). Each message is one JSON object
 * terminated by "\n" whose "type" member names it. Every request carries
 * "gen" and "id"; "end" carries "graceMs"; "cleanup" carries "commands"
 * (argument lists), "timeoutMs", "prevProvider", and "prevLeaseId". The
 * parser rejects an unknown or duplicate key, a missing member, a value
 * of the wrong type, and a value out of range. Replies are written from
 * fixed templates with integer and enumerated fields only, so no byte a
 * runtime process or a cleanup command wrote crosses the channel.
 */
#include "supervisor.h"

/* The largest integer a request member may carry: 2^53 - 1. */
#define SV_MAX_JSON_INT 9007199254740991ULL

static const char *const sv_error_names[SV_ERR_COUNT] = {
	[SV_ERR_NONE] = "",
	[SV_ERR_EXEC_FAILED] = "exec_failed",
	[SV_ERR_NOT_PERMITTED] = "launch_not_permitted",
	[SV_ERR_END_DEADLINE] = "end_deadline",
	[SV_ERR_PROC_READ] = "proc_read_failed",
	[SV_ERR_SWEEP_FAILED] = "sweep_failed",
	[SV_ERR_SWEEP_BOUND] = "sweep_bound",
	[SV_ERR_CLEANUP_FAILED] = "cleanup_failed",
	[SV_ERR_CLEANUP_TIMEOUT] = "cleanup_timeout",
	[SV_ERR_MALFORMED] = "malformed_request",
	[SV_ERR_GEN_MISMATCH] = "gen_mismatch",
};

const char *sv_error_name(enum sv_error e)
{
	if (e < 0 || e >= SV_ERR_COUNT)
		return "";
	return sv_error_names[e];
}

const char *sv_reply_name(enum sv_req_type t)
{
	switch (t) {
	case SV_REQ_LAUNCH:
		return "launched";
	case SV_REQ_END:
		return "ended";
	case SV_REQ_SWEEP:
		return "swept";
	case SV_REQ_CLEANUP:
		return "cleaned";
	case SV_REQ_PING:
		return "pong";
	default:
		return "";
	}
}

/* ---- Bounded line reader ---------------------------------------------- */

long sv_line_take(struct sv_linebuf *lb, const char **line)
{
	for (sv_size i = 0; i < lb->len; i++) {
		if (lb->buf[i] == '\n') {
			*line = lb->buf;
			return (long)i;
		}
	}
	if (lb->len > SV_MAX_LINE)
		return -2;
	return -1;
}

void sv_line_consume(struct sv_linebuf *lb, sv_size n)
{
	sv_size drop = n + 1;
	if (drop > lb->len)
		drop = lb->len;
	for (sv_size i = drop; i < lb->len; i++)
		lb->buf[i - drop] = lb->buf[i];
	lb->len -= drop;
}

long sv_line_fill(struct sv_linebuf *lb, int fd)
{
	sv_size room = sizeof(lb->buf) - lb->len;
	if (room == 0) {
		lb->overflow = 1;
		return -SV_EINVAL;
	}
	sv_ssize r = sv_read(fd, lb->buf + lb->len, room);
	if (r > 0)
		lb->len += (sv_size)r;
	return r;
}

/* ---- Strict JSON subset parser ---------------------------------------- */

struct sv_parser {
	const char *s;
	sv_size n;
	sv_size i;
	struct sv_request *req;
	sv_size apos;  /* next free byte in req->arena */
	int argv_pos;  /* next free slot in req->argv_store */
	int argv_total;
};

static void sv_skip_ws(struct sv_parser *p)
{
	while (p->i < p->n) {
		char c = p->s[p->i];
		if (c != ' ' && c != '\t' && c != '\r' && c != '\n')
			break;
		p->i++;
	}
}

static int sv_expect(struct sv_parser *p, char c)
{
	sv_skip_ws(p);
	if (p->i >= p->n || p->s[p->i] != c)
		return -1;
	p->i++;
	return 0;
}

static int sv_peek(struct sv_parser *p, char c)
{
	sv_skip_ws(p);
	return p->i < p->n && p->s[p->i] == c;
}

static int sv_hex4(struct sv_parser *p, sv_u32 *out)
{
	sv_u32 v = 0;
	if (p->i + 4 > p->n)
		return -1;
	for (int k = 0; k < 4; k++) {
		char c = p->s[p->i++];
		v <<= 4;
		if (c >= '0' && c <= '9')
			v |= (sv_u32)(c - '0');
		else if (c >= 'a' && c <= 'f')
			v |= (sv_u32)(c - 'a' + 10);
		else if (c >= 'A' && c <= 'F')
			v |= (sv_u32)(c - 'A' + 10);
		else
			return -1;
	}
	*out = v;
	return 0;
}

static int sv_put(struct sv_parser *p, char c)
{
	if (p->apos >= SV_MAX_LINE)
		return -1;
	p->req->arena[p->apos++] = c;
	return 0;
}

static int sv_put_utf8(struct sv_parser *p, sv_u32 cp)
{
	if (cp < 0x80)
		return sv_put(p, (char)cp);
	if (cp < 0x800)
		return sv_put(p, (char)(0xc0 | (cp >> 6))) || sv_put(p, (char)(0x80 | (cp & 0x3f)));
	if (cp < 0x10000)
		return sv_put(p, (char)(0xe0 | (cp >> 12))) ||
		       sv_put(p, (char)(0x80 | ((cp >> 6) & 0x3f))) ||
		       sv_put(p, (char)(0x80 | (cp & 0x3f)));
	return sv_put(p, (char)(0xf0 | (cp >> 18))) || sv_put(p, (char)(0x80 | ((cp >> 12) & 0x3f))) ||
	       sv_put(p, (char)(0x80 | ((cp >> 6) & 0x3f))) || sv_put(p, (char)(0x80 | (cp & 0x3f)));
}

/*
 * sv_string decodes a JSON string into the arena, NUL-terminated. It
 * rejects a control character, an unknown escape, a NUL (\u0000), a lone
 * surrogate, and a decoded length above max.
 */
static int sv_string(struct sv_parser *p, char **out, sv_size max)
{
	if (sv_expect(p, '"'))
		return -1;
	sv_size start = p->apos;
	for (;;) {
		if (p->i >= p->n)
			return -1;
		unsigned char c = (unsigned char)p->s[p->i++];
		if (c == '"')
			break;
		if (c < 0x20)
			return -1;
		if (c != '\\') {
			if (sv_put(p, (char)c))
				return -1;
		} else {
			if (p->i >= p->n)
				return -1;
			char e = p->s[p->i++];
			char lit = 0;
			switch (e) {
			case '"': lit = '"'; break;
			case '\\': lit = '\\'; break;
			case '/': lit = '/'; break;
			case 'b': lit = '\b'; break;
			case 'f': lit = '\f'; break;
			case 'n': lit = '\n'; break;
			case 'r': lit = '\r'; break;
			case 't': lit = '\t'; break;
			case 'u': {
				sv_u32 cp;
				if (sv_hex4(p, &cp))
					return -1;
				if (cp >= 0xdc00 && cp <= 0xdfff)
					return -1;
				if (cp >= 0xd800 && cp <= 0xdbff) {
					sv_u32 lo;
					if (p->i + 2 > p->n || p->s[p->i] != '\\' || p->s[p->i + 1] != 'u')
						return -1;
					p->i += 2;
					if (sv_hex4(p, &lo) || lo < 0xdc00 || lo > 0xdfff)
						return -1;
					cp = 0x10000 + ((cp - 0xd800) << 10) + (lo - 0xdc00);
				}
				if (cp == 0)
					return -1;
				if (sv_put_utf8(p, cp))
					return -1;
				break;
			}
			default:
				return -1;
			}
			if (e != 'u' && sv_put(p, lit))
				return -1;
		}
		/* Every decoded byte, an escape's included, counts against max. */
		if (p->apos - start > max)
			return -1;
	}
	if (sv_put(p, 0))
		return -1;
	*out = p->req->arena + start;
	return 0;
}

/* sv_uint decodes a non-negative integer with no sign, fraction, exponent, or leading zero. */
static int sv_uint(struct sv_parser *p, sv_u64 *out)
{
	sv_skip_ws(p);
	if (p->i >= p->n || p->s[p->i] < '0' || p->s[p->i] > '9')
		return -1;
	if (p->s[p->i] == '0' && p->i + 1 < p->n && p->s[p->i + 1] >= '0' && p->s[p->i + 1] <= '9')
		return -1;
	sv_u64 v = 0;
	while (p->i < p->n && p->s[p->i] >= '0' && p->s[p->i] <= '9') {
		v = v * 10 + (sv_u64)(p->s[p->i] - '0');
		if (v > SV_MAX_JSON_INT)
			return -1;
		p->i++;
	}
	if (p->i < p->n && (p->s[p->i] == '.' || p->s[p->i] == 'e' || p->s[p->i] == 'E'))
		return -1;
	*out = v;
	return 0;
}

/* sv_argv decodes one non-empty array of strings whose first string is non-empty. */
static int sv_argv(struct sv_parser *p, char ***out)
{
	if (sv_expect(p, '['))
		return -1;
	char **argv = p->req->argv_store + p->argv_pos;
	int argc = 0;
	if (sv_peek(p, ']'))
		return -1;
	for (;;) {
		char *s;
		if (argc >= SV_MAX_ARGS || p->argv_total >= SV_MAX_ARGV_TOTAL)
			return -1;
		if (sv_string(p, &s, SV_MAX_ARG_BYTES))
			return -1;
		if (argc == 0 && !s[0])
			return -1;
		argv[argc++] = s;
		p->argv_total++;
		if (sv_peek(p, ',')) {
			p->i++;
			continue;
		}
		if (sv_expect(p, ']'))
			return -1;
		break;
	}
	argv[argc] = SV_NULL;
	p->argv_pos += argc + 1;
	*out = argv;
	return 0;
}

static int sv_commands(struct sv_parser *p)
{
	struct sv_request *r = p->req;
	if (sv_expect(p, '['))
		return -1;
	r->ncommands = 0;
	if (sv_peek(p, ']')) {
		p->i++;
		return 0;
	}
	for (;;) {
		if (r->ncommands >= SV_MAX_COMMANDS)
			return -1;
		if (sv_argv(p, &r->commands[r->ncommands]))
			return -1;
		r->ncommands++;
		if (sv_peek(p, ',')) {
			p->i++;
			continue;
		}
		return sv_expect(p, ']');
	}
}

enum sv_key {
	SV_K_TYPE = 0,
	SV_K_GEN,
	SV_K_ID,
	SV_K_GRACE,
	SV_K_COMMANDS,
	SV_K_TIMEOUT,
	SV_K_PREV_PROVIDER,
	SV_K_PREV_LEASE,
	SV_K_COUNT
};

static const char *const sv_key_names[SV_K_COUNT] = {
	"type", "gen", "id", "graceMs", "commands", "timeoutMs", "prevProvider", "prevLeaseId",
};

/* The members each request type requires; no other member is accepted. */
static unsigned sv_required_keys(enum sv_req_type t)
{
	unsigned base = (1u << SV_K_TYPE) | (1u << SV_K_GEN) | (1u << SV_K_ID);
	switch (t) {
	case SV_REQ_END:
		return base | (1u << SV_K_GRACE);
	case SV_REQ_CLEANUP:
		return base | (1u << SV_K_COMMANDS) | (1u << SV_K_TIMEOUT) |
		       (1u << SV_K_PREV_PROVIDER) | (1u << SV_K_PREV_LEASE);
	case SV_REQ_LAUNCH:
	case SV_REQ_SWEEP:
	case SV_REQ_PING:
		return base;
	default:
		return 0;
	}
}

static enum sv_req_type sv_type_of(const char *s)
{
	if (sv_streq(s, "launch"))
		return SV_REQ_LAUNCH;
	if (sv_streq(s, "end"))
		return SV_REQ_END;
	if (sv_streq(s, "sweep"))
		return SV_REQ_SWEEP;
	if (sv_streq(s, "cleanup"))
		return SV_REQ_CLEANUP;
	if (sv_streq(s, "ping"))
		return SV_REQ_PING;
	return SV_REQ_NONE;
}

static int sv_key_index(const char *k)
{
	for (int i = 0; i < SV_K_COUNT; i++)
		if (sv_streq(k, sv_key_names[i]))
			return i;
	return -1;
}

static int sv_member(struct sv_parser *p, int key)
{
	struct sv_request *r = p->req;
	char *s;
	switch (key) {
	case SV_K_TYPE:
		if (sv_string(p, &s, 16))
			return -1;
		r->type = sv_type_of(s);
		return r->type == SV_REQ_NONE ? -1 : 0;
	case SV_K_GEN:
		return sv_uint(p, &r->gen);
	case SV_K_ID:
		return sv_uint(p, &r->id);
	case SV_K_GRACE:
		return sv_uint(p, &r->grace_ms);
	case SV_K_TIMEOUT:
		return sv_uint(p, &r->timeout_ms);
	case SV_K_COMMANDS:
		return sv_commands(p);
	case SV_K_PREV_PROVIDER:
		return sv_string(p, &r->prev_provider, SV_MAX_ARG_BYTES);
	case SV_K_PREV_LEASE:
		return sv_string(p, &r->prev_lease_id, SV_MAX_ARG_BYTES);
	default:
		return -1;
	}
}

enum sv_parse_result sv_parse_request(const char *line, sv_size len, struct sv_request *req)
{
	struct sv_parser p = {.s = line, .n = len, .i = 0, .req = req};
	unsigned seen = 0;
	int bad = 0;

	req->type = SV_REQ_NONE;
	req->gen = req->id = req->grace_ms = req->timeout_ms = 0;
	req->ncommands = 0;
	req->prev_provider = req->prev_lease_id = SV_NULL;

	if (len > SV_MAX_LINE || sv_expect(&p, '{'))
		return SV_PARSE_UNREADABLE;
	if (!sv_peek(&p, '}')) {
		for (;;) {
			char *k;
			if (sv_string(&p, &k, 32))
				return SV_PARSE_UNREADABLE;
			int key = sv_key_index(k);
			if (sv_expect(&p, ':'))
				return SV_PARSE_UNREADABLE;
			if (key < 0 || (seen & (1u << key))) {
				/* An unknown or duplicate key; the value must still parse to keep reading. */
				return (seen & 7u) == 7u ? SV_PARSE_MALFORMED : SV_PARSE_UNREADABLE;
			}
			if (sv_member(&p, key)) {
				if (key <= SV_K_ID)
					return SV_PARSE_UNREADABLE;
				bad = 1;
				break;
			}
			seen |= 1u << key;
			if (sv_peek(&p, ',')) {
				p.i++;
				continue;
			}
			break;
		}
	}
	if (!bad) {
		if (sv_expect(&p, '}'))
			bad = 1;
		sv_skip_ws(&p);
		if (p.i != p.n)
			bad = 1;
	}
	/* Without type, gen, and id no reply can be attributed to a request. */
	if ((seen & 7u) != 7u)
		return SV_PARSE_UNREADABLE;
	if (bad || seen != sv_required_keys(req->type))
		return SV_PARSE_MALFORMED;
	return SV_PARSE_OK;
}

/* ---- Reply templates --------------------------------------------------- */

static sv_size sv_cat(char *out, sv_size pos, const char *s)
{
	while (*s)
		out[pos++] = *s++;
	return pos;
}

static sv_size sv_cat_u64(char *out, sv_size pos, sv_u64 v)
{
	return pos + sv_fmt_u64(out + pos, v);
}

sv_size sv_fmt_reply(char *out, enum sv_req_type t, sv_u64 gen, sv_u64 id, enum sv_error err)
{
	sv_size n = 0;
	n = sv_cat(out, n, "{\"type\":\"");
	n = sv_cat(out, n, sv_reply_name(t));
	n = sv_cat(out, n, "\",\"gen\":");
	n = sv_cat_u64(out, n, gen);
	n = sv_cat(out, n, ",\"id\":");
	n = sv_cat_u64(out, n, id);
	if (err != SV_ERR_NONE) {
		n = sv_cat(out, n, ",\"error\":\"");
		n = sv_cat(out, n, sv_error_name(err));
		n = sv_cat(out, n, "\"");
	}
	n = sv_cat(out, n, "}\n");
	return n;
}

/*
 * exited carries "status" for a process that exited and "signal" for one
 * a signal ended (spec: §4.7.10, Children). WIFSIGNALED is tested first,
 * because WEXITSTATUS reads 0 for a signalled process.
 */
sv_size sv_fmt_exited(char *out, sv_u64 gen, int st)
{
	sv_size n = 0;
	n = sv_cat(out, n, "{\"type\":\"exited\",\"gen\":");
	n = sv_cat_u64(out, n, gen);
	if (SV_WIFSIGNALED(st)) {
		n = sv_cat(out, n, ",\"signal\":");
		n = sv_cat_u64(out, n, (sv_u64)SV_WTERMSIG(st));
	} else {
		n = sv_cat(out, n, ",\"status\":");
		n = sv_cat_u64(out, n, (sv_u64)SV_WEXITSTATUS(st));
	}
	n = sv_cat(out, n, "}\n");
	return n;
}

static const char *sv_pf(int pass)
{
	return pass ? "pass" : "fail";
}

/*
 * The property names of the self-test (spec: §4.7.11 item 8, Property
 * self-test): the non-dumpable mark, kill reach, and PID-namespace init
 * immunity to SIGKILL and to SIGSTOP.
 */
sv_size sv_fmt_ready(char *out, const struct sv_properties *pr)
{
	sv_size n = 0;
	n = sv_cat(out, n, "{\"type\":\"ready\",\"gen\":0,\"properties\":{\"nondumpable\":\"");
	n = sv_cat(out, n, sv_pf(pr->nondumpable));
	n = sv_cat(out, n, "\",\"killReach\":\"");
	n = sv_cat(out, n, sv_pf(pr->kill_reach));
	n = sv_cat(out, n, "\",\"initSigkillImmune\":\"");
	n = sv_cat(out, n, sv_pf(pr->init_sigkill_immune));
	n = sv_cat(out, n, "\",\"initSigstopImmune\":\"");
	n = sv_cat(out, n, sv_pf(pr->init_sigstop_immune));
	n = sv_cat(out, n, "\"}}\n");
	return n;
}

sv_size sv_fmt_selftest(char *out, const struct sv_properties *pr)
{
	sv_size n = 0;
	n = sv_cat(out, n, "nondumpable ");
	n = sv_cat(out, n, sv_pf(pr->nondumpable));
	n = sv_cat(out, n, "\nkillReach ");
	n = sv_cat(out, n, sv_pf(pr->kill_reach));
	n = sv_cat(out, n, "\ninitSigkillImmune ");
	n = sv_cat(out, n, sv_pf(pr->init_sigkill_immune));
	n = sv_cat(out, n, "\ninitSigstopImmune ");
	n = sv_cat(out, n, sv_pf(pr->init_sigstop_immune));
	n = sv_cat(out, n, "\n");
	return n;
}

/* The non-dumpable mark and kill reach gate a restart pod; init immunity gates none. */
int sv_properties_gate_ok(const struct sv_properties *pr)
{
	return pr->nondumpable && pr->kill_reach;
}
