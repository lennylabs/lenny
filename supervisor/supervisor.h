/* SPDX-License-Identifier: MIT */
/*
 * Internal declarations shared by the runtime supervisor's translation
 * units. Every external symbol carries the sv_ prefix so the C unit
 * harness can link these units beside a C library.
 */
#ifndef LENNY_SUPERVISOR_H
#define LENNY_SUPERVISOR_H

#include "syscalls.h"

/*
 * SV_PATH_PREFIX is prepended to every platform volume path the
 * supervisor opens (the sweep volumes, the workspace base, the mqueue
 * mount, and pids.current). The shipped binary is built with an empty
 * prefix. The tier-1 harness builds a copy with a temporary directory as
 * prefix, so the namespace cases sweep a scratch tree rather than the
 * host's /tmp. /proc is never prefixed.
 */
#ifndef SV_PATH_PREFIX
#define SV_PATH_PREFIX ""
#endif

/* ---- Limits ------------------------------------------------------------ */

/* The longest request line the supervisor reads, terminator excluded. */
#define SV_MAX_LINE 65536
/* cleanup bounds: commands per request, arguments per command, bytes per argument. */
#define SV_MAX_COMMANDS 64
#define SV_MAX_ARGS 256
#define SV_MAX_ARG_BYTES 4096
/* The total number of argument pointers one cleanup request may hold. */
#define SV_MAX_ARGV_TOTAL 1024
/* Launch environment entries, the fixed PATH included. */
#define SV_MAX_ENV 256
/* The sweep walk's depth and entry bounds (spec: §4.7.10, Sweep). */
#define SV_SWEEP_MAX_DEPTH 64
#define SV_SWEEP_MAX_ENTRIES 200000

/* ---- Timing ------------------------------------------------------------ */

/* The default --dial-timeout window: it covers a cold pull of the runtime image. */
#define SV_DIAL_TIMEOUT_DEFAULT_MS (10ULL * 60 * 1000)
/* The interval between two refused dials. */
#define SV_DIAL_RETRY_MS 100
/* The bound on an end's SIGKILL passes after the SIGTERM grace. */
#define SV_END_KILL_BOUND_MS 5000
/* The pause between two end passes. */
#define SV_END_PASS_INTERVAL_MS 10
/* The sweep worker's backstop: at it the worker is sent SIGKILL. */
#define SV_SWEEP_BACKSTOP_MS (10ULL * 60 * 1000)
/* The cleanup worker's backstop beyond timeoutMs. */
#define SV_CLEANUP_BACKSTOP_SLACK_MS 5000
/* How long a waiting step waits for a status after its backstop SIGKILL. */
#define SV_WAIT_AFTER_KILL_MS 5000
/* How long the self-test waits for a process state to settle. */
#define SV_SELFTEST_SETTLE_MS 100

/* ---- Exit statuses ----------------------------------------------------- */

#define SV_EXIT_CLOSED 0     /* the CH-SUPERVISE connection closed */
#define SV_EXIT_SELFTEST 1   /* selftest mode: a gating property failed */
#define SV_EXIT_USAGE 2      /* malformed arguments */
#define SV_EXIT_DIAL 3       /* the dial window ended with no connection */
#define SV_EXIT_PEER 4       /* the peer is not the adapter UID */
#define SV_EXIT_NOT_INIT 5   /* run mode outside a PID namespace's init */
#define SV_EXIT_PROTOCOL 6   /* an unreadable request line */
#define SV_EXIT_INTERNAL 7   /* a step the supervisor cannot complete */
#define SV_EXIT_STAGE 8      /* stage mode failed */

/* Worker exit statuses (spec: §4.7.10, Children). */
#define SV_WORKER_OK 0
#define SV_WORKER_FAILED 1
#define SV_WORKER_BOUND 2    /* sweep: the walk reached its bound */
#define SV_WORKER_DEADLINE 3 /* cleanup: the cleanupTimeoutSeconds deadline */

/* A launched child or cleanup command whose execve failed exits with this status. */
#define SV_EXEC_FAILED_STATUS 127

/* ---- Error codes ------------------------------------------------------- */

