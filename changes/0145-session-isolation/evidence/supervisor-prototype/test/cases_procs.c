/* SPDX-License-Identifier: MIT */
/*
 * Cases for the supervisor's process handling: the reaper and the
 * waiting steps, the end pass and its pids.current read, the forked
 * workers, the cleanup worker and its deadline, the launch child, and
 * EINTR injection into every wrapped call.
 *
 * spec: §4.7.10 (Runtime supervisor, Children, End, Cleanup). The end
 * cases replace the supervisor's kill(-1) with a fake, so no case here
 * signals a process other than its own children.
 */
#define _GNU_SOURCE
#include "harness.h"

#include <errno.h>
#include <fcntl.h>
#include <signal.h>
#include <sys/prctl.h>
#include <sys/socket.h>
#include <sys/stat.h>

static struct sv_procs procs;
static struct sv_waiter waiter;

static void setup(void)
{
	sv_install_signals();
	sv_procs_init(&procs);
	memset(&waiter, 0, sizeof(waiter));
	waiter.procs = &procs;
	waiter.conn_fd = -1;
}

static char scratch[4096];

/* scratch_dir makes a fresh per-case directory under the test prefix. */
static const char *scratch_dir(const char *name)
{
	snprintf(scratch, sizeof(scratch), "%s/case-%s-%d", SV_PATH_PREFIX, name, getpid());
	sv_rm_rf(scratch);
	sv_mkdir_p(scratch);
	return scratch;
}

static int zombie(pid_t pid)
{
	char path[64], buf[512];
	snprintf(path, sizeof(path), "/proc/%d/stat", pid);
	if (sv_read_file(path, buf, sizeof(buf)) < 0)
		return 0;
	char *rp = strrchr(buf, ')');
	return rp && rp[2] == 'Z';
}

/* ---- The reaper -------------------------------------------------------- */

/*
 * The launched runtime process's exit outside an end is reported by
 * exited with its generation; inside an end it is part of the end and
 * nothing is reported. A worker's status is kept for its step, and a
 * process reparented to the supervisor is collected with nothing
 * reported.
 */
SV_CASE(children_reap_record_by_kind)
{
	setup();
	procs.slot[SV_CHILD_RUNTIME].pid = 100;
	procs.runtime_gen = 6;
	sv_reap_record(&procs, 100, 3 << 8);
	SV_CHECK(procs.exited_pending, "exited is pending");
	SV_EQ_INT(procs.exited_gen, 6);
	SV_EQ_INT(SV_WEXITSTATUS(procs.exited_status), 3);
	SV_EQ_INT(procs.slot[SV_CHILD_RUNTIME].pid, 0);

	procs.exited_pending = 0;
	procs.slot[SV_CHILD_RUNTIME].pid = 101;
	procs.in_end = 1;
	sv_reap_record(&procs, 101, SV_SIGKILL);
	SV_CHECK(!procs.exited_pending, "an exit during an end is part of the end");
	SV_EQ_INT(procs.slot[SV_CHILD_RUNTIME].pid, 0);
	procs.in_end = 0;

	procs.slot[SV_CHILD_SWEEP].pid = 200;
	sv_reap_record(&procs, 200, 2 << 8);
	SV_CHECK(procs.slot[SV_CHILD_SWEEP].reaped, "the sweep worker's status is kept");
	SV_EQ_INT(SV_WEXITSTATUS(procs.slot[SV_CHILD_SWEEP].status), 2);

	procs.slot[SV_CHILD_CLEANUP].pid = 300;
	sv_reap_record(&procs, 300, 0);
	SV_CHECK(procs.slot[SV_CHILD_CLEANUP].reaped, "the cleanup worker's status is kept");

	/* A reparented process, during an end or not, changes nothing. */
	struct sv_procs before = procs;
	sv_reap_record(&procs, 999, 0);
	procs.in_end = 1;
	sv_reap_record(&procs, 998, SV_SIGKILL);
	procs.in_end = 0;
	SV_CHECK(memcmp(&before.slot, &procs.slot, sizeof(procs.slot)) == 0, "slots unchanged");
	SV_CHECK(!procs.exited_pending, "nothing reported for a reparented process");
	return 0;
}

/* The launched runtime process exiting 0, non-zero, and by SIGKILL is reported by exited. */
SV_CASE(children_runtime_exit_status_reported)
{
	static const int codes[] = {0, 7, -SIGKILL};
	setup();
	for (size_t i = 0; i < 3; i++) {
		pid_t pid = fork();
		if (pid == 0) {
			if (codes[i] < 0)
				raise(-codes[i]);
			_exit(codes[i]);
		}
		procs.exited_pending = 0;
		procs.slot[SV_CHILD_RUNTIME].pid = pid;
		procs.runtime_gen = 2;
		while (!procs.exited_pending)
			sv_wait_until(&waiter, sv_now_ms() + 1000);
		char out[SV_MAX_REPLY];
		out[sv_fmt_exited(out, procs.exited_gen, procs.exited_status)] = 0;
		char want[SV_MAX_REPLY];
		if (codes[i] < 0)
			snprintf(want, sizeof(want), "{\"type\":\"exited\",\"gen\":2,\"signal\":%d}\n", -codes[i]);
		else
			snprintf(want, sizeof(want), "{\"type\":\"exited\",\"gen\":2,\"status\":%d}\n", codes[i]);
		SV_EQ_STR(out, want);
	}
	return 0;
}

