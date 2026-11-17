/* SPDX-License-Identifier: MIT */
/*
 * Cases that run the freestanding supervisor binary in run mode as the
 * init of a scratch user, PID, mount, and IPC namespace, with the case
 * process as its adapter on CH-SUPERVISE. kill(-1) inside that namespace
 * reaches the namespace alone, and the IPC namespace confines the sweep's
 * System V removal.
 *
 * spec: §4.7.10 (Runtime supervisor: Environment, End, Sweep, Cleanup,
 * Signals, Children); §4.7.11 item 8 (Property self-test); §28.5.3
 * (CH-SUPERVISE).
 */
#define _GNU_SOURCE
#include "harness.h"

#include <errno.h>
#include <fcntl.h>
#include <signal.h>
#include <sys/socket.h>
#include <sys/un.h>

static struct sv_ns ns;
static char line[1 << 16];
static char probe[4096];

#define READY_ALL_PASS                                                                     \
	"{\"type\":\"ready\",\"gen\":0,\"properties\":{\"nondumpable\":\"pass\",\"killReach\":\"pass\"," \
	"\"initSigkillImmune\":\"pass\",\"initSigstopImmune\":\"pass\"}}"

/* need_ns skips a case on a host without unprivileged user namespaces. */
#define NEED_NS()                                                                                  \
	do {                                                                                       \
		if (!sv_ns_userns_available())                                                     \
			SV_SKIP("blocked: unprivileged user namespaces are unavailable on this host"); \
	} while (0)

static char pfx[4096];

static const char *prefixed(const char *rel)
{
	sv_prefix_path(pfx, sizeof(pfx), rel);
	return pfx;
}

/* reset_volumes recreates the prefixed volume tree the supervisor sweeps. */
static void reset_volumes(void)
{
	static const char *const vols[] = {"sessions", "artifacts", "tmp", "dev/shm", "dev/mqueue",
					   "workspace", "workspace/slots"};
	for (size_t i = 0; i < sizeof(vols) / sizeof(vols[0]); i++) {
		char p[4096];
		sv_prefix_path(p, sizeof(p), vols[i]);
		if (i < 6)
			sv_rm_rf(p);
		sv_mkdir_p(p);
	}
}

/*
 * start runs the supervisor with the given flags and runtime argv, accepts
 * its dial, and reads its first message into line.
 */
static int start(const char *const *flags, const char *const *runtime)
{
	sv_bin_path(probe, sizeof(probe), "probe");
	reset_volumes();
	if (sv_ns_listen(&ns) < 0)
		return -1;
	if (sv_ns_start(&ns, sv_ns_run_argv(&ns, flags, runtime)) < 0)
		return -1;
	if (sv_ns_accept(&ns, 10000) < 0)
		return -1;
	return sv_ns_read(&ns, line, sizeof(line), 10000) > 0 ? 0 : -1;
}

static long req(const char *r)
{
	return sv_ns_request(&ns, r, line, sizeof(line), 15000);
}

/* ready is the first message and carries every property; on this kernel each passes. */
SV_CASE(ns_ready_first_with_properties)
{
	NEED_NS();
	const char *rt[] = {"/bin/true-not-launched", NULL};
	SV_EQ_INT(start(NULL, rt), 0);
	SV_EQ_STR(line, READY_ALL_PASS);
	SV_CHECK(req("{\"type\":\"ping\",\"gen\":0,\"id\":1}") > 0, "pong");
	SV_EQ_STR(line, "{\"type\":\"pong\",\"gen\":0,\"id\":1}");
	sv_ns_stop(&ns);
	return 0;
}

/* The launched runtime process exiting with a status, outside any request: exited carries it. */
SV_CASE(ns_launched_runtime_exit_status)
{
	NEED_NS();
	const char *rt[] = {probe, "exit", "3", NULL};
	sv_bin_path(probe, sizeof(probe), "probe");
	SV_EQ_INT(start(NULL, rt), 0);
	SV_CHECK(req("{\"type\":\"launch\",\"gen\":1,\"id\":1}") > 0, "launched");
	SV_EQ_STR(line, "{\"type\":\"launched\",\"gen\":1,\"id\":1}");
	SV_CHECK(sv_ns_read(&ns, line, sizeof(line), 5000) > 0, "exited");
	SV_EQ_STR(line, "{\"type\":\"exited\",\"gen\":1,\"status\":3}");
	sv_ns_stop(&ns);
	return 0;
}