/*
 * The enumerated error codes a reply carries (spec: §28.5.3, CH-SUPERVISE,
 * Messages). The specification leaves the names to the two platform
 * binaries; the adapter's strict decoder accepts exactly this set.
 */
enum sv_error {
	SV_ERR_NONE = 0,
	SV_ERR_EXEC_FAILED,      /* launch: fork or execve of the command failed */
	SV_ERR_NOT_PERMITTED,    /* launch: a process is live or no end was attested since the last launch */
	SV_ERR_END_DEADLINE,     /* end: processes remained at the end deadline */
	SV_ERR_PROC_READ,        /* end: /proc could not be read */
	SV_ERR_SWEEP_FAILED,     /* sweep: the worker failed */
	SV_ERR_SWEEP_BOUND,      /* sweep: the walk reached its bound */
	SV_ERR_CLEANUP_FAILED,   /* cleanup: a command or the worker failed */
	SV_ERR_CLEANUP_TIMEOUT,  /* cleanup: the cleanupTimeoutSeconds deadline */
	SV_ERR_MALFORMED,        /* any request: a member is missing, unknown, or out of range */
	SV_ERR_GEN_MISMATCH,     /* any request: gen is not the generation the request needs */
	SV_ERR_COUNT
};

const char *sv_error_name(enum sv_error e);

/* ---- Small utilities (util.c) ------------------------------------------ */

sv_size sv_strlen(const char *s);
int sv_streq(const char *a, const char *b);
int sv_strneq(const char *a, const char *b, sv_size n);
void sv_bytecopy(void *dst, const void *src, sv_size n);
void sv_bytezero(void *dst, sv_size n);
/* sv_fmt_u64 writes n in decimal at buf and returns the digit count. */
sv_size sv_fmt_u64(char *buf, sv_u64 n);
/* sv_parse_u64 parses a decimal string; it returns 0 on success. */
int sv_parse_u64(const char *s, sv_u64 *out);
/* sv_parse_duration_ms parses N, Nms, Ns, or Nm; it returns 0 on success. */
int sv_parse_duration_ms(const char *s, sv_u64 *out);
sv_u64 sv_now_ms(void);
/* sv_ts_until is the time from now to deadline, 0 when the deadline has passed. */
struct sv_timespec sv_ts_until(sv_u64 now_ms, sv_u64 deadline_ms);
/* sv_write_all writes every byte, retrying short writes; 0 on success. */
int sv_write_all(int fd, const void *buf, sv_size n);
/* sv_is_numeric reports whether a name is a non-empty run of digits. */
int sv_is_numeric(const char *s);
/* sv_path_join writes a/b into out (cap bytes); it returns 0 on success. */
int sv_path_join(char *out, sv_size cap, const char *a, const char *b);

/* ---- Environment (util.c) ---------------------------------------------- */

#define SV_FIXED_PATH "PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"

struct sv_env {
	int n;
	char *vars[SV_MAX_ENV + 1];
};

void sv_env_init(struct sv_env *e);
/* sv_env_set adds K=V, replacing an earlier entry of the same name; 0 on success. */
int sv_env_set(struct sv_env *e, char *kv);
/* sv_env_valid reports whether s is NAME=VALUE with a non-empty name. */
int sv_env_valid(const char *s);
/* sv_clearenv zeroes the environment strings the kernel placed on the stack. */
void sv_clearenv(char **envp);

/* ---- Signals (signals.c) ----------------------------------------------- */

extern volatile int sv_sigchld_flag;
extern volatile int sv_signal_flag;

/* The mask of every signal that carries a flag-setting handler. */
sv_u64 sv_handled_mask(void);
/* The mask of the fault signals, which stay SIG_DFL and blocked. */
sv_u64 sv_fault_mask(void);
/* sv_install_signals installs the dispositions and the blocked mask. */
int sv_install_signals(void);
/* sv_wait_mask is the mask passed to ppoll: only the fault signals blocked. */
const sv_u64 *sv_wait_mask(void);
/* sv_child_reset_signals empties the mask of a child about to call execve. */
void sv_child_reset_signals(void);
int sv_is_fault_signal(int sig);
int sv_is_handled_signal(int sig);

/* ---- Wire (wire.c) ----------------------------------------------------- */

