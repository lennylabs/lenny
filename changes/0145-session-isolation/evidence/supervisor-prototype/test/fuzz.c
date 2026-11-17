/* SPDX-License-Identifier: MIT */
/*
 * The bounded fuzz driver of the runtime supervisor, built with the
 * address and undefined-behaviour sanitizers.
 *
 *   fuzz parser ITERATIONS SEED     mutates valid request lines and feeds
 *                                   each to the line reader and the
 *                                   strict parser
 *   fuzz walker ITERATIONS SEED DIR builds random trees under DIR and
 *                                   sweeps each with random bounds
 *
 * Each mode exits 0 when every iteration held its invariants; a sanitizer
 * report or a failed invariant exits non-zero. The iteration count and
 * the seed make a run reproducible.
 *
 * spec: §28.5.3 (CH-SUPERVISE, Messages): strict decoding; §4.7.10
 * (Runtime supervisor, Sweep): the walk follows no symbolic link, skips
 * slots/, and bounds its depth and entry count.
 */
#define _GNU_SOURCE
#include <errno.h>
#include <fcntl.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/stat.h>
#include <unistd.h>

#include "supervisor.h"

static unsigned long long rng;

static unsigned rnd(unsigned n)
{
	rng ^= rng << 13;
	rng ^= rng >> 7;
	rng ^= rng << 17;
	return n ? (unsigned)(rng % n) : 0;
}

static const char *const seeds[] = {
	"{\"type\":\"launch\",\"gen\":1,\"id\":7}",
	"{\"type\":\"end\",\"gen\":2,\"id\":8,\"graceMs\":2500}",
	"{\"type\":\"sweep\",\"gen\":2,\"id\":9}",
	"{\"type\":\"ping\",\"gen\":0,\"id\":10}",
	"{\"type\":\"cleanup\",\"gen\":3,\"id\":11,\"commands\":[[\"/bin/a\",\"x\\u00e9\"],[\"/bin/b\"]],"
	"\"timeoutMs\":30000,\"prevProvider\":\"anthropic\",\"prevLeaseId\":\"lease-1\"}",
};

static const char *const tokens[] = {
	"{", "}", "[", "]", ",", ":", "\"", "\\", "\\u", "\\ud800", "\\udc00", "\\u0000", "0", "-1",
	"1e9", "9007199254740992", "\"type\"", "\"gen\"", "\"id\"", "\"commands\"", "[[\"/a\"]]",
	"null", "true", "\n", "\t", "\x01", "\xff",
};

static struct sv_request req;
static struct sv_linebuf lb;
static char buf[SV_MAX_LINE + 256];

/* check_request asserts the invariants of an accepted request. */
static int check_request(const struct sv_request *r)
{
	if (r->type == SV_REQ_NONE)
		return -1;
	if (r->ncommands < 0 || r->ncommands > SV_MAX_COMMANDS)
		return -1;
	for (int c = 0; c < r->ncommands; c++) {
		char **argv = r->commands[c];
		if (!argv[0] || !argv[0][0])
			return -1;
		int n = 0;
		while (argv[n]) {
			if (strlen(argv[n]) > SV_MAX_ARG_BYTES)
				return -1;
			n++;
		}
		if (n > SV_MAX_ARGS)
			return -1;
	}
	if (r->type == SV_REQ_CLEANUP && (!r->prev_provider || !r->prev_lease_id))
		return -1;
	return 0;
}

static size_t mutate(char *out, size_t cap)
{
	const char *s = seeds[rnd(sizeof(seeds) / sizeof(seeds[0]))];
	size_t n = strlen(s);
	memcpy(out, s, n);
	int edits = 1 + (int)rnd(6);
	for (int e = 0; e < edits; e++) {
		switch (rnd(4)) {
		case 0: /* flip a byte */
			if (n)
				out[rnd((unsigned)n)] = (char)rnd(256);
			break;
		case 1: /* delete a span */
			if (n) {
				size_t at = rnd((unsigned)n), len = 1 + rnd(8);
				if (at + len > n)
					len = n - at;
				memmove(out + at, out + at + len, n - at - len);
				n -= len;
			}
			break;
		case 2: { /* insert a token */
			const char *t = tokens[rnd(sizeof(tokens) / sizeof(tokens[0]))];
			size_t tl = strlen(t), at = n ? rnd((unsigned)n + 1) : 0;
			if (n + tl < cap) {
				memmove(out + at + tl, out + at, n - at);
				memcpy(out + at, t, tl);
				n += tl;
			}
			break;
		}
		default: { /* repeat a span, growing toward the bounds */
			if (n && n * 2 < cap) {
				size_t at = rnd((unsigned)n), len = 1 + rnd((unsigned)(n - at));
				size_t reps = 1 + rnd(64);
				for (size_t k = 0; k < reps && n + len < cap; k++) {
					memmove(out + at + len, out + at, n - at);
					n += len;
				}
			}
			break;
		}
		}
	}
	return n;
}

