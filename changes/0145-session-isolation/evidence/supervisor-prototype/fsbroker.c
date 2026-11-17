/* SPDX-License-Identifier: MIT */
/*
 * The fd broker: file operations the adapter asks for on CH-SUPERVISE, run
 * as the slot UID beneath the broker root and confined by openat2. Each
 * request names up to SV_FS_MAX paths; the reply carries one errno per
 * path (0 for success) and, for open, mkfile, and inotify, one descriptor
 * per success over SCM_RIGHTS, in path order. The reply holds integers
 * only, so no byte the runtime wrote crosses the channel in the reply
 * line; file content and directory entries reach the adapter only through
 * the descriptors.
 */
#include "supervisor.h"

#define SV_FS_RESOLVE (SV_RESOLVE_BENEATH | SV_RESOLVE_NO_XDEV | SV_RESOLVE_NO_SYMLINKS | SV_RESOLVE_NO_MAGICLINKS)
#define SV_FS_REPLY (96 * 1024)
#define SV_FS_NROOTS 4
/* statat answers these integers per path, in this order. */
#define SV_FS_STAT_N 11

static struct {
	const char *name;
	int fd;
} sv_fs_roots[SV_FS_NROOTS];
static int sv_fs_nroots;
static int sv_fs_root = -1; /* the root the current request names */
static int sv_fs_inotify = -1;

int sv_fs_init(const char *spec)
{
	static char names[SV_FS_NROOTS][33];
	const char *eq = spec;
	while (*eq && *eq != '=')
		eq++;
	sv_size n = (sv_size)(eq - spec);
	if (!*eq || n == 0 || n > 32 || sv_fs_nroots >= SV_FS_NROOTS)
		return -1;
	sv_bytecopy(names[sv_fs_nroots], spec, n);
	names[sv_fs_nroots][n] = 0;
	int fd = sv_openat(SV_AT_FDCWD, eq + 1, SV_O_PATH | SV_O_DIRECTORY | SV_O_NOFOLLOW | SV_O_CLOEXEC, 0);
	if (fd < 0)
		return -1;
	sv_fs_roots[sv_fs_nroots].name = names[sv_fs_nroots];
	sv_fs_roots[sv_fs_nroots].fd = fd;
	sv_fs_nroots++;
	return 0;
}

static int sv_fs_select(const char *name)
{
	for (int i = 0; name && i < sv_fs_nroots; i++)
		if (sv_streq(name, sv_fs_roots[i].name))
			return sv_fs_roots[i].fd;
	return -1;
}

static int sv_fs_open(const char *path, sv_u64 flags)
{
	struct sv_open_how how = {flags | SV_O_CLOEXEC, 0, SV_FS_RESOLVE};
	return sv_openat2(sv_fs_root, path, &how);
}

int sv_fs_opendir(const char *root, const char *path)
{
	sv_fs_root = sv_fs_select(root);
	if (sv_fs_root < 0)
		return -SV_ENOENT;
	return sv_fs_open(*path ? path : ".", SV_O_PATH | SV_O_DIRECTORY);
}

/*
 * sv_fs_parent opens the directory that holds path's last component and
 * points *base at that component, which may not be empty, ".", or "..".
 */
static int sv_fs_parent(const char *path, const char **base)
{
	static char dir[SV_MAX_ARG_BYTES + 1];
	sv_size n = sv_strlen(path), cut = n;
	while (cut > 0 && path[cut - 1] != '/')
		cut--;
	*base = path + cut;
	if (!**base || sv_streq(*base, ".") || sv_streq(*base, ".."))
		return -SV_EINVAL;
	if (cut == 0) {
		dir[0] = '.';
		dir[1] = 0;
	} else {
		sv_bytecopy(dir, path, cut - 1);
		dir[cut - 1] = 0;
	}
	return sv_fs_open(dir, SV_O_PATH | SV_O_DIRECTORY);
}

/*
 * sv_fs_one runs op on path i. It returns a descriptor to pass (>= 0), or
 * SV_FS_NOFD for a success with no descriptor, or a negated errno.
 */