enum sv_req_type {
	SV_REQ_NONE = 0,
	SV_REQ_LAUNCH,
	SV_REQ_END,
	SV_REQ_SWEEP,
	SV_REQ_CLEANUP,
	SV_REQ_PING,
};

struct sv_request {
	enum sv_req_type type;
	sv_u64 gen;
	sv_u64 id;
	sv_u64 grace_ms;   /* end */
	sv_u64 timeout_ms; /* cleanup */
	int ncommands;     /* cleanup */
	char **commands[SV_MAX_COMMANDS];
	char *prev_provider; /* cleanup */
	char *prev_lease_id; /* cleanup */
	/* Storage the decoded strings and argument vectors point into. */
	char arena[SV_MAX_LINE + 1];
	char *argv_store[SV_MAX_ARGV_TOTAL + SV_MAX_COMMANDS];
};

enum sv_parse_result {
	SV_PARSE_OK = 0,
	/* The line is a request whose type, gen, and id are known, but a member is not valid. */
	SV_PARSE_MALFORMED,
	/* The line cannot be attributed to a request. */
	SV_PARSE_UNREADABLE,
};

/* sv_parse_request decodes one request line (no terminator). */
enum sv_parse_result sv_parse_request(const char *line, sv_size len, struct sv_request *req);

const char *sv_reply_name(enum sv_req_type t);

/* The bounded line reader. */
struct sv_linebuf {
	char buf[SV_MAX_LINE + 1];
	sv_size len;
	int overflow;
};

/*
 * sv_line_take returns the length of the first complete line in lb and
 * stores its start in *line, or -1 when no complete line is buffered, or
 * -2 when the buffered bytes exceed SV_MAX_LINE with no terminator.
 */
long sv_line_take(struct sv_linebuf *lb, const char **line);
/* sv_line_consume drops the first n bytes and the terminator. */
void sv_line_consume(struct sv_linebuf *lb, sv_size n);
/* sv_line_fill reads once from fd; it returns the byte count, 0 at EOF, or a negated errno. */
long sv_line_fill(struct sv_linebuf *lb, int fd);

/* The largest reply any template writes, terminator included. */
#define SV_MAX_REPLY 256

/* Reply formatting from fixed templates; each returns the byte count. */
sv_size sv_fmt_reply(char *out, enum sv_req_type t, sv_u64 gen, sv_u64 id, enum sv_error err);
sv_size sv_fmt_exited(char *out, sv_u64 gen, int wait_status);

struct sv_properties {
	int nondumpable;
	int kill_reach;
	int init_sigkill_immune;
	int init_sigstop_immune;
};
sv_size sv_fmt_ready(char *out, const struct sv_properties *p);
/* sv_fmt_selftest writes one "<name> <pass|fail>" line per property. */
sv_size sv_fmt_selftest(char *out, const struct sv_properties *p);
int sv_properties_gate_ok(const struct sv_properties *p);

/* ---- Processes (procs.c) ----------------------------------------------- */

/* The kinds of child a waiting step waits for (spec: §4.7.10, Children). */
enum sv_child_kind {
	SV_CHILD_RUNTIME = 0,
	SV_CHILD_SWEEP,
	SV_CHILD_CLEANUP,
	SV_CHILD_KINDS
};

struct sv_child_slot {
	int pid;      /* 0 when no child of this kind runs */
	int reaped;   /* the reaper recorded its status */
	int status;   /* the wait status the reaper recorded */
};

struct sv_procs;
/* sv_kill_fn sends sig to every other process in the namespace. */
typedef int (*sv_kill_fn)(int sig);
/* sv_count_fn counts the other processes; a negated errno on a read failure. */
typedef long (*sv_count_fn)(struct sv_procs *p);

struct sv_procs {
	struct sv_child_slot slot[SV_CHILD_KINDS];
	int echild;         /* the last reap loop ended with ECHILD */
	int in_end;         /* an end or an end pass is in progress */
	int exited_pending; /* a runtime exit waits to be reported */
	int exited_status;
	sv_u64 exited_gen;
	sv_u64 runtime_gen; /* the generation of the launched runtime process */
	/* The paths the end reads; tests point them at fixtures. */
	const char *proc_path;
	const char *pids_current_path;
	int self_pid;
	/* The end's kill and count primitives; tests replace them. */
	sv_kill_fn kill_all;
	sv_count_fn count_others_fn;
};

