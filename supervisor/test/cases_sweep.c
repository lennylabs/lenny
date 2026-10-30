/* SPDX-License-Identifier: MIT */
/*
 * Cases for the sweep worker: the volume walk, the System V selection by
 * uid and cuid, the IPC removal, the POSIX message queue walk, and the
 * worker's exit statuses.
 *
 * spec: §4.7.10 (Runtime supervisor, Sweep). Cases that remove System V
 * objects by enumeration run in a fresh user and IPC namespace, so they
 * cannot reach an object outside the case.
 */
#define _GNU_SOURCE
#include "harness.h"

#include <errno.h>
#include <fcntl.h>
#include <sched.h>
#include <sys/ipc.h>
#include <sys/msg.h>
#include <sys/sem.h>
#include <sys/shm.h>
#include <sys/stat.h>

static char root[4096];

static const char *fresh(const char *name)
{
	snprintf(root, sizeof(root), "%s/sweep-%s-%d", SV_PATH_PREFIX, name, getpid());
	sv_rm_rf(root);
	sv_mkdir_p(root);
	return root;
}

static void mk(const char *base, const char *rel, int dir)
{
	char p[8192];
	snprintf(p, sizeof(p), "%s/%s", base, rel);
	if (dir)
		sv_mkdir_p(p);
	else
		sv_write_file(p, "x");
}

static int exists(const char *base, const char *rel)
{
	char p[8192];
	snprintf(p, sizeof(p), "%s/%s", base, rel);
	return sv_path_exists(p);
}

/*
 * Every entry the UID owns is removed, symbolic links included and not
 * followed, and the skipped top-level child (the workspace base's slots/)
 * is kept with its contents.
 */
SV_CASE(sweep_walk_removes_owned_entries_and_skips_slots)
{
	const char *r = fresh("walk");
	char outside[4200];
	snprintf(outside, sizeof(outside), "%s-outside", r);
	sv_rm_rf(outside);
	sv_mkdir_p(outside);
	char target[4300];
	snprintf(target, sizeof(target), "%s/keep", outside);
	sv_write_file(target, "keep");
	mk(r, "a/b/c", 1);
	mk(r, "a/b/c/file", 0);
	mk(r, "top", 0);
	mk(r, ".hidden", 0);
	mk(r, "slots/s1/work", 1);
	mk(r, "slots/s1/work/f", 0);
	char link[4300];
	snprintf(link, sizeof(link), "%s/a/escape", r);
	SV_EQ_INT(symlink(outside, link), 0);
	snprintf(link, sizeof(link), "%s/dangling", r);
	SV_EQ_INT(symlink("/nonexistent", link), 0);

	struct sv_sweep_stats st;
	memset(&st, 0, sizeof(st));
	sv_sweep_tree(r, getuid(), "slots", SV_SWEEP_MAX_DEPTH, SV_SWEEP_MAX_ENTRIES, &st);
	SV_CHECK(!st.failed && !st.bound_reached, "failed %d bound %d", st.failed, st.bound_reached);
	SV_CHECK(!exists(r, "a") && !exists(r, "top") && !exists(r, ".hidden") && !exists(r, "dangling"),
		 "owned entries removed");
	SV_CHECK(exists(r, "slots/s1/work/f"), "slots/ is left to scrub step 2");
	SV_CHECK(sv_path_exists(target), "the symbolic link was not followed");
	SV_EQ_INT(sv_sweep_status(&st), SV_WORKER_OK);
	sv_rm_rf(r);
	sv_rm_rf(outside);
	return 0;
}

/* Entries another UID owns are kept. */
SV_CASE(sweep_walk_keeps_entries_of_other_uids)
{
	const char *r = fresh("other");
	mk(r, "d/f", 0);
	mk(r, "d", 1);
	mk(r, "d/f", 0);
	struct sv_sweep_stats st;
	memset(&st, 0, sizeof(st));
	sv_sweep_tree(r, getuid() + 4242, NULL, SV_SWEEP_MAX_DEPTH, SV_SWEEP_MAX_ENTRIES, &st);
	SV_CHECK(exists(r, "d/f"), "nothing removed");
	SV_EQ_INT(sv_sweep_status(&st), SV_WORKER_OK);
	sv_rm_rf(r);
	return 0;
}

/* A walk that reaches its depth bound stops and reports the bound. */
SV_CASE(sweep_walk_depth_bound)
{
	const char *r = fresh("depth");
	char rel[4096] = "";
	for (int i = 0; i < 6; i++)
		strcat(rel, i ? "/d" : "d");
	mk(r, rel, 1);
	struct sv_sweep_stats st;
	memset(&st, 0, sizeof(st));
	sv_sweep_tree(r, getuid(), NULL, 3, SV_SWEEP_MAX_ENTRIES, &st);
	SV_CHECK(st.bound_reached, "depth bound reported");
	SV_EQ_INT(sv_sweep_status(&st), SV_WORKER_BOUND);
	SV_CHECK(exists(r, "d"), "the walk stopped without removing the deep tree's root");
	/* Within the bound the same tree is removed. */
	memset(&st, 0, sizeof(st));
	sv_sweep_tree(r, getuid(), NULL, 8, SV_SWEEP_MAX_ENTRIES, &st);
	SV_EQ_INT(sv_sweep_status(&st), SV_WORKER_OK);
	SV_CHECK(!exists(r, "d"), "removed");
	sv_rm_rf(r);
	return 0;
}