/* The launched runtime process ended by a signal: exited carries the signal. */
SV_CASE(ns_launched_runtime_exit_signal)
{
	NEED_NS();
	sv_bin_path(probe, sizeof(probe), "probe");
	const char *rt[] = {probe, "killself", "9", NULL};
	SV_EQ_INT(start(NULL, rt), 0);
	SV_CHECK(req("{\"type\":\"launch\",\"gen\":1,\"id\":1}") > 0, "launched");
	SV_EQ_STR(line, "{\"type\":\"launched\",\"gen\":1,\"id\":1}");
	SV_CHECK(sv_ns_read(&ns, line, sizeof(line), 5000) > 0, "exited");
	SV_EQ_STR(line, "{\"type\":\"exited\",\"gen\":1,\"signal\":9}");
	sv_ns_stop(&ns);
	return 0;
}

/* An exit reaped before launched is written is reported by launched (the exec failure) and then exited. */
SV_CASE(ns_launch_exec_failure_then_exited)
{
	NEED_NS();
	const char *rt[] = {"/nonexistent/runtime", NULL};
	SV_EQ_INT(start(NULL, rt), 0);
	SV_CHECK(req("{\"type\":\"launch\",\"gen\":1,\"id\":4}") > 0, "launched");
	SV_EQ_STR(line, "{\"type\":\"launched\",\"gen\":1,\"id\":4,\"error\":\"exec_failed\"}");
	SV_CHECK(sv_ns_read(&ns, line, sizeof(line), 5000) > 0, "exited");
	SV_EQ_STR(line, "{\"type\":\"exited\",\"gen\":1,\"status\":127}");
	sv_ns_stop(&ns);
	return 0;
}

/*
 * The generation and the launch permit: a second launch without an
 * attested end is refused, a request naming another generation is
 * refused, a malformed request is answered, and an attested end permits
 * the next launch. The runtime process ending inside the end sends no
 * exited.
 */
SV_CASE(ns_generation_permit_and_malformed)
{
	NEED_NS();
	sv_bin_path(probe, sizeof(probe), "probe");
	const char *rt[] = {probe, "sleep", NULL};
	SV_EQ_INT(start(NULL, rt), 0);
	SV_CHECK(req("{\"type\":\"launch\",\"gen\":1,\"id\":1}") > 0, "launch");
	SV_EQ_STR(line, "{\"type\":\"launched\",\"gen\":1,\"id\":1}");
	SV_CHECK(req("{\"type\":\"launch\",\"gen\":2,\"id\":2}") > 0, "second launch");
	SV_EQ_STR(line, "{\"type\":\"launched\",\"gen\":2,\"id\":2,\"error\":\"launch_not_permitted\"}");
	SV_CHECK(req("{\"type\":\"launch\",\"gen\":5,\"id\":3}") > 0, "launch gen 5");
	SV_EQ_STR(line, "{\"type\":\"launched\",\"gen\":5,\"id\":3,\"error\":\"gen_mismatch\"}");
	SV_CHECK(req("{\"type\":\"ping\",\"gen\":0,\"id\":4}") > 0, "ping gen 0");
	SV_EQ_STR(line, "{\"type\":\"pong\",\"gen\":0,\"id\":4,\"error\":\"gen_mismatch\"}");
	SV_CHECK(req("{\"type\":\"end\",\"gen\":1,\"id\":5}") > 0, "malformed end");
	SV_EQ_STR(line, "{\"type\":\"ended\",\"gen\":1,\"id\":5,\"error\":\"malformed_request\"}");
	SV_CHECK(req("{\"type\":\"end\",\"gen\":1,\"id\":6,\"graceMs\":0}") > 0, "end");
	SV_EQ_STR(line, "{\"type\":\"ended\",\"gen\":1,\"id\":6}");
	SV_CHECK(sv_ns_read(&ns, line, sizeof(line), 300) < 0, "no exited for an exit inside the end: %s", line);
	SV_CHECK(req("{\"type\":\"launch\",\"gen\":2,\"id\":7}") > 0, "launch after end");
	SV_EQ_STR(line, "{\"type\":\"launched\",\"gen\":2,\"id\":7}");
	sv_ns_stop(&ns);
	return 0;
}

