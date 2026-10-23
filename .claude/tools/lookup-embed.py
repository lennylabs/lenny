# /// script
# requires-python = ">=3.10,<3.14"
# dependencies = ["fastembed>=0.4,<1", "numpy"]
# ///
"""Embedding shortlist for lookup.mjs.

lookup.mjs shortlists passages by word overlap before asking Jev to judge them.
Word overlap misses a passage that states the idea in different words, so this
script adds a second shortlist by embedding similarity. lookup.mjs runs it with
`uv run`, which resolves the dependencies above on first use.

Reads one JSON request from the file named as the first argument, or from stdin:
    {"cache": "<path to sqlite file>", "models": "<model directory>", "top": 80, "threads": 4,
     "queries": ["..."], "passages": [{"id": 0, "text": "..."}]}
Writes {"model": "...", "embedded": <count computed this run>, "note": "...",
        "results": [[[id, score], ...] per query]} on stdout. "note" is present
only when the results cover fewer passages than were asked for.

A request of {"cache": ..., "prune": <days>} instead deletes the vectors no
lookup has used for that many days and writes {"pruned": <count>, "kept": <count>}.

Vectors are cached by the SHA-256 of the model name and the passage text, in a
SQLite file that lookup.mjs places under the git common directory. A passage is
therefore embedded once however many worktrees contain it, and a worktree whose
files differ gets vectors for its own text with nothing to invalidate.

Many agents run lookups at once, from several worktrees of one repository on
one machine. Three operator-tunable environment variables bound what each
process takes from the others:
    LOOKUP_EMBED_THREADS    onnxruntime threads per process (default: 2, or 1 on
                            a one-core machine; a request's "threads" replaces the
                            default but not this variable). Each process otherwise
                            uses every core, and sixteen concurrent lookups thrash.
    LOOKUP_EMBED_LOCK_WAIT  seconds to wait for another process's embedding run
                            or model download (default 300). Past it, the results
                            cover only the passages already cached and "note" says
                            so; lookup.mjs still has its word-overlap shortlist.
"""

import fcntl
import hashlib
import json
import os
import sqlite3
import sys
import time

import numpy as np

MODEL = "BAAI/bge-small-en-v1.5"
# The model reads at most 512 tokens; text past this is not seen, so it is not sent.
MAX_CHARS = 2000
# Passages embedded per lock hold. The lock is released between batches so a
# process waiting on it can read what has been committed so far.
BATCH = 256
# Texts per model call. fastembed pads each call to its longest text, so texts
# are sorted by length first; on this model that ran 2.6 times faster than
# unsorted calls of 64, and holds far less memory.
MODEL_BATCH = 16
# How often a waiter retries the lock, and how often it re-reads the cache to see
# whether another process has already embedded everything it needs.
POLL_SECONDS = 0.25
RECHECK_SECONDS = 2.0
SQLITE_CHUNK = 900


def env_number(name: str, default: float) -> float:
    try:
        v = float(os.environ.get(name, ""))
        return v if v > 0 else default
    except ValueError:
        return default


def default_threads() -> int:
    return max(1, min(2, os.cpu_count() or 1))


THREADS_ENV = int(env_number("LOOKUP_EMBED_THREADS", 0))
LOCK_WAIT = env_number("LOOKUP_EMBED_LOCK_WAIT", 300.0)


def key(text: str) -> bytes:
    return hashlib.sha256((MODEL + "\0" + text).encode("utf-8")).digest()


