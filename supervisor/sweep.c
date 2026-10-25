/* SPDX-License-Identifier: MIT */
/*
 * The sweep worker of the runtime supervisor.
 *
 * spec: §4.7.10 (Runtime supervisor, Sweep). The worker, which holds no
 * CH-SUPERVISE descriptor, removes the agent UID's System V shared memory
 * segments, message queues, and semaphore sets, matched on the creating
 * or the owning UID, the agent UID's POSIX message queues, and every
 * entry the agent UID owns, symbolic links included, in the runtime
 * container's writable volumes, leaving the workspace base's slots/
 * directory to scrub step 2. It follows no symbolic link and bounds its
 * walk; a walk that reaches a bound stops and the worker reports it.
 */
#include "supervisor.h"

/* The runtime container's writable volumes (pkg/controller/sandbox/podspec). */
const char *const sv_sweep_volumes[] = {
	SV_PATH_PREFIX "/sessions",
	SV_PATH_PREFIX "/artifacts",
	SV_PATH_PREFIX "/tmp",
	SV_PATH_PREFIX "/dev/shm",
	SV_PATH_PREFIX "/workspace",
	SV_NULL,
};
const char *const sv_sweep_skip_top = "slots";
const char *const sv_mqueue_mount = SV_PATH_PREFIX "/dev/mqueue";

#define SV_DIR_FLAGS (SV_O_RDONLY | SV_O_DIRECTORY | SV_O_NOFOLLOW | SV_O_CLOEXEC)

static int sv_is_dot(const char *n)
{
	return n[0] == '.' && (n[1] == 0 || (n[1] == '.' && n[2] == 0));
}

struct sv_walk {
	sv_u32 uid;
	int max_depth;
	sv_u64 max_entries;
	struct sv_sweep_stats *st;
};

/* sv_walk_stop reports whether the walk has to stop: a bound was reached. */
static int sv_walk_stop(const struct sv_walk *w)
{
	return w->st->bound_reached;
}

static void sv_walk_dir(struct sv_walk *w, int dirfd, int depth, const char *skip);

/*
 * sv_walk_entry handles one entry: a directory is walked first and then
 * removed when uid owns it; any other entry, a symbolic link included, is
 * unlinked without following when uid owns it.
 */
static void sv_walk_entry(struct sv_walk *w, int dirfd, const char *name, int depth)
{
	if (++w->st->entries > w->max_entries) {
		w->st->bound_reached = 1;
		return;
	}
	struct sv_statx sx;
	int r = sv_statx(dirfd, name, SV_AT_SYMLINK_NOFOLLOW, SV_STATX_TYPE | SV_STATX_UID, &sx);
	if (r == -SV_ENOENT)
		return;
	if (r < 0) {
		w->st->failed = 1;
		return;
	}
	int owned = sx.stx_uid == w->uid;
	if ((sx.stx_mode & SV_S_IFMT) == SV_S_IFDIR) {
		if (depth + 1 > w->max_depth) {
			w->st->bound_reached = 1;
			return;
		}
		int fd = sv_openat(dirfd, name, SV_DIR_FLAGS, 0);
		if (fd == -SV_ENOENT)
			return;
		if (fd < 0) {
			/* A directory uid does not own may deny it; only an owned one is a failure. */
			if (owned)
				w->st->failed = 1;
			return;
		}
		sv_walk_dir(w, fd, depth + 1, SV_NULL);
		sv_close(fd);
		if (sv_walk_stop(w))
			return;
		if (owned) {
			r = sv_unlinkat(dirfd, name, SV_AT_REMOVEDIR);
			if (r < 0 && r != -SV_ENOENT && r != -SV_ENOTEMPTY && r != -SV_EEXIST)
				w->st->failed = 1;
		}
		return;
	}
	if (!owned)
		return;
	r = sv_unlinkat(dirfd, name, 0);
	if (r < 0 && r != -SV_ENOENT)
		w->st->failed = 1;
}