/*
 * The end reaches every other process in the container, a daemon the
 * runtime double-forked included, and after it only the supervisor is
 * left: a cleanup command then counts itself, its worker, and the
 * supervisor.
 */
SV_CASE(ns_end_reaches_every_process)
{
	NEED_NS();
	sv_bin_path(probe, sizeof(probe), "probe");
	char pidfile[4096], count[4096];
	snprintf(pidfile, sizeof(pidfile), "%s", prefixed("daemon.pid"));
	snprintf(count, sizeof(count), "%s", prefixed("count"));
	unlink(pidfile);
	const char *rt[] = {probe, "daemon", pidfile, NULL};
	SV_EQ_INT(start(NULL, rt), 0);
	SV_CHECK(req("{\"type\":\"launch\",\"gen\":1,\"id\":1}") > 0, "launch");
	SV_CHECK(sv_wait_file_contains(pidfile, "\n", 5000), "the daemon started");
	SV_CHECK(req("{\"type\":\"end\",\"gen\":1,\"id\":2,\"graceMs\":0}") > 0, "end");
	SV_EQ_STR(line, "{\"type\":\"ended\",\"gen\":1,\"id\":2}");
	char r[16384];
	snprintf(r, sizeof(r),
		 "{\"type\":\"cleanup\",\"gen\":1,\"id\":3,\"commands\":[[\"%s\",\"count-procs\",\"%s\"]],"
		 "\"timeoutMs\":10000,\"prevProvider\":\"\",\"prevLeaseId\":\"\"}",
		 probe, count);
	SV_CHECK(req(r) > 0, "cleanup");
	SV_EQ_STR(line, "{\"type\":\"cleaned\",\"gen\":1,\"id\":3}");
	char buf[32];
	SV_CHECK(sv_read_file(count, buf, sizeof(buf)) > 0, "count written");
	SV_EQ_INT(atoi(buf), 3);
	sv_ns_stop(&ns);
	return 0;
}

/* The end sends SIGTERM first and waits the request's grace before SIGKILL. */
SV_CASE(ns_end_sends_sigterm_with_grace)
{
	NEED_NS();
	sv_bin_path(probe, sizeof(probe), "probe");
	char rec[4096], ready[4200];
	snprintf(rec, sizeof(rec), "%s", prefixed("sigrecord"));
	snprintf(ready, sizeof(ready), "%s.ready", rec);
	unlink(rec);
	unlink(ready);
	const char *rt[] = {probe, "sigrecord", rec, NULL};
	SV_EQ_INT(start(NULL, rt), 0);
	SV_CHECK(req("{\"type\":\"launch\",\"gen\":1,\"id\":1}") > 0, "launch");
	SV_CHECK(sv_wait_file_contains(ready, "ready", 5000), "the runtime installed its handlers");
	unsigned long long t0 = sv_ms_now();
	SV_CHECK(req("{\"type\":\"end\",\"gen\":1,\"id\":2,\"graceMs\":300}") > 0, "end");
	SV_EQ_STR(line, "{\"type\":\"ended\",\"gen\":1,\"id\":2}");
	SV_CHECK(sv_ms_now() - t0 >= 290, "the grace was waited");
	SV_CHECK(sv_wait_file_contains(rec, "15\n", 1000), "SIGTERM reached the runtime");
	sv_ns_stop(&ns);
	return 0;
}

/*
 * The sweep removes every entry the agent UID owns in the writable
 * volumes, symbolic links included, keeps the workspace base's slots/,
 * then ends every process again before it answers.
 */