#define SV_FS_NOFD (-1000000)
static int sv_fs_one(const struct sv_request *r, int i, int *wd)
{
	const char *p = r->paths[i];
	const char *base;
	int fd, d, rc;
	/* A mode with the set-user-ID or set-group-ID bit, or any bit above 07777, is refused. */
	if ((r->fs_op == SV_FS_MKFILE || r->fs_op == SV_FS_MKDIR || r->fs_op == SV_FS_MKFIFO ||
	     r->fs_op == SV_FS_CHMOD) && (r->nums[i] & ~(sv_u64)01777))
		return -SV_EINVAL;
	switch (r->fs_op) {
	case SV_FS_OPEN:
		fd = sv_fs_open(p, SV_O_RDONLY | SV_O_NOFOLLOW | SV_O_NONBLOCK | SV_O_NOCTTY);
		/* A symlink, a socket, or a file its owner cannot read: a path-only descriptor. */
		if (fd == -SV_ELOOP || fd == -SV_ENXIO || fd == -SV_EACCES)
			fd = sv_fs_open(p, SV_O_PATH | SV_O_NOFOLLOW);
		return fd;
	case SV_FS_OPENPATH:
		return sv_fs_open(p, SV_O_PATH | SV_O_NOFOLLOW);
	case SV_FS_CHMOD:
		fd = sv_fs_open(p, SV_O_RDONLY | SV_O_DIRECTORY | SV_O_NOFOLLOW);
		if (fd < 0)
			return fd;
		rc = sv_fchmod(fd, (sv_u32)(r->nums[i] & 01777));
		sv_close(fd);
		return rc < 0 ? rc : SV_FS_NOFD;
	case SV_FS_WATCH: {
		fd = sv_fs_open(p, SV_O_PATH | SV_O_DIRECTORY | SV_O_NOFOLLOW);
		if (fd < 0)
			return fd;
		/* inotify takes a path: the magic link of the pinned descriptor names exactly that inode. */
		char link[32] = "/proc/self/fd/";
		link[14 + sv_fmt_u64(link + 14, (sv_u64)fd)] = 0;
		rc = sv_inotify_add_watch(sv_fs_inotify, link, SV_IN_TRACK_MASK | SV_IN_ONLYDIR);
		sv_close(fd);
		if (rc < 0)
			return rc;
		*wd = rc;
		return SV_FS_NOFD;
	}
	default:
		break;
	}
	d = sv_fs_parent(p, &base);
	if (d < 0)
		return d;
	switch (r->fs_op) {
	case SV_FS_MKFILE:
		rc = sv_openat(d, base, SV_O_WRONLY | SV_O_CREAT | SV_O_EXCL | SV_O_NOFOLLOW | SV_O_CLOEXEC,
			       (int)(r->nums[i] & 0777));
		break;
	case SV_FS_MKDIR:
		rc = sv_mkdirat(d, base, (sv_u32)(r->nums[i] & 01777));
		break;
	case SV_FS_MKFIFO:
		rc = sv_mknodat(d, base, SV_S_IFIFO | (sv_u32)(r->nums[i] & 0777));
		break;
	case SV_FS_SYMLINK:
		rc = sv_symlinkat(r->targets[i], d, base);
		break;
	case SV_FS_LINK: {
		const char *tbase;
		int td = sv_fs_parent(r->targets[i], &tbase);
		rc = td < 0 ? td : sv_linkat(td, tbase, d, base);
		if (td >= 0)
			sv_close(td);
		break;
	}
	case SV_FS_UTIMENS: {
		struct sv_timespec ts[2] = {{(sv_i64)r->nums[4 * i], (long)r->nums[4 * i + 1]},
					    {(sv_i64)r->nums[4 * i + 2], (long)r->nums[4 * i + 3]}};
		rc = sv_utimensat(d, base, ts, SV_AT_SYMLINK_NOFOLLOW);
		break;
	}
	default:
		rc = -SV_EINVAL;
	}
	sv_close(d);
	if (rc < 0)
		return rc;
	return r->fs_op == SV_FS_MKFILE ? rc : SV_FS_NOFD;
}

/*
 * sv_fs_statat answers the identity of path i as integers, with no
 * descriptor: the parent is resolved beneath the root with openat2 and
 * reused for consecutive paths in the same directory, the final component
 * is read with statx and AT_SYMLINK_NOFOLLOW, and a final component on
 * another mount than its parent is refused with EXDEV.
 */