/* A walk that reaches its entry bound stops and reports the bound. */
SV_CASE(sweep_walk_entry_bound)
{
	const char *r = fresh("entries");
	for (int i = 0; i < 20; i++) {
		char n[32];
		snprintf(n, sizeof(n), "f%d", i);
		mk(r, n, 0);
	}
	struct sv_sweep_stats st;
	memset(&st, 0, sizeof(st));
	sv_sweep_tree(r, getuid(), NULL, 4, 10, &st);
	SV_CHECK(st.bound_reached, "entry bound reported");
	SV_EQ_INT(sv_sweep_status(&st), SV_WORKER_BOUND);
	SV_CHECK(st.entries <= 11, "the walk stopped at the bound (%llu)", (unsigned long long)st.entries);
	sv_rm_rf(r);
	return 0;
}

/* An absent volume is skipped; a root that cannot be opened is a failure. */
SV_CASE(sweep_walk_absent_and_unopenable_roots)
{
	const char *r = fresh("roots");
	char p[4200];
	snprintf(p, sizeof(p), "%s/absent", r);
	struct sv_sweep_stats st;
	memset(&st, 0, sizeof(st));
	sv_sweep_tree(p, getuid(), NULL, 4, 10, &st);
	SV_EQ_INT(sv_sweep_status(&st), SV_WORKER_OK);
	snprintf(p, sizeof(p), "%s/link", r);
	symlink(r, p);
	sv_sweep_tree(p, getuid(), NULL, 4, 10, &st);
	SV_EQ_INT(sv_sweep_status(&st), SV_WORKER_FAILED);
	sv_rm_rf(r);
	return 0;
}

/* The POSIX message queue mount is flat: its owned entries go, and nothing deeper is walked. */
SV_CASE(sweep_mqueue_mount_entries)
{
	const char *r = fresh("mqueue");
	mk(r, "q1", 0);
	mk(r, "q2", 0);
	struct sv_sweep_stats st;
	memset(&st, 0, sizeof(st));
	sv_sweep_mqueue(r, getuid(), &st);
	SV_CHECK(!exists(r, "q1") && !exists(r, "q2"), "queues removed");
	SV_EQ_INT(sv_sweep_status(&st), SV_WORKER_OK);
	sv_rm_rf(r);
	return 0;
}

/*
 * /proc/sysvipc line selection: the uid column or the cuid column
 * matches (an owner can move uid with IPC_SET), each kind's columns, and
 * an unparsable or header line.
 */
SV_CASE(sweep_ipc_line_select_uid_and_cuid)
{
	/* shm: key shmid perms size cpid lpid nattch uid gid cuid cgid ... */
	const char *shm_uid = "  0  32769  600  4096  10  11  0  1001  1001  1001  1001  0 0 0 0 0";
	const char *shm_cuid = "  0  32770  600  4096  10  11  0  2000  2000  1001  1001  0 0 0 0 0";
	const char *shm_none = "  0  32771  600  4096  10  11  0  2000  2000  2000  2000  0 0 0 0 0";
	/* msg: key msqid perms cbytes qnum lspid lrpid uid gid cuid cgid ... */
	const char *msg_cuid = "  0  5  600  0  0  0  0  2000  2000  1001  1001  0 0 0";
	/* sem: key semid perms nsems uid gid cuid cgid ... */
	const char *sem_uid = "  0  7  600  1  1001  1001  2000  2000  0 0";
	const char *sem_cuid = "  0  8  600  1  2000  2000  1001  1001  0 0";
	const char *header = "       key      shmid perms                  size  cpid  lpid nattch   uid   gid  cuid  cgid";
	int id = -1;
	SV_EQ_INT(sv_ipc_line_select(shm_uid, SV_IPC_SHM, 1001, &id), 1);
	SV_EQ_INT(id, 32769);
	SV_EQ_INT(sv_ipc_line_select(shm_cuid, SV_IPC_SHM, 1001, &id), 1);
	SV_EQ_INT(id, 32770);
	SV_EQ_INT(sv_ipc_line_select(shm_none, SV_IPC_SHM, 1001, &id), 0);
	SV_EQ_INT(sv_ipc_line_select(msg_cuid, SV_IPC_MSG, 1001, &id), 1);
	SV_EQ_INT(id, 5);
	SV_EQ_INT(sv_ipc_line_select(sem_uid, SV_IPC_SEM, 1001, &id), 1);
	SV_EQ_INT(sv_ipc_line_select(sem_cuid, SV_IPC_SEM, 1001, &id), 1);
	SV_EQ_INT(id, 8);
	SV_EQ_INT(sv_ipc_line_select(header, SV_IPC_SHM, 1001, &id), -1);
	SV_EQ_INT(sv_ipc_line_select("", SV_IPC_SHM, 1001, &id), -1);
	SV_EQ_INT(sv_ipc_line_select(shm_uid, 3, 1001, &id), -1);
	return 0;
}