SV_CASE(ns_sweep_removes_volume_entries)
{
	NEED_NS();
	const char *rt[] = {"/nonexistent/runtime", NULL};
	SV_EQ_INT(start(NULL, rt), 0);
	static const char *const files[] = {"sessions/s", "artifacts/a", "tmp/t", "dev/shm/m",
					    "dev/mqueue/q", "workspace/w"};
	for (size_t i = 0; i < 6; i++)
		sv_write_file(prefixed(files[i]), "x");
	char link[4096];
	snprintf(link, sizeof(link), "%s", prefixed("tmp/run-lenny"));
	symlink("/run/lenny", link);
	sv_mkdir_p(prefixed("workspace/slots/slot-1"));
	sv_write_file(prefixed("workspace/slots/slot-1/keep"), "k");
	SV_CHECK(req("{\"type\":\"sweep\",\"gen\":0,\"id\":1}") > 0, "sweep");
	SV_EQ_STR(line, "{\"type\":\"swept\",\"gen\":0,\"id\":1}");
	for (size_t i = 0; i < 6; i++)
		SV_CHECK(!sv_path_exists(prefixed(files[i])), "%s removed", files[i]);
	SV_CHECK(!sv_path_exists(link), "the symbolic link was removed");
	SV_CHECK(sv_path_exists(prefixed("workspace/slots/slot-1/keep")), "slots/ kept");
	sv_ns_stop(&ns);
	return 0;
}

/* A sweep walk that reaches its depth bound answers with the bound error. */
SV_CASE(ns_sweep_bound_error)
{
	NEED_NS();
	const char *rt[] = {"/nonexistent/runtime", NULL};
	SV_EQ_INT(start(NULL, rt), 0);
	char p[16384];
	int n = snprintf(p, sizeof(p), "%s", prefixed("tmp"));
	for (int i = 0; i < SV_SWEEP_MAX_DEPTH + 2; i++)
		n += snprintf(p + n, sizeof(p) - (size_t)n, "/d");
	SV_EQ_INT(sv_mkdir_p(p), 0);
	SV_CHECK(req("{\"type\":\"sweep\",\"gen\":0,\"id\":1}") > 0, "sweep");
	SV_EQ_STR(line, "{\"type\":\"swept\",\"gen\":0,\"id\":1,\"error\":\"sweep_bound\"}");
	sv_ns_stop(&ns);
	sv_rm_rf(prefixed("tmp"));
	return 0;
}

/*
 * cleanup runs each command at the agent UID in the workspace base, with
 * the launch environment (the fixed PATH, which a later --env pair
 * replaces, then each pair) plus the step-0 cleanup metadata, an empty
 * signal mask, and no descriptor beyond the standard streams.
 */
SV_CASE(ns_cleanup_environment_and_working_directory)
{
	NEED_NS();
	sv_bin_path(probe, sizeof(probe), "probe");
	char report[4096];
	snprintf(report, sizeof(report), "%s", prefixed("cleanup-report"));
	unlink(report);
	const char *flags[] = {"--env", "PATH=/opt/venv/bin", "--env", "VIRTUAL_ENV=/opt/venv",
			       "--env", "LENNY_MANIFEST_PATH=/run/lenny/manifest.json", NULL};
	const char *rt[] = {"/nonexistent/runtime", NULL};
	SV_EQ_INT(start(flags, rt), 0);
	char r[16384];
	snprintf(r, sizeof(r),
		 "{\"type\":\"cleanup\",\"gen\":0,\"id\":1,\"commands\":[[\"%s\",\"report\",\"%s\",\"exit\"]],"
		 "\"timeoutMs\":10000,\"prevProvider\":\"anthropic\",\"prevLeaseId\":\"lease-1\"}",
		 probe, report);
	SV_CHECK(req(r) > 0, "cleanup");
	SV_EQ_STR(line, "{\"type\":\"cleaned\",\"gen\":0,\"id\":1}");
	char buf[16384];
	SV_CHECK(sv_read_file(report, buf, sizeof(buf)) > 0, "report");
	SV_CHECK(strstr(buf, "Env: PATH=/opt/venv/bin\n"), "PATH replaced:\n%s", buf);
	SV_CHECK(strstr(buf, "Env: VIRTUAL_ENV=/opt/venv\n"), "runtime env:\n%s", buf);
	SV_CHECK(strstr(buf, "Env: LENNY_MANIFEST_PATH=/run/lenny/manifest.json\n"), "platform env");
	SV_CHECK(strstr(buf, "Env: LENNY_PREV_CREDENTIAL_PROVIDER=anthropic\n"), "metadata provider");
	SV_CHECK(strstr(buf, "Env: LENNY_PREV_LEASE_ID=lease-1\n"), "metadata lease");
	char cwd[4200];
	snprintf(cwd, sizeof(cwd), "Cwd: %s\n", prefixed("workspace"));
	SV_CHECK(strstr(buf, cwd), "cwd:\n%s", buf);
	SV_CHECK(strstr(buf, "SigBlk:\t0000000000000000"), "mask:\n%s", buf);
	for (char *q = strstr(buf, "Fd: "); q; q = strstr(q + 1, "Fd: "))
		SV_CHECK(atoi(q + 4) <= 2, "descriptor leaked:\n%s", buf);
	int envs = 0;
	for (char *q = strstr(buf, "Env: "); q; q = strstr(q + 1, "Env: "))
		envs++;
	SV_EQ_INT(envs, 5);
	sv_ns_stop(&ns);
	return 0;
}

