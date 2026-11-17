/* SPDX-License-Identifier: MIT */
/*
 * The runtime supervisor's modes and its CH-SUPERVISE serve loop.
 *
 *   stage DEST
 *       copies /proc/self/exe to DEST/lenny-supervisor with mode 0555.
 *   run --socket NAME [--adapter-uid UID] [--dial-timeout D] [--env K=V]... -- ARGV...
 *       clears its environment, marks itself non-dumpable, installs its
 *       signal dispositions, runs the property self-test, dials the
 *       adapter, checks the peer UID, sends ready, and serves requests.
 *   selftest
 *       runs the property self-test alone and writes one result line per
 *       property to stdout.
 *
 * spec: §4.7.10 (Runtime supervisor); §4.7.11 item 8 (Runtime
 * supervisor); §28.5.3 (CH-SUPERVISE). In run mode the supervisor writes
 * nothing to stdout or stderr, which the runtime inherits and can forge.
 *
 * A setup or cleanup request starts a run that the serve loop does not
 * wait for: the loop keeps serving fs and ping, and answers the run with
 * setupdone or cleaned when its status arrives. At most one run is
 * outstanding (struct sv_run).
 */
#include "supervisor.h"

struct sv_run_args {
	const char *socket;
	int have_adapter_uid;
	sv_u32 adapter_uid;
	sv_u64 dial_timeout_ms;
	struct sv_env env;
	const char *fs_roots[4];
	int nfs_roots;
	char **argv;
};

/*
 * The outstanding setup or cleanup run. Its child is the one in the
 * SV_CHILD_CLEANUP slot: a setup's child is the program itself, in its own
 * session; a cleanup's is the cleanup worker. The supervisor signals that
 * PID only while the slot holds no collected status, so the PID cannot
 * have been reused.
 */
struct sv_run {
	enum sv_req_type type; /* SV_REQ_SETUP or SV_REQ_CLEANUP; SV_REQ_NONE when none runs */
	sv_u64 gen;
	sv_u64 id;
	sv_u64 deadline;       /* setup: timeoutMs; cleanup: the backstop beyond it */
	sv_u64 killed_at;      /* when the supervisor sent SIGKILL, 0 before */
	enum sv_error stop;    /* setup: why the supervisor stopped it, SV_ERR_NONE if it did not */
	int cancel_fd;         /* setup: the supervisor's copy of the cancel pipe's read end, or -1 */
	int spawn_fd;          /* setup: the spawn status pipe's read end, or -1 */
};

/* The serve loop's state. Large buffers are static: the stack stays small. */
struct sv_server {
	int conn;
	struct sv_procs procs;
	struct sv_waiter waiter;
	struct sv_run_args *args;
	sv_u64 gen;          /* the generation of the latest launch, 0 before the first */
	int launch_permitted; /* set at start and by an attested end; cleared by launch */
	struct sv_run run;
};

static struct sv_linebuf sv_lb;
static struct sv_request sv_req;
static struct sv_run_args sv_args;
static struct sv_server sv_srv;

/* ---- Argument parsing ------------------------------------------------- */

static int sv_parse_run_args(int argc, char **argv, struct sv_run_args *a)
{
	a->dial_timeout_ms = SV_DIAL_TIMEOUT_DEFAULT_MS;
	sv_env_init(&a->env);
	int i = 2;
	for (; i < argc; i++) {
		const char *f = argv[i];
		if (sv_streq(f, "--")) {
			i++;
			break;
		}
		if (i + 1 >= argc)
			return -1;
		const char *v = argv[++i];
		if (sv_streq(f, "--socket")) {
			if (*v == '@')
				v++;
			if (!*v || sv_strlen(v) > 100)
				return -1;
			a->socket = v;
		} else if (sv_streq(f, "--adapter-uid")) {
			sv_u64 u;
			if (sv_parse_u64(v, &u) || u > 0xfffffffeUL)
				return -1;
			a->have_adapter_uid = 1;
			a->adapter_uid = (sv_u32)u;
		} else if (sv_streq(f, "--dial-timeout")) {
			if (sv_parse_duration_ms(v, &a->dial_timeout_ms))
				return -1;
		} else if (sv_streq(f, "--fs-root")) {
			if (a->nfs_roots >= 4)
				return -1;
			a->fs_roots[a->nfs_roots++] = v;
		} else if (sv_streq(f, "--env")) {
			/* A later pair replaces an earlier one of the same name, PATH included. */
			if (sv_env_set(&a->env, argv[i]))
				return -1;
		} else {
			return -1;
		}
	}
	if (!a->socket || i >= argc || argv[i][0] != '/')
		return -1;
	a->argv = argv + i;
	return 0;
}