/* sv_kill_all_default is kill(-1, sig), which as namespace init reaches every other process. */
int sv_kill_all_default(int sig);
void sv_procs_init(struct sv_procs *p);
/* sv_reap_all collects every exited child without blocking (the reap loop). */
void sv_reap_all(struct sv_procs *p);
/* sv_reap_record files one collected status against its child's slot. */
void sv_reap_record(struct sv_procs *p, int pid, int status);

/*
 * sv_count_others counts the numeric /proc entries other than self; it
 * returns the count or a negated errno.
 */
long sv_count_others(const char *proc_path, int self_pid);
/*
 * sv_read_pids_current returns the value of a pids.current file, -1 when
 * the file is absent or cannot be opened, or -2 when the file opened but
 * yielded no parsable value. The end skips the file on either negative
 * result.
 */
long sv_read_pids_current(const char *path);

/*
 * The wait primitive every step uses. sv_wait_until waits through ppoll
 * until the deadline, a signal, or a close of the connection, reaps when
 * SIGCHLD arrived, and reports a pending runtime exit through the
 * callback outside an end. It returns 1 when the connection closed (and
 * sets closed), 0 otherwise. Callers loop on their own condition.
 */
typedef void (*sv_exited_fn)(void *ctx);
struct sv_waiter {
	struct sv_procs *procs;
	int conn_fd;            /* -1 in a worker */
	sv_exited_fn on_exited; /* reports a pending runtime exit; NULL in a worker */
	void *ctx;
	int closed;             /* the connection closed during a wait */
};
int sv_wait_until(struct sv_waiter *w, sv_u64 deadline_ms);
/* sv_service_children clears the SIGCHLD flag, reaps, and reports a pending exit. */
void sv_service_children(struct sv_waiter *w);

/*
 * sv_end_pass sends SIGTERM to every other process, waits grace_ms (which
 * may be 0), and then repeats SIGKILL, the reap loop, and the /proc and
 * pids.current check until two consecutive passes are clean or bound_ms
 * passes (spec: §4.7.10, End). A pids.current that cannot be read is
 * skipped; only a failed /proc scan yields SV_ERR_PROC_READ. It returns
 * the reply error; a close of the connection sets w->closed.
 */
enum sv_error sv_end_pass(struct sv_waiter *w, sv_u64 grace_ms, sv_u64 bound_ms);

/*
 * sv_wait_child waits for the status of the child in a slot. It returns
 * 0 with *status set when the status reached the step, 1 when the
 * connection closed, and -1 when the reaper reported ECHILD with no status
 * recorded for the child, or no status arrived after the backstop SIGKILL
 * (spec: §4.7.10, Children: a status that does not reach its step is a
 * failure). backstop_ms, when non-zero, bounds the wait: at it the child
 * is sent SIGKILL and the wait continues.
 */
int sv_wait_child(struct sv_waiter *w, enum sv_child_kind kind, sv_u64 backstop_ms, int *status);

/*
 * sv_launch_child forks a child that empties its signal mask, starts a
 * new session, and execs argv with env. It returns the child's PID, or a
 * negated errno when fork failed; *exec_errno is set non-zero when the
 * execve failed.
 */
int sv_launch_child(char *const argv[], char *const env[], int *exec_errno);

/*
 * sv_spawn_worker forks a worker that closes conn_fd before anything
 * else and exits with body(arg) (spec: §4.7.11 item 8, Protection from
 * author code). It returns the worker's PID or a negated errno.
 */
typedef int (*sv_worker_body)(void *arg);
int sv_spawn_worker(int conn_fd, sv_worker_body body, void *arg);

/* The reply error for a sweep worker's outcome (spec: §4.7.10, Children). */
enum sv_error sv_sweep_result(int got_status, int status);
/* The reply error for a cleanup worker's outcome (spec: §4.7.10, Children). */
enum sv_error sv_cleanup_result(int got_status, int status);

/* ---- Cleanup worker (procs.c) ------------------------------------------ */