def today() -> int:
    """The day number a row's `used` column records. Day grain keeps the
    last-used bookkeeping to at most one write per row per day."""
    return int(time.time() // 86400)


class Busy(Exception):
    """Another process held a lock past LOCK_WAIT."""


class FileLock:
    """An exclusive flock acquired by non-blocking polling, so a wait has a bound.

    A blocking flock would wait as long as another worktree's cold embedding run
    takes, which is many minutes for a whole scope. `ready`, when given, is
    called every RECHECK_SECONDS while waiting; when it returns true the wait
    ends without the lock, because what the caller needed the lock for has
    already been done by the holder."""

    def __init__(self, path: str):
        self.path = path
        self.f = None

    def acquire(self, wait: float, ready=None) -> bool:
        deadline = time.monotonic() + wait
        f = open(self.path, "a")
        next_check = time.monotonic() + RECHECK_SECONDS
        while True:
            try:
                fcntl.flock(f, fcntl.LOCK_EX | fcntl.LOCK_NB)
                self.f = f
                return True
            except BlockingIOError:
                pass
            now = time.monotonic()
            if ready is not None and now >= next_check:
                next_check = now + RECHECK_SECONDS
                if ready():
                    f.close()
                    return False
            if now >= deadline:
                f.close()
                raise Busy(self.path)
            time.sleep(POLL_SECONDS)

    def release(self) -> None:
        if self.f is not None:
            fcntl.flock(self.f, fcntl.LOCK_UN)
            self.f.close()
            self.f = None


def open_db(path: str) -> sqlite3.Connection:
    # Autocommit mode, so every write transaction below is an explicit BEGIN
    # IMMEDIATE: it takes the write lock up front and waits on the busy timeout,
    # instead of failing with "database is locked" when a read upgrades to a write.
    db = sqlite3.connect(path, timeout=120, isolation_level=None)
    db.execute("PRAGMA journal_mode=WAL")
    db.execute("CREATE TABLE IF NOT EXISTS vec (k BLOB PRIMARY KEY, v BLOB NOT NULL, used INTEGER) WITHOUT ROWID")
    migrate_used_column(db)
    return db


def migrate_used_column(db: sqlite3.Connection) -> None:
    """Add the last-used day to a cache written before it existed. Existing rows
    are stamped with today, so a prune run right after the migration keeps them."""
    if any(r[1] == "used" for r in db.execute("PRAGMA table_info(vec)")):
        return
    db.execute("BEGIN IMMEDIATE")
    try:
        if not any(r[1] == "used" for r in db.execute("PRAGMA table_info(vec)")):
            db.execute("ALTER TABLE vec ADD COLUMN used INTEGER")
            db.execute("UPDATE vec SET used = ?", (today(),))
        db.execute("COMMIT")
    except BaseException:
        db.execute("ROLLBACK")
        raise


def chunks(xs: list, n: int = SQLITE_CHUNK):
    for i in range(0, len(xs), n):
        yield xs[i : i + n]


def load(db: sqlite3.Connection, keys: list) -> dict:
    have = {}
    for chunk in chunks(list(set(keys))):
        marks = ",".join("?" * len(chunk))
        for k, v in db.execute(f"SELECT k, v FROM vec WHERE k IN ({marks})", chunk):
            have[bytes(k)] = np.frombuffer(v, dtype=np.float32)
    return have


def touch(db: sqlite3.Connection, keys: list) -> None:
    """Record that these vectors were used today, for --prune-cache. Best effort:
    a lookup is not failed over bookkeeping."""
    d = today()
    try:
        db.execute("BEGIN IMMEDIATE")
        for chunk in chunks(list(set(keys))):
            marks = ",".join("?" * len(chunk))
            db.execute(f"UPDATE vec SET used = ? WHERE k IN ({marks}) AND (used IS NULL OR used < ?)", [d, *chunk, d])
        db.execute("COMMIT")
    except sqlite3.Error:
        try:
            db.execute("ROLLBACK")
        except sqlite3.Error:
            pass


class Model:
    """Constructs the embedding model on first use, under the model lock.

    fastembed downloads the model into its cache directory on first
    construction. Its default directory is under the system temp directory,
    which a cleanup can wipe and on which concurrent first downloads race, so the
    directory is the one lookup.mjs names under the shared cache and the
    download, together with the construction that triggers it, runs under a lock."""

    def __init__(self, models_dir: str, lock_path: str, threads: int):
        self.models_dir = models_dir
        self.threads = threads
        self.lock_path = lock_path
        self.m = None

    def get(self):
        if self.m is None:
            from fastembed import TextEmbedding

            os.makedirs(self.models_dir, exist_ok=True)
            lock = FileLock(self.lock_path)
            lock.acquire(LOCK_WAIT)
            try:
                self.m = TextEmbedding(MODEL, cache_dir=self.models_dir, threads=self.threads)
            finally:
                lock.release()
        return self.m


def embed_missing(db, model: Model, keys: list, texts: list, have: dict, lock_path: str) -> int:
    """Embed the passages missing from the cache, one batch per lock hold.

    The lock makes one process embed a given passage while the others wait and
    then read it from the cache. Releasing it after each committed batch lets a
    waiter whose passages are already in the cache stop waiting, and lets two
    processes with different missing sets share the work. Raises Busy when one
    wait for the lock exceeds LOCK_WAIT; time spent embedding under the lock
    does not count against it."""
    lock = FileLock(lock_path)
    embedded = 0

    def missing() -> dict:
        todo = {}
        for k, t in zip(keys, texts):
            if k not in have:
                todo[k] = t
        return todo

    def refresh_and_check() -> bool:
        have.update(load(db, [k for k in missing()]))
        return not missing()

    while missing():
        if not lock.acquire(LOCK_WAIT, ready=refresh_and_check):
            break
        try:
            have.update(load(db, list(missing())))
            todo = missing()
            part = sorted(todo, key=lambda k: len(todo[k]))[:BATCH]
            if not part:
                break
            vecs = [np.asarray(v, dtype=np.float32) for v in model.get().embed([todo[k] for k in part], batch_size=MODEL_BATCH)]
            d = today()
            db.execute("BEGIN IMMEDIATE")
            db.executemany("INSERT OR IGNORE INTO vec (k, v, used) VALUES (?, ?, ?)", [(k, v.tobytes(), d) for k, v in zip(part, vecs)])
            db.execute("COMMIT")
            have.update(zip(part, vecs))
            embedded += len(part)
        finally:
            lock.release()
        # flock is not fair: without a pause the holder retakes the lock before any
        # poller wakes. One poll interval lets a waiter in.
        if missing():
            time.sleep(POLL_SECONDS * 1.5)
    return embedded


def rank(model: Model, queries: list, passages: list, keys: list, have: dict, top: int) -> list:
    """Rank the passages with a cached vector against each query."""
    idx_have = [i for i, k in enumerate(keys) if k in have]
    if not queries or not idx_have:
        return [[] for _ in queries]
    mat = np.stack([have[keys[i]] for i in idx_have]).astype(np.float32, copy=True)
    mat /= np.linalg.norm(mat, axis=1, keepdims=True) + 1e-12
    results = []
    for q in model.get().query_embed(queries):
        qv = np.asarray(q, dtype=np.float32)
        qv = qv / (np.linalg.norm(qv) + 1e-12)
        sims = mat @ qv
        n = min(top, len(sims))
        best = np.argpartition(-sims, n - 1)[:n]
        best = best[np.argsort(-sims[best])]
        results.append([[passages[idx_have[int(i)]]["id"], round(float(sims[int(i)]), 4)] for i in best])
    return results


def prune(req: dict) -> dict:
    """Delete vectors unused for `prune` days. A worktree's passages are touched on
    every lookup, so what goes is text no current tree has looked at lately."""
    db = open_db(req["cache"])
    cutoff = today() - int(req["prune"])
    db.execute("BEGIN IMMEDIATE")
    n = db.execute("DELETE FROM vec WHERE used IS NOT NULL AND used < ?", (cutoff,)).rowcount
    db.execute("COMMIT")
    kept = db.execute("SELECT COUNT(*) FROM vec").fetchone()[0]
    try:
        # Returns the freed pages to the filesystem. It needs every other
        # connection idle and is skipped, not waited on, when one is not.
        db.execute("PRAGMA busy_timeout = 2000")
        db.execute("VACUUM")
    except sqlite3.Error:
        pass
    return {"pruned": n, "kept": kept}


def serve(req: dict) -> dict:
    cache = req["cache"]
    cache_dir = os.path.dirname(cache)
    passages = req.get("passages", [])
    queries = req.get("queries", [])
    top = int(req.get("top", 80))
    texts = [p["text"][:MAX_CHARS] for p in passages]
    keys = [key(t) for t in texts]
    # The environment wins over the request, so an operator's setting holds for
    # --warm too, which otherwise asks for more threads as the single process
    # running ahead of a fan-out.
    threads = THREADS_ENV or int(req.get("threads") or 0) or default_threads()
    model = Model(req.get("models") or os.path.join(cache_dir, "models"), cache + ".model.lock", threads)

    db = open_db(cache)
    have = load(db, keys)
    embedded = 0
    note = None
    try:
        embedded = embed_missing(db, model, keys, texts, have, cache + ".lock")
    except Busy as e:
        cached = sum(1 for k in keys if k in have)
        note = (
            f"embedding busy: another process held {os.path.basename(e.args[0])} past {LOCK_WAIT:g}s, "
            f"so the embedding shortlist covers the {cached} of {len(keys)} passages already cached"
        )
    touch(db, [k for k in keys if k in have])
    results = [[] for _ in queries]
    if queries and passages:
        try:
            results = rank(model, queries, passages, keys, have, top)
        except Busy:
            note = f"embedding busy: the model lock was held past {LOCK_WAIT:g}s, so no embedding shortlist was computed"
    out = {"model": MODEL, "embedded": embedded, "results": results}
    if note:
        out["note"] = note
    return out


def main() -> None:
    if len(sys.argv) > 1:
        with open(sys.argv[1], encoding="utf-8") as f:
            req = json.load(f)
    else:
        req = json.load(sys.stdin)
    out = prune(req) if "prune" in req else serve(req)
    json.dump(out, sys.stdout)


if __name__ == "__main__":
    main()
