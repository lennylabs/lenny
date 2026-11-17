/* SPDX-License-Identifier: MIT */
/*
 * The tier-1 C unit harness of the runtime supervisor.
 *
 * The harness links the supervisor's translation units, built with
 * SV_TEST, beside the C library. Each case is a function registered with
 * SV_CASE. The harness runs one case per process:
 *
 *   harness --list          prints every case name, one per line
 *   harness --run NAME      runs one case and exits 0 (pass), 1 (fail),
 *                           or 77 (the environment lacks what the case
 *                           needs; the reason is on stderr)
 *
 * The Go driver under tests/tier1_unit/supervisor runs each case as a
 * subtest with a timeout, so a case that hangs fails rather than blocks.
 *
 * No case calls kill(-1) in the harness's own PID namespace. A case that
 * needs the supervisor as namespace init runs it in a fresh user, PID,
 * mount, and IPC namespace (sv_ns_* below), where kill(-1) reaches that
 * namespace alone.
 */
#ifndef LENNY_SUPERVISOR_HARNESS_H
#define LENNY_SUPERVISOR_HARNESS_H

#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/types.h>
#include <sys/wait.h>
#include <unistd.h>

#include "supervisor.h"

#define SV_CASE_SKIP 77

typedef int (*sv_case_fn)(void);

struct sv_case {
	const char *name;
	sv_case_fn fn;
};

void sv_register_case(const char *name, sv_case_fn fn);

/* SV_CASE defines and registers one case. */
#define SV_CASE(name)                                                           \
	static int sv_case_##name(void);                                        \
	__attribute__((constructor)) static void sv_case_register_##name(void)  \
	{                                                                       \
		sv_register_case(#name, sv_case_##name);                        \
	}                                                                       \
	static int sv_case_##name(void)

/* Assertions: each failed one reports and returns 1 from the case. */
#define SV_FAIL(...)                                                            \
	do {                                                                    \
		fprintf(stderr, "%s:%d: ", __FILE__, __LINE__);                 \
		fprintf(stderr, __VA_ARGS__);                                   \
		fprintf(stderr, "\n");                                          \
		return 1;                                                       \
	} while (0)

#define SV_CHECK(cond, ...)                                                     \
	do {                                                                    \
		if (!(cond)) {                                                  \
			fprintf(stderr, "%s:%d: check failed: %s: ", __FILE__,  \
				__LINE__, #cond);                               \
			fprintf(stderr, __VA_ARGS__);                           \
			fprintf(stderr, "\n");                                  \
			return 1;                                               \
		}                                                               \
	} while (0)

#define SV_EQ_INT(got, want)                                                    \
	do {                                                                    \
		long long sv_g_ = (long long)(got), sv_w_ = (long long)(want);  \
		if (sv_g_ != sv_w_)                                             \
			SV_FAIL("%s = %lld, want %lld", #got, sv_g_, sv_w_);    \
	} while (0)

#define SV_EQ_STR(got, want)                                                    \
	do {                                                                    \
		const char *sv_g_ = (got), *sv_w_ = (want);                     \
		if (!sv_g_ || strcmp(sv_g_, sv_w_) != 0)                        \
			SV_FAIL("%s = \"%s\", want \"%s\"", #got,               \
				sv_g_ ? sv_g_ : "(null)", sv_w_);               \
	} while (0)

#define SV_SKIP(...)                                                            \
	do {                                                                    \
		fprintf(stderr, __VA_ARGS__);                                   \
		fprintf(stderr, "\n");                                          \
		return SV_CASE_SKIP;                                            \
	} while (0)

/* ---- Shared helpers (harness.c) ------------------------------------------ */

/* sv_bin_path writes the path of a file beside the harness binary into out. */
void sv_bin_path(char *out, size_t cap, const char *name);
/* sv_prefix_path writes SV_PATH_PREFIX/rel into out. */
void sv_prefix_path(char *out, size_t cap, const char *rel);
/* sv_rm_rf removes a tree; it ignores a missing path. */
void sv_rm_rf(const char *path);
/* sv_mkdir_p creates a directory and its parents. */
int sv_mkdir_p(const char *path);
/* sv_write_file writes s to path, creating or truncating it. */
int sv_write_file(const char *path, const char *s);
/* sv_read_file reads up to cap-1 bytes of path into out; it returns the length or -1. */
long sv_read_file(const char *path, char *out, size_t cap);
/* sv_path_exists reports whether path exists, without following a final symlink. */
int sv_path_exists(const char *path);
/* sv_status_field returns the hexadecimal value of a /proc/<pid>/status field. */
unsigned long long sv_status_mask(int pid, const char *field);
/* sv_ms_now is the harness's monotonic clock in milliseconds. */
unsigned long long sv_ms_now(void);
/* sv_sleep is a plain millisecond sleep. */
void sv_sleep(unsigned ms);
/* sv_wait_file_contains polls until path contains needle or ms pass. */
int sv_wait_file_contains(const char *path, const char *needle, unsigned ms);

/* A supervisor run in a scratch namespace, with the harness as its adapter. */
struct sv_ns {
	int launcher_pid; /* the harness's child that holds the namespaces */
	int sup_pid;      /* the supervisor's PID as the harness sees it */
	int listen_fd;
	int conn;
	char sock[64];
	char rbuf[1 << 16];
	size_t rlen;
};

/*
 * sv_ns_userns_available reports whether this host lets an unprivileged
 * process create a user namespace; a case that needs one skips without it.
 */
int sv_ns_userns_available(void);
/* sv_ns_listen binds a fresh abstract socket name for one case. */
int sv_ns_listen(struct sv_ns *ns);
/*
 * sv_ns_start runs argv (argv[0] is the binary) as PID 1 of a fresh user,
 * PID, mount, and IPC namespace with /proc remounted. The namespace maps
 * the harness's own UID to itself, so the supervisor runs without
 * capabilities. Returns 0 on success.
 */
int sv_ns_start(struct sv_ns *ns, char *const argv[]);
/* sv_ns_accept accepts the supervisor's dial within ms. */
int sv_ns_accept(struct sv_ns *ns, unsigned ms);
/* sv_ns_send writes one request line (a terminator is appended). */
int sv_ns_send(struct sv_ns *ns, const char *line);
/*
 * sv_ns_read reads one message line within ms into out (terminator
 * removed). It returns the length, 0 at EOF, or -1 at the timeout.
 */
long sv_ns_read(struct sv_ns *ns, char *out, size_t cap, unsigned ms);
/* sv_ns_request sends a line and reads the next message. */
long sv_ns_request(struct sv_ns *ns, const char *line, char *out, size_t cap, unsigned ms);
/* sv_ns_wait reaps the launcher within ms; it returns the supervisor's exit status or -1. */
int sv_ns_wait(struct sv_ns *ns, unsigned ms);
/* sv_ns_stop closes the connection and the listener and reaps the launcher. */
void sv_ns_stop(struct sv_ns *ns);
/*
 * sv_ns_run_argv builds the run-mode argv for the prefixed supervisor with
 * the given runtime argv, into a static array.
 */
char **sv_ns_run_argv(struct sv_ns *ns, const char *const *extra_flags, const char *const *runtime);

/* The EINTR injection hook that fails every other attempt of each call. */
int sv_eintr_every_other(long nr);
extern int sv_eintr_injected;

#endif