/*
 * sv_cleanup_decide classifies one cleanup command's wait. It returns
 * SV_WORKER_DEADLINE when the deadline has fallen, whether or not the
 * command exited (the deadline status prevails), SV_WORKER_FAILED for a
 * command that exited non-zero or by a signal, SV_WORKER_OK for one that
 * exited 0, and -1 while the command still runs.
 */
int sv_cleanup_decide(int reaped, int status, sv_u64 now_ms, sv_u64 deadline_ms);

/*
 * sv_cleanup_worker runs the commands in order from the current directory
 * and returns the worker exit status (spec: §4.7.10, Cleanup).
 */
int sv_cleanup_worker(char **const *commands, int ncommands, char *const env[], sv_u64 timeout_ms);
extern const char *const sv_workspace_base;

/* ---- Sweep worker (sweep.c) -------------------------------------------- */

struct sv_sweep_stats {
	sv_u64 entries;
	int bound_reached;
	int failed;
};

/* sv_sweep_tree removes every entry uid owns beneath root (spec: §4.7.10, Sweep). */
void sv_sweep_tree(const char *root, sv_u32 uid, const char *skip_top, int max_depth,
		   sv_u64 max_entries, struct sv_sweep_stats *st);
/*
 * sv_sweep_ipc_listing removes the System V objects a /proc/sysvipc
 * listing names; it returns -1 when the listing cannot be opened.
 */
int sv_sweep_ipc_listing(const char *path, int kind, sv_u32 uid, struct sv_sweep_stats *st);
/* sv_sweep_ipc_stat enumerates with the *_STAT commands when the listing is unreadable. */
void sv_sweep_ipc_stat(int kind, sv_u32 uid, struct sv_sweep_stats *st);
/* sv_sweep_mqueue removes the POSIX message queues uid owns under the mqueue mount. */
void sv_sweep_mqueue(const char *mount, sv_u32 uid, struct sv_sweep_stats *st);
/* sv_sweep_all runs every part of the sweep and returns the worker exit status. */
int sv_sweep_all(sv_u32 uid);
/* sv_sweep_status maps a sweep's statistics to the worker exit status. */
int sv_sweep_status(const struct sv_sweep_stats *st);

/*
 * The System V kinds, in /proc/sysvipc file order. The column that names
 * each kind's uid and cuid differs per file.
 */
#define SV_IPC_SHM 0
#define SV_IPC_MSG 1
#define SV_IPC_SEM 2

/* The sweep's volume list and the workspace base's skipped child. */
extern const char *const sv_sweep_volumes[];
extern const char *const sv_sweep_skip_top;
extern const char *const sv_mqueue_mount;

/*
 * sv_ipc_line_select parses one /proc/sysvipc line of the kind and reports
 * whether uid matches its uid or cuid column; *id receives the object id.
 * It returns 1 to remove, 0 to keep, and -1 for a line it cannot parse.
 */
int sv_ipc_line_select(const char *line, int kind, sv_u32 uid, int *id);

/* ---- Self-test (selftest.c) -------------------------------------------- */

/* sv_selftest runs every property of the self-test. */
void sv_selftest(struct sv_properties *out);
/* sv_selftest_nondumpable: a same-UID child cannot attach to or read the caller. */
int sv_selftest_nondumpable(void);
/*
 * sv_selftest_kill_reach: kill_all reaches a double-forked grandchild
 * whose /proc entry is present before the kill and absent after it. A
 * NULL kill_all fails the property without sending anything.
 */
int sv_selftest_kill_reach(sv_kill_fn kill_all);
/* sv_selftest_init_immune tests SIGSTOP and then SIGKILL immunity as namespace init. */
void sv_selftest_init_immune(int *sigstop_immune, int *sigkill_immune);

#ifdef SV_TEST
/* Test-build hook: the kill-reach grandchild exits before the kill. */
extern int sv_test_grandchild_exits_early;
/* Test-build hook: a launched child or cleanup command waits this long before setsid. */
extern sv_u64 sv_test_pre_setsid_delay_ms;
/* Test-build hook: when set, sv_now_ms returns its value. */
extern sv_u64 (*sv_now_hook)(void);
#endif

/* ---- Entry (supervisor.c) ---------------------------------------------- */

int sv_main(int argc, char **argv, char **envp);

#endif