/* ---- Wire I/O ---------------------------------------------------------- */

__attribute__((noreturn)) static void sv_exit_closed(void)
{
	/* As namespace init, the exit ends every remaining process in the container. */
	sv_exit(SV_EXIT_CLOSED);
}

static void sv_send(struct sv_server *s, const char *buf, sv_size n)
{
	if (sv_send_all(s->conn, buf, n, SV_NULL, 0) < 0)
		sv_exit_closed(); /* a failed or stalled write is a closed connection (SIGPIPE row) */
}

static void sv_reply(struct sv_server *s, const struct sv_request *r, enum sv_error err)
{
	char out[SV_MAX_REPLY];
	sv_send(s, out, sv_fmt_reply(out, r->type, r->gen, r->id, err));
}

/* sv_report_exited sends exited for a runtime exit reaped outside an end. */
static void sv_report_exited(void *ctx)
{
	struct sv_server *s = ctx;
	char out[SV_MAX_REPLY];
	s->procs.exited_pending = 0;
	sv_send(s, out, sv_fmt_exited(out, s->procs.exited_gen, s->procs.exited_status));
}

static void sv_check_closed(struct sv_server *s)
{
	if (s->waiter.closed)
		sv_exit_closed();
}

/* ---- The setup or cleanup run ------------------------------------------ */

/* A setup program's process group dies with it, killed by the reaper while its zombie holds the ID. */
static void sv_run_start(struct sv_server *s, const struct sv_request *r, int pid, sv_u64 deadline_ms,
			 int cancel_fd, int spawn_fd)
{
	s->procs.slot[SV_CHILD_CLEANUP].pid = pid;
	s->procs.group_pid = r->type == SV_REQ_SETUP ? pid : 0;
	s->run = (struct sv_run){r->type, r->gen, r->id, sv_now_ms() + deadline_ms, 0, SV_ERR_NONE, cancel_fd,
				 spawn_fd};
}

/*
 * sv_run_stop sends SIGKILL to the run's child and its process group, and
 * records why. It stops a run once; the status is then collected as any
 * other, and a status that does not arrive within SV_WAIT_AFTER_KILL_MS is
 * a failure.
 */
static void sv_run_stop(struct sv_server *s, enum sv_error why)
{
	struct sv_run *run = &s->run;
	struct sv_child_slot *c = &s->procs.slot[SV_CHILD_CLEANUP];
	if (run->type == SV_REQ_NONE || run->killed_at)
		return;
	if (!c->reaped && c->pid > 0) {
		sv_kill(-c->pid, SV_SIGKILL); /* a setup program's group; no group before its setsid */
		sv_kill(c->pid, SV_SIGKILL);
	}
	run->killed_at = sv_now_ms();
	run->stop = why;
	if (run->cancel_fd >= 0)
		sv_close(run->cancel_fd);
	run->cancel_fd = -1;
}

/* sv_run_finish answers the run and clears it, so its state is gone before the reply is written. */
static void sv_run_finish(struct sv_server *s, int got, int status)
{
	struct sv_run run = s->run;
	char out[SV_MAX_REPLY];
	sv_size n;
	if (run.cancel_fd >= 0)
		sv_close(run.cancel_fd);
	sv_bytezero(&s->procs.slot[SV_CHILD_CLEANUP], sizeof(struct sv_child_slot));
	s->procs.group_pid = 0;
	s->run = (struct sv_run){SV_REQ_NONE, 0, 0, 0, 0, SV_ERR_NONE, -1, -1};
	if (run.type == SV_REQ_SETUP) {
		/*
		 * An errno on the spawn pipe means the program never ran, so there is
		 * no program status: a positive one is a failed execve, the request's
		 * error; a negative one is a supervisor step before it.
		 */
		int e = 0;
		enum sv_error err = sv_setup_result(run.stop, got, status);
		if (sv_read(run.spawn_fd, &e, sizeof(e)) != (sv_ssize)sizeof(e))
			e = 0;
		sv_close(run.spawn_fd);
		if (e)
			got = 0;
		if (e < 0)
			err = SV_ERR_SETUP_SPAWN;
		else if (e > 0)
			err = SV_ERR_SETUP_FAILED;
		n = sv_fmt_setupdone(out, run.gen, run.id, got, status, err, e > 0 ? SV_WHY_EXEC : SV_WHY_NONE, e);
	} else {
		n = sv_fmt_reply(out, run.type, run.gen, run.id, sv_cleanup_result(got, status));
	}
	sv_send(s, out, n);
}