static int fuzz_parser(long iterations)
{
	for (long i = 0; i < iterations; i++) {
		size_t n = mutate(buf, sizeof(buf) - 1);
		enum sv_parse_result r = sv_parse_request(buf, n, &req);
		if (r == SV_PARSE_OK && check_request(&req) < 0) {
			fprintf(stderr, "iteration %ld: an accepted request breaks an invariant: %.*s\n", i,
				(int)(n > 200 ? 200 : n), buf);
			return 1;
		}
		/* The line reader over the same bytes, split at a random point. */
		memset(&lb, 0, sizeof(lb));
		size_t first = n ? rnd((unsigned)n) : 0;
		memcpy(lb.buf, buf, first < sizeof(lb.buf) ? first : sizeof(lb.buf));
		lb.len = first;
		const char *line;
		for (;;) {
			long t = sv_line_take(&lb, &line);
			if (t < 0)
				break;
			sv_parse_request(line, (sv_size)t, &req);
			sv_line_consume(&lb, (sv_size)t);
		}
	}
	return 0;
}

static void rm_rf(const char *path)
{
	char cmd[8192];
	snprintf(cmd, sizeof(cmd), "rm -rf -- '%s'", path);
	if (system(cmd) != 0) {
	}
}

static int fuzz_walker(long iterations, const char *dir)
{
	char root[4096], outside[4096], path[16384];
	snprintf(outside, sizeof(outside), "%s/outside", dir);
	for (long i = 0; i < iterations; i++) {
		snprintf(root, sizeof(root), "%s/tree", dir);
		rm_rf(root);
		rm_rf(outside);
		mkdir(root, 0755);
		mkdir(outside, 0755);
		snprintf(path, sizeof(path), "%s/target", outside);
		close(open(path, O_CREAT | O_WRONLY, 0644));
		snprintf(path, sizeof(path), "%s/slots", root);
		mkdir(path, 0755);
		snprintf(path, sizeof(path), "%s/slots/keep", root);
		close(open(path, O_CREAT | O_WRONLY, 0644));
		int nodes = 1 + (int)rnd(60);
		char cur[4096];
		snprintf(cur, sizeof(cur), "%s", root);
		for (int k = 0; k < nodes; k++) {
			char name[32];
			snprintf(name, sizeof(name), "n%d", k);
			snprintf(path, sizeof(path), "%s/%s", cur, name);
			switch (rnd(4)) {
			case 0:
				if (mkdir(path, 0755) == 0 && strlen(path) < sizeof(cur))
					memcpy(cur, path, strlen(path) + 1);
				break;
			case 1:
				if (symlink(rnd(2) ? outside : "/nonexistent", path) < 0) {
				}
				break;
			default:
				close(open(path, O_CREAT | O_WRONLY, 0644));
				break;
			}
		}
		struct sv_sweep_stats st;
		memset(&st, 0, sizeof(st));
		int depth = (int)rnd(12);
		sv_u64 entries = 1 + rnd(80);
		sv_sweep_tree(root, getuid(), "slots", depth, entries, &st);
		snprintf(path, sizeof(path), "%s/target", outside);
		if (access(path, F_OK) != 0) {
			fprintf(stderr, "iteration %ld: the walk followed a symbolic link\n", i);
			return 1;
		}
		snprintf(path, sizeof(path), "%s/slots/keep", root);
		if (access(path, F_OK) != 0) {
			fprintf(stderr, "iteration %ld: the walk entered slots/\n", i);
			return 1;
		}
		if (st.entries > entries + 1) {
			fprintf(stderr, "iteration %ld: %llu entries past a bound of %llu\n", i,
				(unsigned long long)st.entries, (unsigned long long)entries);
			return 1;
		}
		if (!st.bound_reached && !st.failed) {
			/* A walk that finished removed everything but slots/. */
			char probe[8192];
			snprintf(probe, sizeof(probe), "%s/n0", root);
			struct stat sb;
			if (lstat(probe, &sb) == 0) {
				fprintf(stderr, "iteration %ld: a finished walk left %s\n", i, probe);
				return 1;
			}
		}
	}
	rm_rf(root);
	rm_rf(outside);
	return 0;
}

int main(int argc, char **argv)
{
	if (argc < 4) {
		fprintf(stderr, "usage: fuzz parser|walker ITERATIONS SEED [DIR]\n");
		return 2;
	}
	long iterations = strtol(argv[2], NULL, 10);
	rng = strtoull(argv[3], NULL, 10) | 1;
	if (strcmp(argv[1], "parser") == 0)
		return fuzz_parser(iterations);
	if (strcmp(argv[1], "walker") == 0 && argc == 5)
		return fuzz_walker(iterations, argv[4]);
	return 2;
}
