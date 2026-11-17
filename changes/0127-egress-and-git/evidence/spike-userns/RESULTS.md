# Mirror start sequence and hostUsers (2026-11-02). Kind, Kubernetes 1.35.0, containerd 2.2.0, runc 1.2.9, kernel 6.12. Raw output: run2.out, run3.out, sweep.out, run.out (Pod Security).
Sequence used: util-linux setpriv from a root entrypoint with capabilities SETUID, SETGID, and CHOWN, allowPrivilegeEscalation false, RuntimeDefault seccomp, read-only root, fsGroup with OnRootMismatch.