/* sv_run_service answers a run whose status arrived and enforces its deadline. */
static void sv_run_service(struct sv_server *s)
{
	struct sv_run *run = &s->run;
	struct sv_child_slot *c = &s->procs.slot[SV_CHILD_CLEANUP];
	if (run->type == SV_REQ_NONE)
		return;
	sv_u64 now = sv_now_ms();
	if (c->reaped)
		sv_run_finish(s, 1, c->status);
	else if (run->killed_at && now - run->killed_at >= SV_WAIT_AFTER_KILL_MS)
		sv_run_finish(s, 0, 0); /* the status did not reach the supervisor: a failure */
	else if (!run->killed_at && now >= run->deadline)
		sv_run_stop(s, SV_ERR_SETUP_TIMEOUT); /* a cleanup reports its killed worker as cleanup_failed */
}

/* sv_run_next is the time of the run's next deadline, 0 when none runs. */
static sv_u64 sv_run_next(const struct sv_server *s)
{
	if (s->run.type == SV_REQ_NONE)
		return 0;
	return s->run.killed_at ? s->run.killed_at + SV_WAIT_AFTER_KILL_MS : s->run.deadline;
}

/* ---- Requests ---------------------------------------------------------- */

static void sv_handle_launch(struct sv_server *s, struct sv_request *r)
{
	if (r->gen != s->gen + 1) {
		sv_reply(s, r, SV_ERR_GEN_MISMATCH);
		return;
	}
	if (!s->launch_permitted || s->procs.slot[SV_CHILD_RUNTIME].pid) {
		sv_reply(s, r, SV_ERR_NOT_PERMITTED);
		return;
	}
	s->gen = r->gen;
	s->launch_permitted = 0;
	int exec_errno = 0;
	int pid = sv_launch_child(s->args->argv, s->args->env.vars, &exec_errno);
	if (pid < 0) {
		sv_reply(s, r, SV_ERR_EXEC_FAILED);
		return;
	}
	s->procs.slot[SV_CHILD_RUNTIME].pid = pid;
	s->procs.runtime_gen = r->gen;
	/*
	 * Nothing is reaped before launched is written, so a process that
	 * exits at once is reported by launched and then by exited.
	 */
	sv_reply(s, r, exec_errno ? SV_ERR_EXEC_FAILED : SV_ERR_NONE);
}

/*
 * An end stops a run first. When the end collects the run's status, the
 * run's reply precedes ended; otherwise it follows within
 * SV_WAIT_AFTER_KILL_MS.
 */
static void sv_handle_end(struct sv_server *s, struct sv_request *r)
{
	sv_run_stop(s, SV_ERR_SETUP_CANCELLED);
	enum sv_error err = sv_end_pass(&s->waiter, r->grace_ms, SV_END_KILL_BOUND_MS);
	sv_check_closed(s);
	sv_run_service(s);
	if (err == SV_ERR_NONE)
		s->launch_permitted = 1;
	sv_reply(s, r, err);
}

static int sv_sweep_body(void *arg)
{
	(void)arg;
#ifdef SV_TEST_SWEEP_STALL
	/* Tier-3 test build: the worker never reports, so only its backstop ends it. */
	for (;;)
		sv_ppoll(SV_NULL, 0, SV_NULL, SV_NULL);
#endif
	return sv_sweep_all(sv_getuid());
}