/* A failing command answers cleanup_failed; a command past timeoutMs answers cleanup_timeout. */
SV_CASE(ns_cleanup_failure_and_deadline)
{
	NEED_NS();
	sv_bin_path(probe, sizeof(probe), "probe");
	const char *rt[] = {"/nonexistent/runtime", NULL};
	SV_EQ_INT(start(NULL, rt), 0);
	char r[16384];
	snprintf(r, sizeof(r),
		 "{\"type\":\"cleanup\",\"gen\":0,\"id\":1,\"commands\":[[\"%s\",\"exit\",\"2\"]],"
		 "\"timeoutMs\":10000,\"prevProvider\":\"\",\"prevLeaseId\":\"\"}",
		 probe);
	SV_CHECK(req(r) > 0, "cleanup");
	SV_EQ_STR(line, "{\"type\":\"cleaned\",\"gen\":0,\"id\":1,\"error\":\"cleanup_failed\"}");
	snprintf(r, sizeof(r),
		 "{\"type\":\"cleanup\",\"gen\":0,\"id\":2,\"commands\":[[\"%s\",\"sleep\"]],"
		 "\"timeoutMs\":200,\"prevProvider\":\"\",\"prevLeaseId\":\"\"}",
		 probe);
	SV_CHECK(req(r) > 0, "cleanup");
	SV_EQ_STR(line, "{\"type\":\"cleaned\",\"gen\":0,\"id\":2,\"error\":\"cleanup_timeout\"}");
	sv_ns_stop(&ns);
	return 0;
}

/*
 * The launched runtime process starts with an empty signal mask, in its
 * own session, with no descriptor beyond the standard streams (the
 * CH-SUPERVISE socket included), and with exactly the launch environment.
 */
SV_CASE(ns_launched_process_mask_descriptors_and_environment)
{
	NEED_NS();
	sv_bin_path(probe, sizeof(probe), "probe");
	char report[4096];
	snprintf(report, sizeof(report), "%s", prefixed("launch-report"));
	unlink(report);
	const char *flags[] = {"--env", "VIRTUAL_ENV=/opt/venv", "--env", "LENNY_SOCKET=@lenny-msg", NULL};
	const char *rt[] = {probe, "report", report, "sleep", NULL};
	SV_EQ_INT(start(flags, rt), 0);
	SV_CHECK(req("{\"type\":\"launch\",\"gen\":1,\"id\":1}") > 0, "launch");
	SV_EQ_STR(line, "{\"type\":\"launched\",\"gen\":1,\"id\":1}");
	SV_CHECK(sv_wait_file_contains(report, "Env:", 5000), "report");
	char buf[16384];
	sv_sleep(50);
	sv_read_file(report, buf, sizeof(buf));
	SV_CHECK(strstr(buf, "SigBlk:\t0000000000000000"), "mask:\n%s", buf);
	SV_CHECK(strstr(buf, "SigCgt:\t0000000000000000"), "handlers reset:\n%s", buf);
	int pid = atoi(strstr(buf, "Pid: ") + 5), sid = atoi(strstr(buf, "Sid: ") + 5);
	SV_CHECK(pid == sid && pid != 1, "own session: pid %d sid %d", pid, sid);
	for (char *q = strstr(buf, "Fd: "); q; q = strstr(q + 1, "Fd: "))
		SV_CHECK(atoi(q + 4) <= 2, "descriptor leaked:\n%s", buf);
	const char *want = "Env: " SV_FIXED_PATH "\nEnv: VIRTUAL_ENV=/opt/venv\nEnv: LENNY_SOCKET=@lenny-msg\n";
	SV_CHECK(strstr(buf, want), "environment:\n%s", buf);
	int envs = 0;
	for (char *q = strstr(buf, "Env: "); q; q = strstr(q + 1, "Env: "))
		envs++;
	SV_EQ_INT(envs, 3);
	sv_ns_stop(&ns);
	return 0;
}

