/* SPDX-License-Identifier: MIT */
/*
 * Process handling of the runtime supervisor: the reaper, the wait
 * primitive, the end pass, the launch child, the forked workers, and the
 * cleanup worker.
 *
 * spec: §4.7.10 (Runtime supervisor, Children, End, Cleanup). One reaper
 * collects every child and every process reparented to the supervisor as
 * the namespace init. It hands the status of the launched runtime process,
 * a sweep worker, and the cleanup worker to the step that waits for that
 * child, and a status that does not reach its step is a failure.
 */
#include "supervisor.h"

const char *const sv_workspace_base = SV_PATH_PREFIX "/workspace";

int sv_kill_all_default(int sig)
{
	return sv_kill(-1, sig);
}

static long sv_count_others_default(struct sv_procs *p)
{
	return sv_count_others(p->proc_path, p->self_pid);
}

void sv_procs_init(struct sv_procs *p)
{
	sv_bytezero(p, sizeof(*p));
	p->proc_path = "/proc";
	p->pids_current_path = SV_PATH_PREFIX "/sys/fs/cgroup/pids.current";
	p->self_pid = sv_getpid();
	p->kill_all = sv_kill_all_default;
	p->count_others_fn = sv_count_others_default;
}

/*
 * sv_reap_record files one status. The launched runtime process's exit is
 * part of an end in progress and reported by nothing else; outside an end
 * it is reported by exited. A worker's status is kept for the step that
 * waits for it. A process reparented to the supervisor is collected and
 * nothing is reported.
 */
void sv_reap_record(struct sv_procs *p, int pid, int status)
{
	for (int k = 0; k < SV_CHILD_KINDS; k++) {
		struct sv_child_slot *s = &p->slot[k];
		if (s->pid != pid || s->reaped)
			continue;
		if (k == SV_CHILD_RUNTIME) {
			s->pid = 0;
			if (!p->in_end) {
				p->exited_pending = 1;
				p->exited_status = status;
				p->exited_gen = p->runtime_gen;
			}
			return;
		}
		s->reaped = 1;
		s->status = status;
		return;
	}
}

/*
 * The reap loop: wait4(-1, WNOHANG) until it returns 0 or ECHILD. The
 * caller clears the SIGCHLD flag before it runs the loop, so a SIGCHLD
 * that arrives during the loop runs it again.
 */
void sv_reap_all(struct sv_procs *p)
{
	for (;;) {
		int st = 0;
		int r = sv_wait4(-1, &st, SV_WNOHANG);
		if (r > 0) {
			sv_reap_record(p, r, st);
			continue;
		}
		p->echild = (r == -SV_ECHILD);
		return;
	}
}

void sv_service_children(struct sv_waiter *w)
{
	struct sv_procs *p = w->procs;
	sv_sigchld_flag = 0;
	sv_reap_all(p);
	if (w->on_exited && p->exited_pending && !p->in_end)
		w->on_exited(w->ctx);
}

int sv_wait_until(struct sv_waiter *w, sv_u64 deadline_ms)
{
	struct sv_timespec ts = sv_ts_until(sv_now_ms(), deadline_ms);
	struct sv_pollfd pfd = {w->conn_fd, SV_POLLRDHUP, 0};
	/*
	 * Every handled signal is blocked outside ppoll and unblocked only by
	 * the mask ppoll takes, so a signal that arrives after the caller's
	 * last check interrupts this wait instead of waiting for the timeout.
	 */
	int r = sv_ppoll(w->conn_fd >= 0 ? &pfd : SV_NULL, w->conn_fd >= 0 ? 1 : 0, &ts,
			 sv_wait_mask());
	if (r > 0 && (pfd.revents & (SV_POLLHUP | SV_POLLRDHUP | SV_POLLERR | SV_POLLNVAL))) {
		w->closed = 1;
		return 1;
	}
	if (sv_sigchld_flag)
		sv_service_children(w);
	return 0;
}