static void sv_handle_sweep(struct sv_server *s, struct sv_request *r)
{
	int pid = sv_spawn_worker(s->conn, sv_sweep_body, SV_NULL);
	int status = 0, got = 0;
	if (pid > 0) {
		s->procs.slot[SV_CHILD_SWEEP].pid = pid;
		int w = sv_wait_child(&s->waiter, SV_CHILD_SWEEP, SV_SWEEP_BACKSTOP_MS, &status);
		sv_check_closed(s);
		got = (w == 0);
	}
	enum sv_error err = sv_sweep_result(got, status);
	/* The supervisor ends every other process again before it answers. */
	enum sv_error end_err = sv_end_pass(&s->waiter, 0, SV_END_KILL_BOUND_MS);
	sv_check_closed(s);
	if (err == SV_ERR_NONE)
		err = end_err;
	if (end_err == SV_ERR_NONE)
		s->launch_permitted = 1;
	sv_reply(s, r, err);
}

struct sv_cleanup_ctx {
	struct sv_request *req;
	struct sv_env *env;
};

/* The step-0 cleanup metadata entries, written into the worker's own memory. */
static char sv_prev_provider_kv[SV_MAX_ARG_BYTES + 64];
static char sv_prev_lease_kv[SV_MAX_ARG_BYTES + 64];

static void sv_kv(char *out, const char *name, const char *value)
{
	sv_size n = sv_strlen(name);
	sv_bytecopy(out, name, n);
	out[n++] = '=';
	sv_size m = sv_strlen(value);
	sv_bytecopy(out + n, value, m);
	out[n + m] = 0;
}

static int sv_cleanup_body(void *arg)
{
	struct sv_cleanup_ctx *c = arg;
	int d = sv_openat(SV_AT_FDCWD, sv_workspace_base, SV_O_RDONLY | SV_O_DIRECTORY | SV_O_NOFOLLOW | SV_O_CLOEXEC, 0);
	if (d < 0)
		return SV_WORKER_FAILED;
	int r = sv_fchdir(d);
	sv_close(d);
	if (r < 0)
		return SV_WORKER_FAILED;
	/* The launch environment plus the step-0 cleanup metadata. */
	static struct sv_env env;
	env = *c->env;
	sv_kv(sv_prev_provider_kv, "LENNY_PREV_CREDENTIAL_PROVIDER", c->req->prev_provider);
	sv_kv(sv_prev_lease_kv, "LENNY_PREV_LEASE_ID", c->req->prev_lease_id);
	if (sv_env_set(&env, sv_prev_provider_kv) || sv_env_set(&env, sv_prev_lease_kv))
		return SV_WORKER_FAILED;
	return sv_cleanup_worker(c->req->commands, c->req->ncommands, env.vars, c->req->timeout_ms);
}

/*
 * The cleanup worker enforces timeoutMs itself; the serve loop holds the
 * backstop beyond it. A cleanup clears the launch permission: a process it
 * leaves behind, such as a daemon, survives until the next end, which the
 * launch of the next generation then needs. A setup leaves the permission
 * alone, so a fresh pod can run setup before its first launch.
 */
static void sv_handle_cleanup(struct sv_server *s, struct sv_request *r)
{
	struct sv_cleanup_ctx ctx = {r, &s->args->env};
	s->launch_permitted = 0;
	int pid = sv_spawn_worker(s->conn, sv_cleanup_body, &ctx);
	if (pid < 0)
		sv_reply(s, r, SV_ERR_CLEANUP_FAILED);
	else
		sv_run_start(s, r, pid, r->timeout_ms + SV_CLEANUP_BACKSTOP_SLACK_MS, -1, -1);
}

/*
 * The setup program runs as the run's child, so the serve loop enforces
 * timeoutMs and the cancel pipe on it directly. The supervisor closes its
 * copies of the passed descriptors once the child holds them, except the
 * cancel pipe's read end (passed index 2), which only the supervisor holds
 * and watches until the run ends.
 */