static char sv_fs_pdir[SV_MAX_ARG_BYTES + 1];
static int sv_fs_pfd = -1;
static sv_u64 sv_fs_pmnt;
static int sv_fs_pmnt_ok;

static void sv_fs_pdrop(void)
{
	if (sv_fs_pfd >= 0)
		sv_close(sv_fs_pfd);
	sv_fs_pfd = -1;
	sv_fs_pdir[0] = 0;
}

static int sv_fs_statat(const char *p, sv_u64 *out)
{
	sv_size n = sv_strlen(p), cut = n;
	while (cut > 0 && p[cut - 1] != '/')
		cut--;
	const char *base = p + cut;
	if (!*base || sv_streq(base, ".") || sv_streq(base, ".."))
		return -SV_EINVAL;
	sv_size dl = cut ? cut - 1 : 1;
	const char *dsrc = cut ? p : ".";
	int same = sv_fs_pfd >= 0 && sv_strlen(sv_fs_pdir) == dl;
	for (sv_size k = 0; same && k < dl; k++)
		if (sv_fs_pdir[k] != dsrc[k])
			same = 0;
	if (!same) {
		sv_fs_pdrop();
		sv_bytecopy(sv_fs_pdir, dsrc, dl);
		sv_fs_pdir[dl] = 0;
		int d = sv_fs_open(sv_fs_pdir, SV_O_PATH | SV_O_DIRECTORY);
		if (d < 0) {
			sv_fs_pdir[0] = 0;
			return d;
		}
		sv_fs_pfd = d;
		struct sv_statx ps;
		sv_fs_pmnt_ok = sv_statx(d, "", SV_AT_EMPTY_PATH, SV_STATX_MNT_ID, &ps) == 0 &&
				(ps.stx_mask & SV_STATX_MNT_ID);
		sv_fs_pmnt = ps.stx_mnt_id;
	}
	struct sv_statx st;
	int rc = sv_statx(sv_fs_pfd, base, SV_AT_SYMLINK_NOFOLLOW, SV_STATX_BASIC_STATS | SV_STATX_MNT_ID, &st);
	if (rc < 0)
		return rc;
	if (sv_fs_pmnt_ok && (st.stx_mask & SV_STATX_MNT_ID) && st.stx_mnt_id != sv_fs_pmnt)
		return -SV_EXDEV;
	out[0] = st.stx_dev_major;
	out[1] = st.stx_dev_minor;
	out[2] = st.stx_ino;
	out[3] = st.stx_mode;
	out[4] = st.stx_nlink;
	out[5] = st.stx_size;
	out[6] = st.stx_blocks;
	out[7] = (sv_u64)st.stx_mtime.tv_sec;
	out[8] = st.stx_mtime.tv_nsec;
	out[9] = (sv_u64)st.stx_ctime.tv_sec;
	out[10] = st.stx_ctime.tv_nsec;
	return 0;
}

static sv_size sv_fs_cat(char *out, sv_size n, const char *s)
{
	while (*s)
		out[n++] = *s++;
	return n;
}

static sv_size sv_fs_list(char *out, sv_size n, const char *name, const int *v, int count)
{
	n = sv_fs_cat(out, n, name);
	for (int i = 0; i < count; i++) {
		if (i)
			out[n++] = ',';
		n += sv_fmt_u64(out + n, (sv_u64)v[i]);
	}
	return sv_fs_cat(out, n, "]");
}

/* sv_fs_reply writes the fsdone line for the request's per-path results. */
static sv_size sv_fs_reply(char *out, const struct sv_request *r, const int *errs, const int *wds, int nwd,
			   const sv_u64 *sts)
{
	sv_size n = sv_fs_cat(out, 0, "{\"type\":\"fsdone\",\"gen\":");
	n += sv_fmt_u64(out + n, r->gen);
	n = sv_fs_cat(out, n, ",\"id\":");
	n += sv_fmt_u64(out + n, r->id);
	n = sv_fs_list(out, n, ",\"errs\":[", errs, r->npaths);
	if (r->fs_op == SV_FS_WATCH)
		n = sv_fs_list(out, n, ",\"wds\":[", wds, nwd);
	if (r->fs_op == SV_FS_STATAT) {
		n = sv_fs_cat(out, n, ",\"st\":[");
		for (int i = 0; i < r->npaths * SV_FS_STAT_N; i++) {
			if (i)
				out[n++] = ',';
			n += sv_fmt_u64(out + n, sts[i]);
		}
		n = sv_fs_cat(out, n, "]");
	}
	return sv_fs_cat(out, n, "}\n");
}