static int body_exit(void *arg)
{
	return (int)(long)arg;
}

static int body_sleep(void *arg)
{
	(void)arg;
	for (;;)
		pause();
	return 0;
}

static int body_killself(void *arg)
{
	(void)arg;
	signal(SIGKILL, SIG_DFL);
	kill(getpid(), SIGKILL);
	return 0;
}

static int worker_conn_fd;

/* The worker body checks that the CH-SUPERVISE descriptor is closed. */
static int body_conn_closed(void *arg)
{
	(void)arg;
	return fcntl(worker_conn_fd, F_GETFD) == -1 && errno == EBADF ? 0 : 1;
}

/*
 * The sweep worker and the cleanup worker cells: exit 0, non-zero (the
 * bound and the deadline statuses included), a signal, and a status that
 * does not reach the waiting step, each mapped to its reply error.
 */
SV_CASE(children_worker_cells)
{
	struct {
		enum sv_child_kind kind;
		sv_worker_body body;
		long arg;
		int lose_status;
		enum sv_error want;
	} cells[] = {
		{SV_CHILD_SWEEP, body_exit, SV_WORKER_OK, 0, SV_ERR_NONE},
		{SV_CHILD_SWEEP, body_exit, SV_WORKER_BOUND, 0, SV_ERR_SWEEP_BOUND},
		{SV_CHILD_SWEEP, body_exit, SV_WORKER_FAILED, 0, SV_ERR_SWEEP_FAILED},
		{SV_CHILD_SWEEP, body_exit, SV_WORKER_DEADLINE, 0, SV_ERR_SWEEP_FAILED},
		{SV_CHILD_SWEEP, body_killself, 0, 0, SV_ERR_SWEEP_FAILED},
		{SV_CHILD_SWEEP, body_exit, SV_WORKER_OK, 1, SV_ERR_SWEEP_FAILED},
		{SV_CHILD_CLEANUP, body_exit, SV_WORKER_OK, 0, SV_ERR_NONE},
		{SV_CHILD_CLEANUP, body_exit, SV_WORKER_DEADLINE, 0, SV_ERR_CLEANUP_TIMEOUT},
		{SV_CHILD_CLEANUP, body_exit, SV_WORKER_FAILED, 0, SV_ERR_CLEANUP_FAILED},
		{SV_CHILD_CLEANUP, body_exit, SV_WORKER_BOUND, 0, SV_ERR_CLEANUP_FAILED},
		{SV_CHILD_CLEANUP, body_killself, 0, 0, SV_ERR_CLEANUP_FAILED},
		{SV_CHILD_CLEANUP, body_exit, SV_WORKER_OK, 1, SV_ERR_CLEANUP_FAILED},
	};
	setup();
	for (size_t i = 0; i < sizeof(cells) / sizeof(cells[0]); i++) {
		int pid = sv_spawn_worker(-1, cells[i].body, (void *)cells[i].arg);
		SV_CHECK(pid > 0, "spawn %zu", i);
		if (cells[i].lose_status) {
			/* The status goes elsewhere: the reaper never files it against the step. */
			int st;
			waitpid(pid, &st, 0);
		}
		procs.slot[cells[i].kind].pid = pid;
		int status = 0;
		int w = sv_wait_child(&waiter, cells[i].kind, 5000, &status);
		enum sv_error got = cells[i].kind == SV_CHILD_SWEEP ? sv_sweep_result(w == 0, status)
								     : sv_cleanup_result(w == 0, status);
		if (got != cells[i].want)
			SV_FAIL("cell %zu: error %s, want %s (wait %d, status %d)", i, sv_error_name(got),
				sv_error_name(cells[i].want), w, status);
		SV_EQ_INT(procs.slot[cells[i].kind].pid, 0);
	}
	return 0;
}

/* A status the reaper takes before the waiting step starts still reaches that step. */
SV_CASE(children_status_reaped_before_its_step)
{
	setup();
	int pid = sv_spawn_worker(-1, body_exit, (void *)(long)SV_WORKER_BOUND);
	procs.slot[SV_CHILD_SWEEP].pid = pid;
	while (!zombie(pid))
		sv_sleep(1);
	sv_reap_all(&procs);
	SV_CHECK(procs.slot[SV_CHILD_SWEEP].reaped, "the reaper filed the status");
	int status = 0;
	SV_EQ_INT(sv_wait_child(&waiter, SV_CHILD_SWEEP, 0, &status), 0);
	SV_EQ_INT(sv_sweep_result(1, status), SV_ERR_SWEEP_BOUND);
	return 0;
}

/* A waiting step whose child the reaper reports as gone with no status fails. */
SV_CASE(children_waiting_step_finds_no_status)
{
	setup();
	pid_t pid = fork();
	if (pid == 0)
		_exit(0);
	int st;
	waitpid(pid, &st, 0);
	procs.slot[SV_CHILD_CLEANUP].pid = pid;
	int status = 0;
	SV_EQ_INT(sv_wait_child(&waiter, SV_CHILD_CLEANUP, 0, &status), -1);
	SV_EQ_INT(sv_cleanup_result(0, status), SV_ERR_CLEANUP_FAILED);
	return 0;
}