long sv_count_others(const char *proc_path, int self_pid)
{
	int fd = sv_openat(SV_AT_FDCWD, proc_path, SV_O_RDONLY | SV_O_DIRECTORY | SV_O_CLOEXEC, 0);
	if (fd < 0)
		return fd;
	char self[24];
	self[sv_fmt_u64(self, (sv_u64)self_pid)] = 0;
	long count = 0;
	char buf[4096] __attribute__((aligned(8)));
	for (;;) {
		long n = sv_getdents64(fd, buf, sizeof(buf));
		if (n < 0) {
			sv_close(fd);
			return n;
		}
		if (n == 0)
			break;
		for (long off = 0; off < n;) {
			struct sv_dirent64 *d = (struct sv_dirent64 *)(buf + off);
			if (sv_is_numeric(d->d_name) && !sv_streq(d->d_name, self))
				count++;
			off += d->d_reclen;
		}
	}
	sv_close(fd);
	return count;
}

/*
 * pids.current is read only where it is readable (spec: §4.7.10, End): a
 * file that is absent or that cannot be opened, whatever the errno, is
 * not readable, and the /proc scan alone decides the pass.
 */
long sv_read_pids_current(const char *path)
{
	int fd = sv_openat(SV_AT_FDCWD, path, SV_O_RDONLY | SV_O_CLOEXEC | SV_O_NOFOLLOW, 0);
	if (fd < 0)
		return -1;
	char buf[32];
	sv_ssize n = sv_read(fd, buf, sizeof(buf) - 1);
	sv_close(fd);
	if (n <= 0)
		return -2;
	sv_size len = (sv_size)n;
	while (len && (buf[len - 1] == '\n' || buf[len - 1] == ' '))
		len--;
	buf[len] = 0;
	sv_u64 v;
	if (sv_parse_u64(buf, &v) || v > 0x7fffffffUL)
		return -2;
	return (long)v;
}

/*
 * sv_end_clean_pass runs one pass: SIGKILL, the reap loop, and the check.
 * It returns 1 for a clean pass, 0 for one that found another process,
 * and -1 when /proc could not be read. A pids.current that yields no
 * parsed value is not readable and is skipped, so only the /proc scan
 * can fail a pass. The supervisor is single-threaded, so a clean
 * pids.current counts the supervisor alone.
 */
static int sv_end_clean_pass(struct sv_procs *p)
{
	p->kill_all(SV_SIGKILL);
	sv_sigchld_flag = 0;
	sv_reap_all(p);
	long n = p->count_others_fn(p);
	if (n < 0)
		return -1;
	long pc = sv_read_pids_current(p->pids_current_path);
	return n == 0 && (pc < 0 || pc <= 1);
}

enum sv_error sv_end_pass(struct sv_waiter *w, sv_u64 grace_ms, sv_u64 bound_ms)
{
	struct sv_procs *p = w->procs;
	enum sv_error err = SV_ERR_NONE;
	p->in_end = 1;
	/* SIGTERM is sent on every end; a grace of 0 sends SIGKILL right after it. */
	p->kill_all(SV_SIGTERM);
	sv_u64 grace_end = sv_now_ms() + grace_ms;
	while (sv_now_ms() < grace_end) {
		if (sv_wait_until(w, grace_end))
			goto out;
	}
	sv_u64 deadline = sv_now_ms() + bound_ms;
	int clean = 0;
	for (;;) {
		int r = sv_end_clean_pass(p);
		if (r < 0) {
			err = SV_ERR_PROC_READ;
			break;
		}
		clean = r ? clean + 1 : 0;
		if (clean >= 2)
			break;
		if (sv_now_ms() >= deadline) {
			err = SV_ERR_END_DEADLINE;
			break;
		}
		if (sv_wait_until(w, sv_now_ms() + SV_END_PASS_INTERVAL_MS))
			break;
	}
out:
	p->in_end = 0;
	return err;
}

int sv_wait_child(struct sv_waiter *w, enum sv_child_kind kind, sv_u64 backstop_ms, int *status)
{
	struct sv_procs *p = w->procs;
	struct sv_child_slot *s = &p->slot[kind];
	sv_u64 start = sv_now_ms();
	sv_u64 killed_at = 0;
	for (;;) {
		sv_service_children(w);
		if (s->reaped) {
			*status = s->status;
			sv_bytezero(s, sizeof(*s));
			return 0;
		}
		sv_u64 now = sv_now_ms();
		if (p->echild || (killed_at && now - killed_at >= SV_WAIT_AFTER_KILL_MS)) {
			sv_bytezero(s, sizeof(*s));
			return -1;
		}
		if (backstop_ms && !killed_at && now - start >= backstop_ms) {
			sv_kill(s->pid, SV_SIGKILL);
			killed_at = now;
		}
		sv_u64 next = now + 50;
		if (backstop_ms && !killed_at && start + backstop_ms < next)
			next = start + backstop_ms;
		if (sv_wait_until(w, next))
			return 1;
	}
}