/*
 * The kubelet's stop signal and every other catchable signal sent from
 * outside the namespace, SIGCHLD and the fault signals included: the
 * supervisor takes no action, keeps answering, and sends the runtime
 * process nothing.
 */
SV_CASE(ns_signals_from_outside_take_no_action)
{
	NEED_NS();
	sv_bin_path(probe, sizeof(probe), "probe");
	char rec[4096], ready[4200];
	snprintf(rec, sizeof(rec), "%s", prefixed("outside-rec"));
	snprintf(ready, sizeof(ready), "%s.ready", rec);
	unlink(rec);
	unlink(ready);
	const char *rt[] = {probe, "sigrecord", rec, NULL};
	SV_EQ_INT(start(NULL, rt), 0);
	SV_CHECK(req("{\"type\":\"launch\",\"gen\":1,\"id\":1}") > 0, "launch");
	SV_CHECK(sv_wait_file_contains(ready, "ready", 5000), "the runtime installed its handlers");
	for (int sig = 1; sig < 65; sig++) {
		if (sig == SIGKILL || sig == SIGSTOP)
			continue;
		SV_EQ_INT(kill(ns.sup_pid, sig), 0);
	}
	char r[128];
	snprintf(r, sizeof(r), "{\"type\":\"ping\",\"gen\":1,\"id\":2}");
	SV_CHECK(req(r) > 0, "the supervisor still answers");
	SV_EQ_STR(line, "{\"type\":\"pong\",\"gen\":1,\"id\":2}");
	sv_sleep(100);
	char buf[256];
	SV_EQ_INT(sv_read_file(rec, buf, sizeof(buf)), 0);
	sv_ns_stop(&ns);
	return 0;
}

/*
 * Every signal the runtime process sends PID 1, SIGKILL and SIGSTOP
 * (which the kernel discards for a namespace init) and the fault signals
 * through kill and rt_sigqueueinfo included, leaves the supervisor
 * answering.
 */
SV_CASE(ns_signals_from_inside_take_no_action)
{
	NEED_NS();
	sv_bin_path(probe, sizeof(probe), "probe");
	char done[4096];
	snprintf(done, sizeof(done), "%s", prefixed("inside-done"));
	unlink(done);
	const char *rt[] = {probe, "signal-init", done, NULL};
	SV_EQ_INT(start(NULL, rt), 0);
	SV_CHECK(req("{\"type\":\"launch\",\"gen\":1,\"id\":1}") > 0, "launch");
	SV_CHECK(sv_wait_file_contains(done, "done", 5000), "the runtime sent every signal");
	SV_CHECK(req("{\"type\":\"ping\",\"gen\":1,\"id\":2}") > 0, "the supervisor still answers");
	SV_EQ_STR(line, "{\"type\":\"pong\",\"gen\":1,\"id\":2}");
	sv_ns_stop(&ns);
	return 0;
}