/* A worker that outlives its backstop is sent SIGKILL, and its signalled status fails the step. */
SV_CASE(children_worker_backstop_kills)
{
	setup();
	int pid = sv_spawn_worker(-1, body_sleep, NULL);
	procs.slot[SV_CHILD_SWEEP].pid = pid;
	unsigned long long t0 = sv_ms_now();
	int status = 0;
	SV_EQ_INT(sv_wait_child(&waiter, SV_CHILD_SWEEP, 100, &status), 0);
	SV_CHECK(SV_WIFSIGNALED(status) && SV_WTERMSIG(status) == SV_SIGKILL, "status %d", status);
	SV_CHECK(sv_ms_now() - t0 < 3000, "the backstop fired");
	SV_EQ_INT(sv_sweep_result(1, status), SV_ERR_SWEEP_FAILED);
	return 0;
}

/* A close of the connection during a waiting step ends the wait. */
SV_CASE(children_wait_ends_on_closed_connection)
{
	setup();
	int sp[2];
	SV_EQ_INT(socketpair(AF_UNIX, SOCK_STREAM | SOCK_CLOEXEC, 0, sp), 0);
	waiter.conn_fd = sp[0];
	close(sp[1]);
	int pid = sv_spawn_worker(sp[0], body_sleep, NULL);
	procs.slot[SV_CHILD_CLEANUP].pid = pid;
	int status;
	SV_EQ_INT(sv_wait_child(&waiter, SV_CHILD_CLEANUP, 0, &status), 1);
	SV_CHECK(waiter.closed, "closed is set");
	kill(pid, SIGKILL);
	return 0;
}

/* A forked worker closes the CH-SUPERVISE descriptor before anything else. */
SV_CASE(workers_close_supervise_descriptor)
{
	setup();
	int sp[2];
	SV_EQ_INT(socketpair(AF_UNIX, SOCK_STREAM | SOCK_CLOEXEC, 0, sp), 0);
	worker_conn_fd = sp[0];
	int pid = sv_spawn_worker(sp[0], body_conn_closed, NULL);
	procs.slot[SV_CHILD_SWEEP].pid = pid;
	int status = 0;
	SV_EQ_INT(sv_wait_child(&waiter, SV_CHILD_SWEEP, 5000, &status), 0);
	SV_CHECK(SV_WIFEXITED(status) && SV_WEXITSTATUS(status) == 0, "the worker held the descriptor");
	return 0;
}

/* The pure classifications of the Children table. */
SV_CASE(children_result_tables)
{
	SV_EQ_INT(sv_sweep_result(0, 0), SV_ERR_SWEEP_FAILED);
	SV_EQ_INT(sv_sweep_result(1, SV_SIGKILL), SV_ERR_SWEEP_FAILED);
	SV_EQ_INT(sv_sweep_result(1, 0), SV_ERR_NONE);
	SV_EQ_INT(sv_sweep_result(1, SV_WORKER_BOUND << 8), SV_ERR_SWEEP_BOUND);
	SV_EQ_INT(sv_sweep_result(1, SV_WORKER_FAILED << 8), SV_ERR_SWEEP_FAILED);
	SV_EQ_INT(sv_cleanup_result(0, 0), SV_ERR_CLEANUP_FAILED);
	SV_EQ_INT(sv_cleanup_result(1, SV_SIGKILL), SV_ERR_CLEANUP_FAILED);
	SV_EQ_INT(sv_cleanup_result(1, 0), SV_ERR_NONE);
	SV_EQ_INT(sv_cleanup_result(1, SV_WORKER_DEADLINE << 8), SV_ERR_CLEANUP_TIMEOUT);
	SV_EQ_INT(sv_cleanup_result(1, SV_WORKER_FAILED << 8), SV_ERR_CLEANUP_FAILED);
	/* A cleanup command: the deadline prevails even over an exit; WIFSIGNALED before WEXITSTATUS. */
	SV_EQ_INT(sv_cleanup_decide(1, 0, 100, 100), SV_WORKER_DEADLINE);
	SV_EQ_INT(sv_cleanup_decide(1, SV_SIGKILL, 100, 100), SV_WORKER_DEADLINE);
	SV_EQ_INT(sv_cleanup_decide(0, 0, 101, 100), SV_WORKER_DEADLINE);
	SV_EQ_INT(sv_cleanup_decide(1, 0, 99, 100), SV_WORKER_OK);
	SV_EQ_INT(sv_cleanup_decide(1, 1 << 8, 99, 100), SV_WORKER_FAILED);
	SV_EQ_INT(sv_cleanup_decide(1, SV_SIGTERM, 99, 100), SV_WORKER_FAILED);
	SV_EQ_INT(sv_cleanup_decide(0, 0, 99, 100), -1);
	return 0;
}

/* ---- The end ------------------------------------------------------------- */

static int kill_sigs[256];
static unsigned long long kill_times[256];
static int nkill;
static int fake_target = -1;

static int fake_kill(int sig)
{
	if (nkill < 256) {
		kill_sigs[nkill] = sig;
		kill_times[nkill] = sv_ms_now();
		nkill++;
	}
	if (fake_target > 0)
		kill(fake_target, sig);
	return 0;
}

static long count_script[16];
static int count_len, count_pos;

static long fake_count(struct sv_procs *p)
{
	(void)p;
	if (count_pos < count_len)
		return count_script[count_pos++];
	return count_script[count_len - 1];
}