/*
 * The launched runtime process and each cleanup command start with an
 * empty signal mask (execve resets the handlers) in a new session. Every
 * descriptor the supervisor holds carries close-on-exec, so the child
 * inherits only its standard streams. The close-on-exec pipe carries the
 * execve errno back, and closes with no bytes on a successful execve.
 * The runtime command's first element is an absolute path (spec: §5.1),
 * so only a cleanup command searches the PATH.
 */
#ifdef SV_TEST
sv_u64 sv_test_pre_setsid_delay_ms;
#endif

/* sv_env_lookup returns the value of name's entry in env, or SV_NULL. */
static const char *sv_env_lookup(char *const env[], const char *name)
{
	sv_size n = sv_strlen(name);
	for (; env && *env; env++)
		if (sv_strneq(*env, name, n) && (*env)[n] == '=')
			return *env + n + 1;
	return SV_NULL;
}

static int sv_has_slash(const char *s)
{
	for (; *s; s++)
		if (*s == '/')
			return 1;
	return 0;
}

/*
 * sv_execve_path runs a cleanup command whose program names no directory
 * the way the argv mode of workspace setup runs one: argv[0] is looked up
 * in each element of the PATH in env, in order (spec: §4.7.10, Runtime
 * supervisor, Cleanup; Environment). A candidate that is missing, sits
 * under a non-directory, or is not executable moves the search on to the
 * next element; any other execve failure ends it. An element that is not
 * an absolute directory is skipped, because a relative element would
 * resolve against the working directory, which the agent writes. It
 * returns the negated errno of the last attempt, and returns only when
 * every attempt failed.
 */
static int sv_execve_path(char *const argv[], char *const env[])
{
	const char *file = argv[0];
	if (sv_has_slash(file))
		return sv_execve(file, argv, env);
	const char *path = sv_env_lookup(env, "PATH");
	sv_size flen = sv_strlen(file);
	if (!path || flen == 0 || flen >= SV_MAX_ARG_BYTES)
		return -SV_ENOENT;
	char cand[2 * SV_MAX_ARG_BYTES + 2];
	int r = -SV_ENOENT;
	while (*path) {
		sv_size dlen = 0;
		while (path[dlen] && path[dlen] != ':')
			dlen++;
		if (dlen > 0 && path[0] == '/' && dlen + 1 + flen < sizeof(cand)) {
			sv_bytecopy(cand, path, dlen);
			cand[dlen] = '/';
			sv_bytecopy(cand + dlen + 1, file, flen + 1);
			r = sv_execve(cand, argv, env);
			if (r != -SV_ENOENT && r != -SV_ENOTDIR && r != -SV_EACCES)
				return r;
		}
		path += dlen;
		if (*path == ':')
			path++;
	}
	return r;
}

__attribute__((noreturn)) static void sv_exec_child(char *const argv[], char *const env[], int errfd, int search_path)
{
	sv_child_reset_signals();
#ifdef SV_TEST
	if (sv_test_pre_setsid_delay_ms) {
		struct sv_timespec ts = sv_ts_until(0, sv_test_pre_setsid_delay_ms);
		sv_ppoll(SV_NULL, 0, &ts, SV_NULL);
	}
#endif
	sv_setsid();
	int r = search_path ? sv_execve_path(argv, env) : sv_execve(argv[0], argv, env);
	if (errfd >= 0) {
		int e = -r;
		sv_write(errfd, &e, sizeof(e));
	}
	sv_exit(SV_EXEC_FAILED_STATUS);
}

int sv_launch_child(char *const argv[], char *const env[], int *exec_errno)
{
	int fds[2];
	*exec_errno = 0;
	int r = sv_pipe2(fds, SV_O_CLOEXEC);
	if (r < 0)
		return r;
	int pid = sv_fork();
	if (pid == 0) {
		sv_close(fds[0]);
		sv_exec_child(argv, env, fds[1], 0);
	}
	sv_close(fds[1]);
	if (pid < 0) {
		sv_close(fds[0]);
		return pid;
	}
	int e = 0;
	sv_ssize n = sv_read(fds[0], &e, sizeof(e));
	sv_close(fds[0]);
	if (n == (sv_ssize)sizeof(e))
		*exec_errno = e ? e : SV_EINVAL;
	return pid;
}