static void sv_walk_dir(struct sv_walk *w, int dirfd, int depth, const char *skip)
{
	char buf[4096] __attribute__((aligned(8)));
	for (;;) {
		long n = sv_getdents64(dirfd, buf, sizeof(buf));
		if (n < 0) {
			w->st->failed = 1;
			return;
		}
		if (n == 0)
			return;
		for (long off = 0; off < n; off += ((struct sv_dirent64 *)(buf + off))->d_reclen) {
			const char *name = ((struct sv_dirent64 *)(buf + off))->d_name;
			if (sv_is_dot(name) || (skip && sv_streq(name, skip)))
				continue;
			sv_walk_entry(w, dirfd, name, depth);
			if (sv_walk_stop(w))
				return;
		}
	}
}

void sv_sweep_tree(const char *root, sv_u32 uid, const char *skip_top, int max_depth,
		   sv_u64 max_entries, struct sv_sweep_stats *st)
{
	int fd = sv_openat(SV_AT_FDCWD, root, SV_DIR_FLAGS, 0);
	if (fd == -SV_ENOENT)
		return; /* the volume is not mounted in this container */
	if (fd < 0) {
		st->failed = 1;
		return;
	}
	struct sv_walk w = {uid, max_depth, max_entries, st};
	sv_walk_dir(&w, fd, 0, skip_top);
	sv_close(fd);
}

/* ---- System V IPC ------------------------------------------------------ */

/* The columns of each /proc/sysvipc file: the id, uid, and cuid. */
static const int sv_ipc_uid_col[3] = {7, 7, 4};
static const int sv_ipc_cuid_col[3] = {9, 9, 6};

/* sv_ipc_field returns the decimal value of the column, or -1. */
static int sv_ipc_field(const char *line, int col, sv_u64 *out)
{
	int c = 0;
	const char *s = line;
	for (;;) {
		while (*s == ' ' || *s == '\t')
			s++;
		if (!*s || *s == '\n')
			return -1;
		const char *start = s;
		while (*s && *s != ' ' && *s != '\t' && *s != '\n')
			s++;
		if (c == col) {
			char tmp[24];
			sv_size n = (sv_size)(s - start);
			if (n == 0 || n >= sizeof(tmp))
				return -1;
			sv_bytecopy(tmp, start, n);
			tmp[n] = 0;
			return sv_parse_u64(tmp, out);
		}
		c++;
	}
}

int sv_ipc_line_select(const char *line, int kind, sv_u32 uid, int *id)
{
	if (kind < 0 || kind > 2)
		return -1;
	sv_u64 idv, u, cu;
	if (sv_ipc_field(line, 1, &idv) || sv_ipc_field(line, sv_ipc_uid_col[kind], &u) ||
	    sv_ipc_field(line, sv_ipc_cuid_col[kind], &cu) || idv > 0x7fffffff)
		return -1;
	*id = (int)idv;
	/* An owner can move uid with IPC_SET, so the creator's cuid matches too. */
	return u == uid || cu == uid;
}

static int sv_ipc_remove(int kind, int id)
{
	switch (kind) {
	case SV_IPC_SHM:
		return sv_shmctl(id, SV_IPC_RMID, SV_NULL);
	case SV_IPC_MSG:
		return sv_msgctl(id, SV_IPC_RMID, SV_NULL);
	default:
		return sv_semctl(id, 0, SV_IPC_RMID, SV_NULL);
	}
}

static void sv_ipc_remove_counted(int kind, int id, struct sv_sweep_stats *st)
{
	int r = sv_ipc_remove(kind, id);
	if (r < 0 && r != -SV_EINVAL && r != -SV_ENOENT)
		st->failed = 1;
}