static void setup_end(const long *script, int n)
{
	setup();
	nkill = 0;
	fake_target = -1;
	for (int i = 0; i < n; i++)
		count_script[i] = script[i];
	count_len = n;
	count_pos = 0;
	procs.kill_all = fake_kill;
	procs.count_others_fn = fake_count;
	procs.pids_current_path = "/nonexistent/pids.current";
}

/* The end attests only after two consecutive clean passes. */
SV_CASE(end_requires_two_consecutive_clean_passes)
{
	const long script[] = {0, 1, 0, 0};
	setup_end(script, 4);
	SV_EQ_INT(sv_end_pass(&waiter, 0, 5000), SV_ERR_NONE);
	SV_EQ_INT(count_pos, 4);
	SV_EQ_INT(kill_sigs[0], SV_SIGTERM);
	for (int i = 1; i < nkill; i++)
		SV_EQ_INT(kill_sigs[i], SV_SIGKILL);
	SV_EQ_INT(nkill, 5);
	SV_CHECK(!procs.in_end, "the end flag is cleared");
	return 0;
}

/* A process that survives every pass yields the end deadline error. */
SV_CASE(end_deadline_when_a_process_remains)
{
	const long script[] = {1};
	setup_end(script, 1);
	unsigned long long t0 = sv_ms_now();
	SV_EQ_INT(sv_end_pass(&waiter, 0, 100), SV_ERR_END_DEADLINE);
	SV_CHECK(sv_ms_now() - t0 < 2000, "bounded");
	return 0;
}

/* A failed /proc scan yields the /proc read failure error. */
SV_CASE(end_proc_read_failure)
{
	const long script[] = {-SV_EACCES};
	setup_end(script, 1);
	SV_EQ_INT(sv_end_pass(&waiter, 0, 1000), SV_ERR_PROC_READ);
	return 0;
}

/* SIGTERM, the request's grace, then SIGKILL. */
SV_CASE(end_sigterm_grace_then_sigkill)
{
	const long script[] = {0};
	setup_end(script, 1);
	SV_EQ_INT(sv_end_pass(&waiter, 150, 1000), SV_ERR_NONE);
	SV_EQ_INT(kill_sigs[0], SV_SIGTERM);
	SV_EQ_INT(kill_sigs[1], SV_SIGKILL);
	SV_CHECK(kill_times[1] - kill_times[0] >= 140, "grace honored: %llu ms",
		 kill_times[1] - kill_times[0]);
	return 0;
}

/* The runtime process ending during an end is part of the end: no exited is pending. */
SV_CASE(end_runtime_exit_during_end_is_not_reported)
{
	const long script[] = {0};
	setup_end(script, 1);
	pid_t pid = fork();
	if (pid == 0) {
		for (;;)
			pause();
	}
	procs.slot[SV_CHILD_RUNTIME].pid = pid;
	procs.runtime_gen = 3;
	fake_target = pid;
	SV_EQ_INT(sv_end_pass(&waiter, 0, 2000), SV_ERR_NONE);
	SV_CHECK(!procs.exited_pending, "no exited for an exit during the end");
	SV_EQ_INT(procs.slot[SV_CHILD_RUNTIME].pid, 0);
	return 0;
}

/* A close of the connection during the grace ends the end with closed set. */
SV_CASE(end_closed_connection_during_grace)
{
	const long script[] = {0};
	setup_end(script, 1);
	int sp[2];
	SV_EQ_INT(socketpair(AF_UNIX, SOCK_STREAM | SOCK_CLOEXEC, 0, sp), 0);
	waiter.conn_fd = sp[0];
	close(sp[1]);
	unsigned long long t0 = sv_ms_now();
	sv_end_pass(&waiter, 10000, 1000);
	SV_CHECK(waiter.closed, "closed is set");
	SV_CHECK(sv_ms_now() - t0 < 2000, "the close ended the grace");
	return 0;
}

/*
 * sv_read_pids_current: a value, an absent file, a file that cannot be
 * opened (a dangling symbolic link refused by O_NOFOLLOW, and a mode-000
 * file for a non-root caller), and a file that opened but yielded no
 * value.
 */
SV_CASE(end_read_pids_current_results)
{
	const char *d = scratch_dir("pidscur");
	char p[4200];
	snprintf(p, sizeof(p), "%s/value", d);
	sv_write_file(p, "5\n");
	SV_EQ_INT(sv_read_pids_current(p), 5);
	snprintf(p, sizeof(p), "%s/absent", d);
	SV_EQ_INT(sv_read_pids_current(p), -1);
	snprintf(p, sizeof(p), "%s/link", d);
	SV_EQ_INT(symlink("/nonexistent-target", p), 0);
	SV_EQ_INT(sv_read_pids_current(p), -1);
	if (getuid() != 0) {
		snprintf(p, sizeof(p), "%s/unreadable", d);
		sv_write_file(p, "2\n");
		chmod(p, 0);
		SV_EQ_INT(sv_read_pids_current(p), -1);
	}
	snprintf(p, sizeof(p), "%s/empty", d);
	sv_write_file(p, "");
	SV_EQ_INT(sv_read_pids_current(p), -2);
	snprintf(p, sizeof(p), "%s/garbage", d);
	sv_write_file(p, "max\n");
	SV_EQ_INT(sv_read_pids_current(p), -2);
	sv_rm_rf(d);
	return 0;
}