#define SV_CANCEL_INDEX 2
static void sv_handle_setup(struct sv_server *s, struct sv_request *r)
{
	static int fds[SV_FS_MAX];
	static char *const no_env[] = {SV_NULL};
	int n = (int)r->nfds, spawn_fd = -1;
	sv_fdin_take(fds, n);
	int cwd = sv_fs_opendir("workspace", r->cwd);
	if (cwd < 0) {
		/* The request names no directory beneath the root: its error, not the supervisor's. */
		char out[SV_MAX_REPLY];
		for (int i = 0; i < n; i++)
			sv_close(fds[i]);
		sv_send(s, out, sv_fmt_setupdone(out, r->gen, r->id, 0, 0, SV_ERR_SETUP_FAILED, SV_WHY_CWD, -cwd));
		return;
	}
	struct sv_setup_spec sp = {r->argv, r->env_empty ? no_env : s->args->env.vars, cwd, fds, n, r->fsize_bytes};
	int pid = sv_spawn_setup(s->conn, &sp, &spawn_fd);
	sv_close(cwd);
	for (int i = 0; i < n; i++)
		if (pid < 0 || i != SV_CANCEL_INDEX)
			sv_close(fds[i]);
	if (pid < 0)
		sv_reply(s, r, SV_ERR_SETUP_SPAWN);
	else
		sv_run_start(s, r, pid, r->timeout_ms, fds[SV_CANCEL_INDEX], spawn_fd);
}

/*
 * sv_gen_ok reports whether a request other than launch names the
 * current generation, which is 0 before the first launch.
 */
static int sv_gen_ok(const struct sv_server *s, const struct sv_request *r)
{
	return r->type == SV_REQ_LAUNCH || r->gen == s->gen;
}

static void sv_dispatch(struct sv_server *s, struct sv_request *r)
{
	/* Descriptors arrive only with a setup request, exactly as many as it names. */
	int want = r->type == SV_REQ_SETUP ? (int)r->nfds : 0;
	if (sv_fdin_count() != want) {
		sv_fdin_drop();
		sv_reply(s, r, SV_ERR_MALFORMED);
		return;
	}
	if (!sv_gen_ok(s, r)) {
		sv_fdin_drop();
		sv_reply(s, r, SV_ERR_GEN_MISMATCH);
		return;
	}
	/* While a run is outstanding, only fs, ping, and end are served. */
	if (s->run.type != SV_REQ_NONE && r->type != SV_REQ_FS && r->type != SV_REQ_PING && r->type != SV_REQ_END) {
		sv_fdin_drop();
		sv_reply(s, r, SV_ERR_WORKER_BUSY);
		return;
	}
	switch (r->type) {
	case SV_REQ_LAUNCH:
		sv_handle_launch(s, r);
		break;
	case SV_REQ_END:
		sv_handle_end(s, r);
		break;
	case SV_REQ_SWEEP:
		sv_handle_sweep(s, r);
		break;
	case SV_REQ_CLEANUP:
		sv_handle_cleanup(s, r);
		break;
	case SV_REQ_SETUP:
		sv_handle_setup(s, r);
		break;
	case SV_REQ_FS:
		if (!s->args->nfs_roots)
			sv_reply(s, r, SV_ERR_MALFORMED);
		else if (sv_fs_handle(s->conn, r) < 0)
			sv_exit_closed();
		break;
	default:
		sv_reply(s, r, SV_ERR_NONE); /* pong */
		break;
	}
}

/* sv_drain_lines handles every complete buffered request line. */
static void sv_drain_lines(struct sv_server *s)
{
	for (;;) {
		const char *line;
		long n = sv_line_take(&sv_lb, &line);
		if (n == -1)
			return;
		if (n < 0)
			sv_exit(SV_EXIT_PROTOCOL);
		enum sv_parse_result pr = sv_parse_request(line, (sv_size)n, &sv_req);
		sv_fdin_select(&sv_lb, (sv_size)n); /* the descriptors this line arrived with */
		sv_line_consume(&sv_lb, (sv_size)n);
		if (pr == SV_PARSE_UNREADABLE)
			sv_exit(SV_EXIT_PROTOCOL);
		if (pr == SV_PARSE_MALFORMED) {
			sv_fdin_drop();
			sv_reply(s, &sv_req, SV_ERR_MALFORMED);
		}
		else
			sv_dispatch(s, &sv_req);
	}
}