int sv_fs_handle(int conn, const struct sv_request *r)
{
	static char out[SV_FS_REPLY];
	static int errs[SV_FS_MAX], wds[SV_FS_MAX], fds[SV_FS_MAX];
	static char cbuf[sizeof(struct sv_cmsghdr) + SV_FS_MAX * sizeof(int) + 8];
	static sv_u64 sts[SV_FS_MAX * SV_FS_STAT_N];
	int nfd = 0, nwd = 0, keep = -1;
	sv_fs_root = sv_fs_select(r->fs_root);
	sv_u32 old = sv_umask(0); /* the request's modes are exact */
	for (int i = 0; i < r->npaths; i++) {
		int wd = 0, rc;
		if (sv_fs_root < 0) {
			rc = -SV_EINVAL;
		} else if (r->fs_op == SV_FS_STATAT) {
			sv_u64 *o = sts + (sv_size)i * SV_FS_STAT_N;
			for (int k = 0; k < SV_FS_STAT_N; k++)
				o[k] = 0;
			rc = sv_fs_statat(r->paths[i], o);
			if (rc == 0)
				rc = SV_FS_NOFD;
		} else if (r->fs_op == SV_FS_INOTIFY) {
			if (sv_fs_inotify < 0)
				sv_fs_inotify = sv_inotify_init1(SV_IN_NONBLOCK | SV_IN_CLOEXEC);
			rc = keep = sv_fs_inotify;
		} else if (r->fs_op == SV_FS_WATCH && sv_fs_inotify < 0) {
			rc = -SV_EINVAL;
		} else {
			rc = sv_fs_one(r, i, &wd);
		}
		errs[i] = rc >= 0 || rc == SV_FS_NOFD ? 0 : -rc;
		if (rc >= 0)
			fds[nfd++] = rc;
		if (r->fs_op == SV_FS_WATCH)
			wds[nwd++] = wd;
	}
	sv_umask(old);
	sv_fs_pdrop();
	sv_size n = sv_fs_reply(out, r, errs, wds, nwd, sts);

	sv_size clen = 0;
	if (nfd) {
		struct sv_cmsghdr *c = (struct sv_cmsghdr *)cbuf;
		c->len = sizeof(*c) + (sv_size)nfd * sizeof(int);
		c->level = SV_SOL_SOCKET;
		c->type = SV_SCM_RIGHTS;
		sv_bytecopy(cbuf + sizeof(*c), fds, (sv_size)nfd * sizeof(int));
		clen = (c->len + 7) & ~(sv_size)7;
	}
	int w = sv_send_all(conn, out, n, clen ? cbuf : SV_NULL, clen);
	/*
	 * The kernel can refuse the descriptors and leave the connection
	 * intact: ETOOMANYREFS when processes at the slot UID anywhere on the
	 * node hold more descriptors in flight than RLIMIT_NOFILE. Nothing was
	 * sent, so each path that had a descriptor is answered with that errno,
	 * and the line goes alone. A file mkfile created is removed first, so a
	 * retry does not meet EEXIST.
	 */
	if (nfd && w < 0 && w != -SV_EPIPE && w != -SV_EAGAIN && w != -SV_ECONNRESET) {
		for (int i = 0; i < r->npaths; i++) {
			if (errs[i] != 0)
				continue;
			errs[i] = -w;
			const char *base;
			int d = r->fs_op == SV_FS_MKFILE ? sv_fs_parent(r->paths[i], &base) : -1;
			if (d >= 0) {
				sv_unlinkat(d, base, 0);
				sv_close(d);
			}
		}
		n = sv_fs_reply(out, r, errs, wds, nwd, sts);
		w = sv_send_all(conn, out, n, SV_NULL, 0);
	}
	for (int i = 0; i < nfd; i++)
		if (fds[i] != keep)
			sv_close(fds[i]);
	return w < 0 ? -1 : 0;
}