static int enter_ipc_namespace(void)
{
	uid_t uid = getuid();
	gid_t gid = getgid();
	if (unshare(CLONE_NEWUSER | CLONE_NEWIPC) < 0)
		return -1;
	char map[64];
	snprintf(map, sizeof(map), "%u %u 1", (unsigned)uid, (unsigned)uid);
	int fd = open("/proc/self/uid_map", O_WRONLY);
	if (fd < 0 || write(fd, map, strlen(map)) < 0)
		return -1;
	close(fd);
	fd = open("/proc/self/setgroups", O_WRONLY);
	if (fd >= 0) {
		if (write(fd, "deny", 4) < 0) {
		}
		close(fd);
	}
	snprintf(map, sizeof(map), "%u %u 1", (unsigned)gid, (unsigned)gid);
	fd = open("/proc/self/gid_map", O_WRONLY);
	if (fd < 0 || write(fd, map, strlen(map)) < 0)
		return -1;
	close(fd);
	return 0;
}

/*
 * The listing path removes the objects a /proc/sysvipc listing selects,
 * and the *_STAT enumeration removes them when no listing is readable.
 * Both run in a fresh IPC namespace holding only the case's objects.
 */
SV_CASE(sweep_ipc_listing_and_stat_fallback)
{
	if (!sv_ns_userns_available() || enter_ipc_namespace() < 0)
		SV_SKIP("blocked: unprivileged user and IPC namespaces are unavailable on this host");
	for (int pass = 0; pass < 2; pass++) {
		int shm = shmget(IPC_PRIVATE, 4096, IPC_CREAT | 0600);
		int msq = msgget(IPC_PRIVATE, IPC_CREAT | 0600);
		int sem = semget(IPC_PRIVATE, 1, IPC_CREAT | 0600);
		SV_CHECK(shm >= 0 && msq >= 0 && sem >= 0, "create");
		struct sv_sweep_stats st;
		memset(&st, 0, sizeof(st));
		if (pass == 0) {
			SV_EQ_INT(sv_sweep_ipc_listing("/proc/sysvipc/shm", SV_IPC_SHM, getuid(), &st), 0);
			SV_EQ_INT(sv_sweep_ipc_listing("/proc/sysvipc/msg", SV_IPC_MSG, getuid(), &st), 0);
			SV_EQ_INT(sv_sweep_ipc_listing("/proc/sysvipc/sem", SV_IPC_SEM, getuid(), &st), 0);
		} else {
			SV_EQ_INT(sv_sweep_ipc_listing("/nonexistent/listing", SV_IPC_SHM, getuid(), &st), -1);
			sv_sweep_ipc_stat(SV_IPC_SHM, getuid(), &st);
			sv_sweep_ipc_stat(SV_IPC_MSG, getuid(), &st);
			sv_sweep_ipc_stat(SV_IPC_SEM, getuid(), &st);
		}
		SV_CHECK(!st.failed, "pass %d: no failure", pass);
		struct shmid_ds sds;
		struct msqid_ds mds;
		SV_CHECK(shmctl(shm, IPC_STAT, &sds) < 0, "pass %d: shm removed", pass);
		SV_CHECK(msgctl(msq, IPC_STAT, &mds) < 0, "pass %d: msg removed", pass);
		SV_CHECK(semctl(sem, 0, IPC_STAT, NULL) < 0, "pass %d: sem removed", pass);
	}
	return 0;
}

/* The worker's exit status: the bound prevails, then a failure, then success. */
SV_CASE(sweep_status_mapping)
{
	struct sv_sweep_stats st = {0, 0, 0};
	SV_EQ_INT(sv_sweep_status(&st), SV_WORKER_OK);
	st.failed = 1;
	SV_EQ_INT(sv_sweep_status(&st), SV_WORKER_FAILED);
	st.bound_reached = 1;
	SV_EQ_INT(sv_sweep_status(&st), SV_WORKER_BOUND);
	return 0;
}

/* The sweep's volume list is the runtime container's writable mounts, and slots/ is skipped. */
SV_CASE(sweep_volume_list)
{
	static const char *const want[] = {"/sessions", "/artifacts", "/tmp", "/dev/shm", "/workspace"};
	int n = 0;
	for (; sv_sweep_volumes[n]; n++) {
		char full[4096];
		snprintf(full, sizeof(full), "%s%s", SV_PATH_PREFIX, want[n]);
		SV_EQ_STR(sv_sweep_volumes[n], full);
	}
	SV_EQ_INT(n, 5);
	SV_EQ_STR(sv_sweep_skip_top, "slots");
	return 0;
}