/*
 * pids.current is read only where readable. With two clean /proc passes,
 * a pids.current that exists but cannot be read (a symbolic link refused
 * with ELOOP, a mode-000 file, an empty file, and an unparsable one)
 * leaves the end attested with no error; a readable value of 1 attests
 * and a readable value above 1 keeps the end from attesting.
 */
SV_CASE(end_unreadable_pids_current_is_skipped)
{
	const char *d = scratch_dir("pidsend");
	char link[4200], noperm[4200], empty[4200], garbage[4200], one[4200], three[4200];
	snprintf(link, sizeof(link), "%s/link", d);
	symlink("/nonexistent-target", link);
	snprintf(noperm, sizeof(noperm), "%s/noperm", d);
	sv_write_file(noperm, "1\n");
	chmod(noperm, 0);
	snprintf(empty, sizeof(empty), "%s/empty", d);
	sv_write_file(empty, "");
	snprintf(garbage, sizeof(garbage), "%s/garbage", d);
	sv_write_file(garbage, "x\n");
	snprintf(one, sizeof(one), "%s/one", d);
	sv_write_file(one, "1\n");
	snprintf(three, sizeof(three), "%s/three", d);
	sv_write_file(three, "3\n");
	struct {
		const char *path;
		enum sv_error want;
	} cells[] = {
		{link, SV_ERR_NONE},  {noperm, SV_ERR_NONE}, {empty, SV_ERR_NONE},
		{garbage, SV_ERR_NONE}, {one, SV_ERR_NONE},  {three, SV_ERR_END_DEADLINE},
	};
	for (size_t i = 0; i < sizeof(cells) / sizeof(cells[0]); i++) {
		const long script[] = {0};
		setup_end(script, 1);
		procs.pids_current_path = cells[i].path;
		enum sv_error got = sv_end_pass(&waiter, 0, 100);
		if (got != cells[i].want)
			SV_FAIL("%s: %s, want %s", cells[i].path, sv_error_name(got), sv_error_name(cells[i].want));
	}
	sv_rm_rf(d);
	return 0;
}

/* sv_count_others counts numeric /proc entries other than self, and fails on an unreadable directory. */
SV_CASE(end_count_others_scan)
{
	const char *d = scratch_dir("procscan");
	char p[4200];
	static const char *const names[] = {"1", "42", "77", "self", "sys", "123abc"};
	for (size_t i = 0; i < 6; i++) {
		snprintf(p, sizeof(p), "%s/%s", d, names[i]);
		sv_mkdir_p(p);
	}
	SV_EQ_INT(sv_count_others(d, 42), 2);
	SV_EQ_INT(sv_count_others(d, 5), 3);
	snprintf(p, sizeof(p), "%s/missing", d);
	SV_CHECK(sv_count_others(p, 1) < 0, "a missing directory is a read failure");
	sv_rm_rf(d);
	return 0;
}

/* ---- The cleanup worker ---------------------------------------------------- */

static char probe_path[4096];

static char **cmd(const char *a, const char *b, const char *c)
{
	static char *store[8][6];
	static int next;
	char **v = store[next++ % 8];
	sv_bin_path(probe_path, sizeof(probe_path), "probe");
	v[0] = strdup(probe_path);
	v[1] = (char *)a;
	v[2] = (char *)b;
	v[3] = (char *)c;
	v[4] = NULL;
	return v;
}

static char *cleanup_env[] = {SV_FIXED_PATH, "LENNY_PREV_LEASE_ID=lease-9", NULL};

/* Commands run in order, and the worker exits 0 after the last. */
SV_CASE(cleanup_commands_run_in_order)
{
	setup();
	const char *d = scratch_dir("cl-order");
	char a[4200], b[4200];
	snprintf(a, sizeof(a), "%s/a", d);
	snprintf(b, sizeof(b), "%s/b", d);
	char **cmds[] = {cmd("touch", a, NULL), cmd("touch", b, NULL)};
	SV_EQ_INT(sv_cleanup_worker(cmds, 2, cleanup_env, 10000), SV_WORKER_OK);
	SV_CHECK(sv_path_exists(a) && sv_path_exists(b), "both commands ran");
	SV_EQ_INT(sv_cleanup_worker(cmds, 0, cleanup_env, 10000), SV_WORKER_OK);
	sv_rm_rf(d);
	return 0;
}

/* A command that exits non-zero, ends by a signal, or fails its execve stops the worker with its failure status. */
SV_CASE(cleanup_failed_command_stops_the_worker)
{
	setup();
	const char *d = scratch_dir("cl-fail");
	char after[4200];
	snprintf(after, sizeof(after), "%s/after", d);
	char *bad_exec[] = {"/nonexistent/cleanup-command", NULL};
	char **first[] = {cmd("exit", "3", NULL), cmd("killself", "9", NULL), bad_exec};
	for (int i = 0; i < 3; i++) {
		char **cmds[] = {first[i], cmd("touch", after, NULL)};
		SV_EQ_INT(sv_cleanup_worker(cmds, 2, cleanup_env, 10000), SV_WORKER_FAILED);
		SV_CHECK(!sv_path_exists(after), "case %d: no command runs after a failure", i);
	}
	sv_rm_rf(d);
	return 0;
}

/*
 * A cleanup command whose program names no directory is looked up in the
 * PATH of the launch environment, element by element: a missing or
 * non-executable candidate moves the search on, a relative element is
 * skipped, a name found on no element fails the command with the exec
 * failure status, and an absolute program path is run without a search.
 *
 * spec: §4.7.10 (Runtime supervisor, Cleanup; Environment)
 */