int sv_sweep_ipc_listing(const char *path, int kind, sv_u32 uid, struct sv_sweep_stats *st)
{
	int fd = sv_openat(SV_AT_FDCWD, path, SV_O_RDONLY | SV_O_CLOEXEC | SV_O_NOFOLLOW, 0);
	if (fd < 0)
		return -1;
	char buf[8192];
	char line[512];
	sv_size llen = 0;
	int overlong = 0;
	for (;;) {
		sv_ssize n = sv_read(fd, buf, sizeof(buf));
		if (n < 0) {
			st->failed = 1;
			break;
		}
		if (n == 0)
			break;
		for (sv_ssize i = 0; i < n; i++) {
			if (buf[i] != '\n') {
				if (llen < sizeof(line) - 1)
					line[llen++] = buf[i];
				else
					overlong = 1;
				continue;
			}
			line[llen] = 0;
			int id;
			/* The header line and an unparsable line select nothing. */
			if (!overlong && sv_ipc_line_select(line, kind, uid, &id) == 1)
				sv_ipc_remove_counted(kind, id, st);
			llen = 0;
			overlong = 0;
		}
	}
	sv_close(fd);
	return 0;
}

void sv_sweep_ipc_stat(int kind, sv_u32 uid, struct sv_sweep_stats *st)
{
	/* Large enough for shmid64_ds, msqid64_ds, semid64_ds, and the *_INFO structures. */
	sv_u64 buf[64];
	int maxidx;
	switch (kind) {
	case SV_IPC_SHM:
		maxidx = sv_shmctl(0, SV_SHM_INFO, buf);
		break;
	case SV_IPC_MSG:
		maxidx = sv_msgctl(0, SV_MSG_INFO, buf);
		break;
	default:
		maxidx = sv_semctl(0, 0, SV_SEM_INFO, buf);
		break;
	}
	if (maxidx < 0) {
		st->failed = 1;
		return;
	}
	for (int idx = 0; idx <= maxidx; idx++) {
		int id;
		sv_bytezero(buf, sizeof(buf));
		switch (kind) {
		case SV_IPC_SHM:
			id = sv_shmctl(idx, SV_SHM_STAT, buf);
			break;
		case SV_IPC_MSG:
			id = sv_msgctl(idx, SV_MSG_STAT, buf);
			break;
		default:
			id = sv_semctl(idx, 0, SV_SEM_STAT, buf);
			break;
		}
		if (id < 0)
			continue; /* an unused index */
		const struct sv_ipc64_perm *perm = (const struct sv_ipc64_perm *)buf;
		if (perm->uid == uid || perm->cuid == uid)
			sv_ipc_remove_counted(kind, id, st);
	}
}

void sv_sweep_mqueue(const char *mount, sv_u32 uid, struct sv_sweep_stats *st)
{
	/* The mqueue mount is flat: a depth of 0 removes its queues and walks nothing deeper. */
	sv_sweep_tree(mount, uid, SV_NULL, 0, SV_SWEEP_MAX_ENTRIES, st);
}

int sv_sweep_status(const struct sv_sweep_stats *st)
{
	if (st->bound_reached)
		return SV_WORKER_BOUND;
	return st->failed ? SV_WORKER_FAILED : SV_WORKER_OK;
}

int sv_sweep_all(sv_u32 uid)
{
	static const char *const listings[3] = {
		"/proc/sysvipc/shm",
		"/proc/sysvipc/msg",
		"/proc/sysvipc/sem",
	};
	struct sv_sweep_stats st;
	sv_bytezero(&st, sizeof(st));
	for (int k = 0; k < 3; k++)
		if (sv_sweep_ipc_listing(listings[k], k, uid, &st) < 0)
			sv_sweep_ipc_stat(k, uid, &st);
	sv_sweep_mqueue(sv_mqueue_mount, uid, &st);
	for (int i = 0; sv_sweep_volumes[i] && !st.bound_reached; i++) {
		/* The entry bound is per volume; the workspace base keeps slots/. */
		sv_u64 before = st.entries;
		const char *skip = sv_streq(sv_sweep_volumes[i], sv_workspace_base) ? sv_sweep_skip_top : SV_NULL;
		st.entries = 0;
		sv_sweep_tree(sv_sweep_volumes[i], uid, skip, SV_SWEEP_MAX_DEPTH, SV_SWEEP_MAX_ENTRIES, &st);
		st.entries += before;
	}
	return sv_sweep_status(&st);
}