int sv_spawn_worker(int conn_fd, sv_worker_body body, void *arg)
{
	int pid = sv_fork();
	if (pid == 0) {
		/* The worker holds no CH-SUPERVISE descriptor from its first instruction on. */
		if (conn_fd >= 0)
			sv_close(conn_fd);
		sv_exit(body(arg));
	}
	return pid;
}

enum sv_error sv_sweep_result(int got_status, int status)
{
	if (!got_status || SV_WIFSIGNALED(status))
		return SV_ERR_SWEEP_FAILED;
	if (SV_WEXITSTATUS(status) == SV_WORKER_OK)
		return SV_ERR_NONE;
	if (SV_WEXITSTATUS(status) == SV_WORKER_BOUND)
		return SV_ERR_SWEEP_BOUND;
	return SV_ERR_SWEEP_FAILED;
}

enum sv_error sv_cleanup_result(int got_status, int status)
{
	if (!got_status || SV_WIFSIGNALED(status))
		return SV_ERR_CLEANUP_FAILED;
	if (SV_WEXITSTATUS(status) == SV_WORKER_OK)
		return SV_ERR_NONE;
	if (SV_WEXITSTATUS(status) == SV_WORKER_DEADLINE)
		return SV_ERR_CLEANUP_TIMEOUT;
	return SV_ERR_CLEANUP_FAILED;
}

int sv_cleanup_decide(int reaped, int status, sv_u64 now_ms, sv_u64 deadline_ms)
{
	if (now_ms >= deadline_ms)
		return SV_WORKER_DEADLINE;
	if (!reaped)
		return -1;
	/* WIFSIGNALED first: WEXITSTATUS reads 0 for a signalled command. */
	if (SV_WIFSIGNALED(status) || SV_WEXITSTATUS(status) != 0)
		return SV_WORKER_FAILED;
	return SV_WORKER_OK;
}

/*
 * sv_kill_command sends SIGKILL to a cleanup command's process group,
 * which setsid made its own, and to the command itself, which the group
 * kill misses while the command has not yet called setsid.
 */
static void sv_kill_command(int pid)
{
	sv_kill(-pid, SV_SIGKILL);
	sv_kill(pid, SV_SIGKILL);
}

/*
 * sv_run_command runs one command to its end or the deadline. At the
 * deadline the command and its process group are sent SIGKILL and the
 * command is collected. Each iteration reads the clock once, so the time
 * it waits is computed from the same reading the deadline test used.
 */
static int sv_run_command(char *const argv[], char *const env[], sv_u64 deadline)
{
	int pid = sv_fork();
	if (pid < 0)
		return SV_WORKER_FAILED;
	if (pid == 0)
		sv_exec_child(argv, env, -1, 1);
	for (;;) {
		int st = 0;
		/* The flag is cleared before the wait, so a later SIGCHLD wakes ppoll. */
		sv_sigchld_flag = 0;
		int r = sv_wait4(pid, &st, SV_WNOHANG);
		if (r < 0 && r != -SV_ECHILD)
			r = 0;
		sv_u64 now = sv_now_ms();
		int d = sv_cleanup_decide(r == pid, st, now, deadline);
		if (d == SV_WORKER_DEADLINE) {
			if (r != pid) {
				sv_kill_command(pid);
				sv_wait4(pid, &st, 0);
			}
			return d;
		}
		if (d >= 0)
			return d;
		if (r == -SV_ECHILD)
			return SV_WORKER_FAILED; /* the status did not reach the worker */
		struct sv_timespec ts = sv_ts_until(now, deadline);
		sv_ppoll(SV_NULL, 0, &ts, sv_wait_mask());
	}
}

int sv_cleanup_worker(char **const *commands, int ncommands, char *const env[], sv_u64 timeout_ms)
{
	sv_u64 deadline = sv_now_ms() + timeout_ms;
	for (int i = 0; i < ncommands; i++) {
		int r = sv_run_command(commands[i], env, deadline);
		if (r != SV_WORKER_OK)
			return r;
	}
	return SV_WORKER_OK;
}