SV_CASE(cleanup_bare_program_name_searches_launch_path)
{
	setup();
	const char *d = scratch_dir("cl-path");
	char root[4200], miss[4300], noexec[4300], bin[4300], rel[4300];
	snprintf(root, sizeof(root), "%s", d);
	snprintf(miss, sizeof(miss), "%s/missing", root);
	snprintf(noexec, sizeof(noexec), "%s/noexec", root);
	snprintf(bin, sizeof(bin), "%s/bin", root);
	snprintf(rel, sizeof(rel), "%s/rel", root);
	sv_mkdir_p(noexec);
	sv_mkdir_p(bin);
	sv_mkdir_p(rel);
	sv_bin_path(probe_path, sizeof(probe_path), "probe");
	char tgt[4400];
	/* A same-named file without execute permission precedes the program. */
	snprintf(tgt, sizeof(tgt), "%s/sv-path-probe", noexec);
	sv_write_file(tgt, "#!/bin/false\n");
	SV_EQ_INT(chmod(tgt, 0644), 0);
	snprintf(tgt, sizeof(tgt), "%s/sv-path-probe", bin);
	SV_EQ_INT(symlink(probe_path, tgt), 0);
	snprintf(tgt, sizeof(tgt), "%s/sv-rel-probe", rel);
	SV_EQ_INT(symlink(probe_path, tgt), 0);

	char pathvar[16384];
	snprintf(pathvar, sizeof(pathvar), "PATH=%s::rel:%s:%s", miss, noexec, bin);
	char *env[] = {pathvar, NULL};
	char marker[4400];
	snprintf(marker, sizeof(marker), "%s/ran", root);

	char *found[] = {"sv-path-probe", "touch", marker, NULL};
	char **ok[] = {found};
	SV_EQ_INT(sv_cleanup_worker(ok, 1, env, 10000), SV_WORKER_OK);
	SV_CHECK(sv_path_exists(marker), "the bare name ran from the PATH element that holds it");
	unlink(marker);

	/* The program sits only under a relative element, resolved from the working directory. */
	char cwd[4200];
	SV_CHECK(getcwd(cwd, sizeof(cwd)) != NULL, "getcwd");
	SV_EQ_INT(chdir(root), 0);
	char *relonly[] = {"sv-rel-probe", "touch", marker, NULL};
	char *absent[] = {"sv-absent-probe", "touch", marker, NULL};
	char *absolute[] = {"/nonexistent-dir/sv-path-probe", "touch", marker, NULL};
	char **const *bad[] = {(char **[]){relonly}, (char **[]){absent}, (char **[]){absolute}};
	int rs[3];
	for (int i = 0; i < 3; i++)
		rs[i] = sv_cleanup_worker(bad[i], 1, env, 10000);
	SV_EQ_INT(chdir(cwd), 0);
	for (int i = 0; i < 3; i++) {
		SV_EQ_INT(rs[i], SV_WORKER_FAILED);
		SV_CHECK(!sv_path_exists(marker), "case %d: nothing ran", i);
	}

	/* An absolute program path keeps running without a search. */
	char *abs_ok[] = {probe_path, "touch", marker, NULL};
	char **absc[] = {abs_ok};
	SV_EQ_INT(sv_cleanup_worker(absc, 1, env, 10000), SV_WORKER_OK);
	SV_CHECK(sv_path_exists(marker), "the absolute program ran");
	sv_rm_rf(d);
	return 0;
}

/*
 * At the deadline the worker sends SIGKILL to the running command's
 * process group, collects the command, and exits with its deadline
 * status. A process in the command's group ends with it.
 */
SV_CASE(cleanup_deadline_kills_the_process_group)
{
	setup();
	prctl(PR_SET_CHILD_SUBREAPER, 1);
	const char *d = scratch_dir("cl-deadline");
	char pidfile[4200];
	snprintf(pidfile, sizeof(pidfile), "%s/child.pid", d);
	char **cmds[] = {cmd("fork-sleep", pidfile, NULL)};
	unsigned long long t0 = sv_ms_now();
	SV_EQ_INT(sv_cleanup_worker(cmds, 1, cleanup_env, 300), SV_WORKER_DEADLINE);
	unsigned long long took = sv_ms_now() - t0;
	SV_CHECK(took >= 290 && took < 3000, "deadline after %llu ms", took);
	char buf[64];
	SV_CHECK(sv_read_file(pidfile, buf, sizeof(buf)) > 0, "the command forked");
	pid_t child = (pid_t)atoi(buf);
	int st = 0;
	unsigned long long end = sv_ms_now() + 3000;
	pid_t r;
	while ((r = waitpid(child, &st, WNOHANG)) == 0 && sv_ms_now() < end)
		sv_sleep(5);
	SV_CHECK(r == child && WIFSIGNALED(st) && WTERMSIG(st) == SIGKILL,
		 "the group member was killed (r %d status %d)", r, st);
	sv_rm_rf(d);
	return 0;
}

/*
 * A deadline that falls before the command has called setsid still
 * reaches the command: the worker sends SIGKILL to the command itself as
 * well as to its process group, so it does not wait on a command that
 * the group kill missed.
 */