/* SIGKILL from outside the namespace ends the supervisor and every process; the connection closes. */
SV_CASE(ns_sigkill_from_outside_ends_container)
{
	NEED_NS();
	sv_bin_path(probe, sizeof(probe), "probe");
	const char *rt[] = {probe, "sleep", NULL};
	SV_EQ_INT(start(NULL, rt), 0);
	SV_CHECK(req("{\"type\":\"launch\",\"gen\":1,\"id\":1}") > 0, "launch");
	SV_EQ_INT(kill(ns.sup_pid, SIGKILL), 0);
	SV_EQ_INT(sv_ns_read(&ns, line, sizeof(line), 5000), 0);
	SV_EQ_INT(sv_ns_wait(&ns, 5000), 128 + SIGKILL);
	sv_ns_stop(&ns);
	return 0;
}

/* SIGSTOP from outside stops the supervisor, so a ping goes unanswered until it continues. */
SV_CASE(ns_sigstop_from_outside_misses_pong)
{
	NEED_NS();
	const char *rt[] = {"/nonexistent/runtime", NULL};
	SV_EQ_INT(start(NULL, rt), 0);
	SV_EQ_INT(kill(ns.sup_pid, SIGSTOP), 0);
	sv_sleep(50);
	SV_EQ_INT(sv_ns_send(&ns, "{\"type\":\"ping\",\"gen\":0,\"id\":1}"), 0);
	SV_CHECK(sv_ns_read(&ns, line, sizeof(line), 300) < 0, "a stopped supervisor answered");
	SV_EQ_INT(kill(ns.sup_pid, SIGCONT), 0);
	SV_CHECK(sv_ns_read(&ns, line, sizeof(line), 5000) > 0, "pong after SIGCONT");
	SV_EQ_STR(line, "{\"type\":\"pong\",\"gen\":0,\"id\":1}");
	sv_ns_stop(&ns);
	return 0;
}

/* A closed connection makes the supervisor exit, which ends the runtime process with it. */
SV_CASE(ns_closed_connection_exits)
{
	NEED_NS();
	sv_bin_path(probe, sizeof(probe), "probe");
	const char *rt[] = {probe, "sleep", NULL};
	SV_EQ_INT(start(NULL, rt), 0);
	SV_CHECK(req("{\"type\":\"launch\",\"gen\":1,\"id\":1}") > 0, "launch");
	close(ns.conn);
	ns.conn = -1;
	SV_EQ_INT(sv_ns_wait(&ns, 5000), SV_EXIT_CLOSED);
	sv_ns_stop(&ns);
	return 0;
}

/* An unreadable request line, and a line above the line bound, make the supervisor exit. */
SV_CASE(ns_unreadable_and_overlong_lines_exit)
{
	NEED_NS();
	const char *rt[] = {"/nonexistent/runtime", NULL};
	SV_EQ_INT(start(NULL, rt), 0);
	SV_EQ_INT(sv_ns_send(&ns, "not json"), 0);
	SV_EQ_INT(sv_ns_wait(&ns, 5000), SV_EXIT_PROTOCOL);
	sv_ns_stop(&ns);

	SV_EQ_INT(start(NULL, rt), 0);
	static char big[SV_MAX_LINE + 64];
	memset(big, 'x', sizeof(big));
	size_t off = 0;
	while (off < sizeof(big)) {
		ssize_t n = write(ns.conn, big + off, sizeof(big) - off);
		if (n <= 0)
			break;
		off += (size_t)n;
	}
	SV_EQ_INT(sv_ns_wait(&ns, 5000), SV_EXIT_PROTOCOL);
	sv_ns_stop(&ns);
	return 0;
}

static int bind_name(const char *name)
{
	int fd = socket(AF_UNIX, SOCK_STREAM | SOCK_CLOEXEC, 0);
	struct sockaddr_un a;
	memset(&a, 0, sizeof(a));
	a.sun_family = AF_UNIX;
	size_t n = strlen(name);
	memcpy(a.sun_path + 1, name, n);
	if (bind(fd, (struct sockaddr *)&a, (socklen_t)(sizeof(a.sun_family) + 1 + n)) < 0 || listen(fd, 4) < 0) {
		close(fd);
		return -1;
	}
	return fd;
}