/*
 * The serve loop. The SIGCHLD flag is cleared before each reap loop, and
 * the loop waits only in ppoll, whose mask is the one place the handled
 * signals are unblocked, so a run's exit wakes it. Requests are served in
 * the order they arrive, each reply written whole; a run's reply is
 * written between two of them, at the top of the loop or at an end.
 */
__attribute__((noreturn)) static void sv_serve(struct sv_server *s)
{
	for (;;) {
		if (sv_sigchld_flag)
			sv_service_children(&s->waiter);
		sv_run_service(s);
		sv_drain_lines(s);
		/*
		 * The cancel entry asks for no event: POLLHUP, when the adapter closes
		 * its write end, is reported anyway, and a byte cancels nothing. A
		 * negative descriptor is ignored, so the entry is inert without a setup.
		 */
		struct sv_pollfd pfd[2] = {{s->conn, SV_POLLIN | SV_POLLRDHUP, 0}, {s->run.cancel_fd, 0, 0}};
		sv_u64 next = sv_run_next(s);
		struct sv_timespec ts = sv_ts_until(sv_now_ms(), next);
		int r = sv_ppoll(pfd, 2, next ? &ts : SV_NULL, sv_wait_mask());
		if (r <= 0)
			continue; /* a signal or the run's deadline: both are read at the top of the loop */
		/* The adapter closing the cancel pipe's write end cancels the setup. */
		if (pfd[1].revents)
			sv_run_stop(s, SV_ERR_SETUP_CANCELLED);
		if (pfd[0].revents && sv_fdin_fill(&sv_lb, s->conn) <= 0)
			sv_exit_closed();
	}
}

/* ---- Dial --------------------------------------------------------------- */

static void sv_sleep_until(struct sv_server *s, sv_u64 deadline)
{
	while (sv_now_ms() < deadline)
		sv_wait_until(&s->waiter, deadline);
}

/*
 * sv_dial connects to the adapter's abstract socket, repeating a dial the
 * adapter refuses until one connects or the window ends.
 */
static int sv_dial(struct sv_server *s, const char *name, sv_u64 window_ms)
{
	struct sv_sockaddr_un addr;
	sv_bytezero(&addr, sizeof(addr));
	addr.sun_family = SV_AF_UNIX;
	sv_size len = sv_strlen(name);
	sv_bytecopy(addr.sun_path + 1, name, len); /* sun_path[0] = 0: the abstract namespace */
	sv_u32 alen = (sv_u32)(2 + 1 + len);
	sv_u64 deadline = sv_now_ms() + window_ms;
	for (;;) {
		int fd = sv_socket(SV_AF_UNIX, SV_SOCK_STREAM | SV_SOCK_CLOEXEC, 0);
		if (fd < 0)
			return fd;
		int r;
		do
			r = sv_connect_raw(fd, &addr, alen);
		while (r == -SV_EINTR);
		if (r == 0 || r == -SV_EISCONN)
			return fd;
		sv_close(fd);
		if (r != -SV_ECONNREFUSED && r != -SV_ENOENT)
			return r;
		if (sv_now_ms() >= deadline)
			return -SV_ECONNREFUSED;
		sv_u64 next = sv_now_ms() + SV_DIAL_RETRY_MS;
		sv_sleep_until(s, next < deadline ? next : deadline);
	}
}

/* sv_peer_ok checks with SO_PEERCRED that the peer is the adapter UID. */
static int sv_peer_ok(int fd, sv_u32 uid)
{
	struct sv_ucred cred;
	sv_u32 len = sizeof(cred);
	if (sv_getsockopt(fd, SV_SOL_SOCKET, SV_SO_PEERCRED, &cred, &len) < 0 || len != sizeof(cred))
		return 0;
	return cred.uid == uid;
}

/* ---- Modes -------------------------------------------------------------- */

/* sv_harden clears the environment, marks the process non-dumpable, and installs the signals. */
static int sv_harden(char **envp)
{
	sv_clearenv(envp);
	if (sv_prctl(SV_PR_SET_DUMPABLE, 0) < 0)
		return -1;
	return sv_install_signals();
}