SV_CASE(cleanup_deadline_before_setsid_kills_the_command)
{
	setup();
	sv_test_pre_setsid_delay_ms = 400;
	char **cmds[] = {cmd("sleep", NULL, NULL)};
	unsigned long long t0 = sv_ms_now();
	SV_EQ_INT(sv_cleanup_worker(cmds, 1, cleanup_env, 50), SV_WORKER_DEADLINE);
	unsigned long long took = sv_ms_now() - t0;
	SV_CHECK(took < 350, "the worker returned at the deadline, after %llu ms", took);
	int st;
	SV_EQ_INT(waitpid(-1, &st, WNOHANG), -1);
	return 0;
}

static unsigned long long clock_script[8];
static int clock_len, clock_pos;

static sv_u64 scripted_now(void)
{
	if (clock_pos < clock_len)
		return clock_script[clock_pos++];
	return clock_script[clock_len - 1];
}

/*
 * The time the worker waits is computed from the same clock reading as
 * its deadline test, so a tick that crosses the deadline between two
 * readings cannot wrap the wait to an unbounded timeout. The scripted
 * clock reads 1 ms before the deadline for the test and 1 ms after it
 * for any second reading.
 */
SV_CASE(cleanup_deadline_wait_does_not_underflow)
{
	setup();
	clock_script[0] = 1000; /* the worker's start: the deadline is 1100 */
	clock_script[1] = 1099; /* the first iteration's deadline test */
	clock_script[2] = 1101; /* any later reading */
	clock_len = 3;
	clock_pos = 0;
	sv_now_hook = scripted_now;
	char **cmds[] = {cmd("sleep", NULL, NULL)};
	unsigned long long t0 = sv_ms_now();
	int r = sv_cleanup_worker(cmds, 1, cleanup_env, 100);
	sv_now_hook = NULL;
	SV_EQ_INT(r, SV_WORKER_DEADLINE);
	SV_CHECK(sv_ms_now() - t0 < 2000, "the worker reached the deadline");
	return 0;
}

/* A command that exits as the deadline falls: the deadline status prevails. */
SV_CASE(cleanup_exit_at_deadline_deadline_prevails)
{
	setup();
	clock_script[0] = 1000;
	clock_script[1] = 5000;
	clock_len = 2;
	clock_pos = 0;
	sv_now_hook = scripted_now;
	char **cmds[] = {cmd("exit", "0", NULL)};
	int r = sv_cleanup_worker(cmds, 1, cleanup_env, 100);
	sv_now_hook = NULL;
	SV_EQ_INT(r, SV_WORKER_DEADLINE);
	return 0;
}

static int hook_wait4_delay(long nr)
{
	static int done;
	if (nr == SV_NR_wait4 && !done) {
		done = 1;
		sv_sleep(300);
	}
	return 0;
}

/* A command whose status does not reach the worker is a failure, as a non-zero exit is. */
SV_CASE(cleanup_command_status_lost_is_failure)
{
	setup();
	/* With SIGCHLD ignored the kernel discards the command's status. */
	signal(SIGCHLD, SIG_IGN);
	sv_eintr_hook = hook_wait4_delay;
	char **cmds[] = {cmd("exit", "0", NULL)};
	int r = sv_cleanup_worker(cmds, 1, cleanup_env, 10000);
	sv_eintr_hook = NULL;
	SV_EQ_INT(r, SV_WORKER_FAILED);
	return 0;
}

/*
 * Each command runs in a new session with an empty signal mask, no
 * descriptor beyond the standard streams, the worker's working
 * directory, and the environment the worker was given.
 */
SV_CASE(cleanup_command_mask_descriptors_and_environment)
{
	setup();
	const char *d = scratch_dir("cl-report");
	char report[4200];
	snprintf(report, sizeof(report), "%s/report", d);
	int keep = open("/proc/self/stat", O_RDONLY | O_CLOEXEC);
	SV_CHECK(keep >= 0, "open");
	SV_EQ_INT(chdir(d), 0);
	char **cmds[] = {cmd("report", report, "exit")};
	SV_EQ_INT(sv_cleanup_worker(cmds, 1, cleanup_env, 10000), SV_WORKER_OK);
	char buf[16384];
	SV_CHECK(sv_read_file(report, buf, sizeof(buf)) > 0, "report");
	SV_CHECK(strstr(buf, "SigBlk:\t0000000000000000"), "empty mask:\n%s", buf);
	char line[128];
	int pid = 0, sid = -1;
	char *p = strstr(buf, "Pid: ");
	if (p)
		pid = atoi(p + 5);
	p = strstr(buf, "Sid: ");
	if (p)
		sid = atoi(p + 5);
	SV_CHECK(pid > 0 && pid == sid, "own session: pid %d sid %d", pid, sid);
	snprintf(line, sizeof(line), "Cwd: %s\n", d);
	SV_CHECK(strstr(buf, line), "cwd:\n%s", buf);
	for (char *q = strstr(buf, "Fd: "); q; q = strstr(q + 1, "Fd: ")) {
		int fd = atoi(q + 4);
		SV_CHECK(fd <= 2, "descriptor %d leaked:\n%s", fd, buf);
	}
	SV_CHECK(strstr(buf, "Env: " SV_FIXED_PATH "\n"), "PATH");
	SV_CHECK(strstr(buf, "Env: LENNY_PREV_LEASE_ID=lease-9\n"), "metadata");
	SV_CHECK(!strstr(buf, "Env: HOME="), "no inherited environment:\n%s", buf);
	sv_rm_rf(d);
	return 0;
}