/* A supervisor started before the adapter binds keeps dialling and connects once it binds. */
SV_CASE(ns_dial_retries_until_adapter_binds)
{
	NEED_NS();
	const char *rt[] = {"/nonexistent/runtime", NULL};
	reset_volumes();
	SV_EQ_INT(sv_ns_listen(&ns), 0);
	close(ns.listen_fd);
	SV_EQ_INT(sv_ns_start(&ns, sv_ns_run_argv(&ns, NULL, rt)), 0);
	sv_sleep(500);
	ns.listen_fd = bind_name(ns.sock);
	SV_CHECK(ns.listen_fd >= 0, "bind");
	SV_EQ_INT(sv_ns_accept(&ns, 10000), 0);
	SV_CHECK(sv_ns_read(&ns, line, sizeof(line), 5000) > 0, "ready");
	SV_EQ_STR(line, READY_ALL_PASS);
	sv_ns_stop(&ns);
	return 0;
}

/* With no adapter listening, the dial window ends and the supervisor exits non-zero, having sent nothing. */
SV_CASE(ns_dial_timeout_exits)
{
	NEED_NS();
	const char *flags[] = {"--dial-timeout", "300ms", NULL};
	const char *rt[] = {"/nonexistent/runtime", NULL};
	reset_volumes();
	SV_EQ_INT(sv_ns_listen(&ns), 0);
	close(ns.listen_fd);
	ns.listen_fd = -1;
	unsigned long long t0 = sv_ms_now();
	SV_EQ_INT(sv_ns_start(&ns, sv_ns_run_argv(&ns, flags, rt)), 0);
	SV_EQ_INT(sv_ns_wait(&ns, 10000), SV_EXIT_DIAL);
	SV_CHECK(sv_ms_now() - t0 < 8000, "bounded by the window");
	return 0;
}

/*
 * --adapter-uid naming a UID other than the peer's: the supervisor exits
 * non-zero after one dial without sending ready. Naming the peer's UID,
 * it sends ready.
 */
SV_CASE(ns_adapter_uid_checked_with_so_peercred)
{
	NEED_NS();
	const char *rt[] = {"/nonexistent/runtime", NULL};
	char other[32], self[32];
	snprintf(other, sizeof(other), "%u", (unsigned)getuid() + 1);
	snprintf(self, sizeof(self), "%u", (unsigned)getuid());
	const char *bad[] = {"--adapter-uid", other, NULL};
	reset_volumes();
	SV_EQ_INT(sv_ns_listen(&ns), 0);
	SV_EQ_INT(sv_ns_start(&ns, sv_ns_run_argv(&ns, bad, rt)), 0);
	SV_EQ_INT(sv_ns_accept(&ns, 10000), 0);
	SV_EQ_INT(sv_ns_read(&ns, line, sizeof(line), 5000), 0);
	SV_EQ_INT(sv_ns_wait(&ns, 5000), SV_EXIT_PEER);
	sv_ns_stop(&ns);

	const char *good[] = {"--adapter-uid", self, NULL};
	SV_EQ_INT(start(good, rt), 0);
	SV_EQ_STR(line, READY_ALL_PASS);
	sv_ns_stop(&ns);
	return 0;
}

/* selftest mode as a namespace init: every property passes and the mode exits 0. */
SV_CASE(ns_selftest_mode_passes_as_namespace_init)
{
	NEED_NS();
	char sup[4096];
	sv_bin_path(sup, sizeof(sup), "lenny-supervisor-prefixed");
	int fds[2];
	SV_EQ_INT(pipe(fds), 0);
	int saved = dup(1);
	dup2(fds[1], 1);
	close(fds[1]);
	SV_EQ_INT(sv_ns_listen(&ns), 0);
	char *argv[] = {sup, "selftest", NULL};
	int r = sv_ns_start(&ns, argv);
	dup2(saved, 1);
	close(saved);
	SV_EQ_INT(r, 0);
	int st = sv_ns_wait(&ns, 10000);
	char buf[512];
	ssize_t n = read(fds[0], buf, sizeof(buf) - 1);
	buf[n < 0 ? 0 : n] = 0;
	SV_EQ_INT(st, 0);
	SV_EQ_STR(buf, "nondumpable pass\nkillReach pass\ninitSigkillImmune pass\ninitSigstopImmune pass\n");
	sv_ns_stop(&ns);
	return 0;
}