static int sv_mode_run(int argc, char **argv, char **envp)
{
	if (sv_harden(envp) < 0)
		return SV_EXIT_INTERNAL;
	if (sv_parse_run_args(argc, argv, &sv_args))
		return SV_EXIT_USAGE;
	/* kill(-1) reaches only the namespace when the supervisor is its init. */
	if (sv_getpid() != 1)
		return SV_EXIT_NOT_INIT;
	struct sv_server *s = &sv_srv;
	s->args = &sv_args;
	s->conn = -1;
	s->launch_permitted = 1;
	s->run.cancel_fd = s->run.spawn_fd = -1;
	sv_procs_init(&s->procs);
	s->waiter.procs = &s->procs;
	s->waiter.conn_fd = -1;

	struct sv_properties props;
	sv_selftest(&props);
	for (int i = 0; i < sv_args.nfs_roots; i++)
		if (sv_fs_init(sv_args.fs_roots[i]) < 0)
			return SV_EXIT_INTERNAL;

	int fd = sv_dial(s, sv_args.socket, sv_args.dial_timeout_ms);
	if (fd < 0)
		return SV_EXIT_DIAL;
	if (sv_args.have_adapter_uid && !sv_peer_ok(fd, sv_args.adapter_uid))
		return SV_EXIT_PEER; /* nothing has been sent */
	s->conn = fd;
	s->waiter.conn_fd = fd;
	s->waiter.on_exited = sv_report_exited;
	s->waiter.ctx = s;
	char out[SV_MAX_REPLY];
	sv_send(s, out, sv_fmt_ready(out, &props));
	sv_serve(s);
}

static int sv_mode_selftest(char **envp)
{
	if (sv_harden(envp) < 0)
		return SV_EXIT_INTERNAL;
	struct sv_properties props;
	sv_selftest(&props);
	char out[SV_MAX_REPLY];
	/* selftest runs in a pod with no runtime process, so its stdout is the report. */
	if (sv_write_all(1, out, sv_fmt_selftest(out, &props)) < 0)
		return SV_EXIT_INTERNAL;
	return sv_properties_gate_ok(&props) ? 0 : SV_EXIT_SELFTEST;
}

static int sv_copy_fd(int in, int out)
{
	static char buf[65536];
	for (;;) {
		sv_ssize n = sv_read(in, buf, sizeof(buf));
		if (n < 0)
			return (int)n;
		if (n == 0)
			return 0;
		int r = sv_write_all(out, buf, (sv_size)n);
		if (r < 0)
			return r;
	}
}

/* sv_mode_stage copies the binary through a temporary name and renames it into place. */
static int sv_mode_stage(int argc, char **argv)
{
	if (argc != 3)
		return SV_EXIT_USAGE;
	int dir = sv_openat(SV_AT_FDCWD, argv[2], SV_O_RDONLY | SV_O_DIRECTORY | SV_O_NOFOLLOW | SV_O_CLOEXEC, 0);
	if (dir < 0)
		return SV_EXIT_STAGE;
	int in = sv_openat(SV_AT_FDCWD, "/proc/self/exe", SV_O_RDONLY | SV_O_CLOEXEC, 0);
	if (in < 0)
		return SV_EXIT_STAGE;
	static const char tmp[] = ".lenny-supervisor.tmp";
	sv_unlinkat(dir, tmp, 0);
	int out = sv_openat(dir, tmp, SV_O_WRONLY | SV_O_CREAT | SV_O_EXCL | SV_O_NOFOLLOW | SV_O_CLOEXEC, 0700);
	if (out < 0)
		return SV_EXIT_STAGE;
	if (sv_copy_fd(in, out) < 0 || sv_fchmod(out, 0555) < 0 || sv_fsync(out) < 0)
		return SV_EXIT_STAGE;
	sv_close(out);
	sv_close(in);
	if (sv_renameat(dir, tmp, dir, "lenny-supervisor") < 0)
		return SV_EXIT_STAGE;
	sv_close(dir);
	return 0;
}

int sv_main(int argc, char **argv, char **envp)
{
	if (argc < 2)
		return SV_EXIT_USAGE;
	if (sv_streq(argv[1], "run"))
		return sv_mode_run(argc, argv, envp);
	if (sv_streq(argv[1], "selftest"))
		return sv_mode_selftest(envp);
	if (sv_streq(argv[1], "stage"))
		return sv_mode_stage(argc, argv);
	return SV_EXIT_USAGE;
}