/* ---- The launch child -------------------------------------------------------- */

/* A launch whose execve fails reports the errno; the child exits with the exec failure status. */
SV_CASE(launch_exec_failure_reported)
{
	setup();
	char *argv[] = {"/nonexistent/runtime", NULL};
	char *env[] = {SV_FIXED_PATH, NULL};
	int e = 0;
	int pid = sv_launch_child(argv, env, &e);
	SV_CHECK(pid > 0, "fork");
	SV_EQ_INT(e, SV_ENOENT);
	int st;
	waitpid(pid, &st, 0);
	SV_CHECK(WIFEXITED(st) && WEXITSTATUS(st) == SV_EXEC_FAILED_STATUS, "status %d", st);
	return 0;
}

/* The launched process starts a new session with no descriptor beyond the standard streams. */
SV_CASE(launch_child_session_and_descriptors)
{
	setup();
	const char *d = scratch_dir("launch");
	char report[4200];
	snprintf(report, sizeof(report), "%s/report", d);
	int keep = socket(AF_UNIX, SOCK_STREAM | SOCK_CLOEXEC, 0);
	SV_CHECK(keep >= 0, "socket");
	char **argv = cmd("report", report, "exit");
	char *env[] = {SV_FIXED_PATH, "VIRTUAL_ENV=/opt/venv", NULL};
	int e = 0;
	int pid = sv_launch_child(argv, env, &e);
	SV_CHECK(pid > 0 && e == 0, "launch");
	int st;
	waitpid(pid, &st, 0);
	char buf[16384];
	SV_CHECK(sv_read_file(report, buf, sizeof(buf)) > 0, "report");
	char want[64];
	snprintf(want, sizeof(want), "Sid: %d\n", pid);
	SV_CHECK(strstr(buf, want), "the child leads its own session:\n%s", buf);
	for (char *q = strstr(buf, "Fd: "); q; q = strstr(q + 1, "Fd: "))
		SV_CHECK(atoi(q + 4) <= 2, "descriptor leaked:\n%s", buf);
	SV_CHECK(strstr(buf, "Env: VIRTUAL_ENV=/opt/venv\n"), "env");
	sv_rm_rf(d);
	return 0;
}

/* ---- EINTR --------------------------------------------------------------------- */

/*
 * EINTR injected into every other attempt of every wrapped call changes
 * no result: each wrapper retries, and the wait loops tolerate an
 * interrupted ppoll.
 */
SV_CASE(eintr_injected_into_every_wrapped_call)
{
	setup();
	const char *d = scratch_dir("eintr");
	char p[4200], tree[4200], a[4200];
	snprintf(p, sizeof(p), "%s/pids.current", d);
	sv_write_file(p, "1\n");
	snprintf(tree, sizeof(tree), "%s/tree/sub", d);
	sv_mkdir_p(tree);
	snprintf(a, sizeof(a), "%s/tree/sub/file", d);
	sv_write_file(a, "x");
	snprintf(tree, sizeof(tree), "%s/tree", d);
	char touched[4200];
	snprintf(touched, sizeof(touched), "%s/touched", d);

	sv_eintr_injected = 0;
	sv_eintr_hook = sv_eintr_every_other;

	SV_EQ_INT(sv_read_pids_current(p), 1);
	SV_CHECK(sv_count_others("/proc", getpid()) > 0, "count");
	struct sv_sweep_stats st;
	memset(&st, 0, sizeof(st));
	sv_sweep_tree(tree, getuid(), NULL, 8, 1000, &st);
	SV_CHECK(!st.failed && !st.bound_reached, "sweep");
	SV_CHECK(!sv_path_exists(a), "the sweep removed the file");
	char **cmds[] = {cmd("touch", touched, NULL)};
	SV_EQ_INT(sv_cleanup_worker(cmds, 1, cleanup_env, 10000), SV_WORKER_OK);
	SV_CHECK(sv_path_exists(touched), "the command ran");
	char *bad[] = {"/nonexistent/runtime", NULL};
	int e = 0;
	int pid = sv_launch_child(bad, cleanup_env, &e);
	SV_EQ_INT(e, SV_ENOENT);
	procs.slot[SV_CHILD_RUNTIME].pid = pid;
	procs.runtime_gen = 1;
	while (!procs.exited_pending)
		sv_wait_until(&waiter, sv_now_ms() + 200);
	int wp = sv_spawn_worker(-1, body_exit, (void *)(long)SV_WORKER_OK);
	procs.slot[SV_CHILD_SWEEP].pid = wp;
	int status = -1;
	SV_EQ_INT(sv_wait_child(&waiter, SV_CHILD_SWEEP, 5000, &status), 0);
	SV_EQ_INT(status, 0);
	const long script[] = {0};
	procs.kill_all = fake_kill;
	procs.count_others_fn = fake_count;
	count_script[0] = script[0];
	count_len = 1;
	count_pos = 0;
	procs.pids_current_path = p;
	SV_EQ_INT(sv_end_pass(&waiter, 20, 2000), SV_ERR_NONE);

	sv_eintr_hook = NULL;
	SV_CHECK(sv_eintr_injected > 10, "EINTR injected %d times", sv_eintr_injected);
	sv_rm_rf(d);
	return 0;
}
