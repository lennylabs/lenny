# /// script
# requires-python = ">=3.10"
# dependencies = ["fastembed>=0.4,<1", "numpy"]
# ///
"""Embedding shortlist for lookup.mjs.

lookup.mjs shortlists passages by word overlap before asking Jev to judge them.
Word overlap misses a passage that states the idea in different words, so this
script adds a second shortlist by embedding similarity. lookup.mjs runs it with
`uv run`, which resolves the dependencies above on first use.

Reads one JSON request from the file named as the first argument, or from stdin:
    {"cache": "<path to sqlite file>", "top": 80,
     "queries": ["..."], "passages": [{"id": 0, "text": "..."}]}
Writes {"model": "...", "embedded": <count computed this run>,
        "results": [[[id, score], ...] per query]} on stdout.

Vectors are cached by the SHA-256 of the model name and the passage text, in a
SQLite file that lookup.mjs places under the git common directory. A passage is
therefore embedded once however many worktrees contain it, and a worktree whose
files differ gets vectors for its own text with nothing to invalidate.
"""

import fcntl
import hashlib
import json
import sqlite3
import sys

import numpy as np
from fastembed import TextEmbedding

MODEL = "BAAI/bge-small-en-v1.5"
# The model reads at most 512 tokens; text past this is not seen, so it is not sent.
MAX_CHARS = 2000


def key(text: str) -> bytes:
    return hashlib.sha256((MODEL + "\0" + text).encode("utf-8")).digest()


def main() -> None:
    if len(sys.argv) > 1:
        with open(sys.argv[1], encoding="utf-8") as f:
            req = json.load(f)
    else:
        req = json.load(sys.stdin)
    passages = req.get("passages", [])
    queries = req.get("queries", [])
    top = int(req.get("top", 80))
    texts = [p["text"][:MAX_CHARS] for p in passages]
    keys = [key(t) for t in texts]

    db = sqlite3.connect(req["cache"], timeout=120)
    db.execute("PRAGMA journal_mode=WAL")
    db.execute("CREATE TABLE IF NOT EXISTS vec (k BLOB PRIMARY KEY, v BLOB NOT NULL) WITHOUT ROWID")

    def load() -> dict:
        have = {}
        uniq = list(set(keys))
        for i in range(0, len(uniq), 900):
            chunk = uniq[i : i + 900]
            marks = ",".join("?" * len(chunk))
            for k, v in db.execute(f"SELECT k, v FROM vec WHERE k IN ({marks})", chunk):
                have[bytes(k)] = np.frombuffer(v, dtype=np.float32)
        return have

    have = load()
    model = None
    embedded = 0
    if any(k not in have for k in keys):
        # Reviewers run in parallel. The lock makes one process embed the missing
        # passages while the others wait and then read them from the cache.
        with open(req["cache"] + ".lock", "w") as lock:
            fcntl.flock(lock, fcntl.LOCK_EX)
            have = load()
            todo = {}
            for k, t in zip(keys, texts):
                if k not in have:
                    todo[k] = t
            if todo:
                model = TextEmbedding(MODEL)
                ks = list(todo)
                for i in range(0, len(ks), 512):
                    part = ks[i : i + 512]
                    vecs = list(model.embed([todo[k] for k in part], batch_size=64))
                    rows = [(k, np.asarray(v, dtype=np.float32).tobytes()) for k, v in zip(part, vecs)]
                    db.executemany("INSERT OR IGNORE INTO vec (k, v) VALUES (?, ?)", rows)
                    db.commit()
                    for k, v in zip(part, vecs):
                        have[k] = np.asarray(v, dtype=np.float32)
                embedded = len(ks)

    results = []
    if queries and passages:
        if model is None:
            model = TextEmbedding(MODEL)
        mat = np.stack([have[k] for k in keys])
        mat /= np.linalg.norm(mat, axis=1, keepdims=True) + 1e-12
        for q in model.query_embed(queries):
            qv = np.asarray(q, dtype=np.float32)
            qv /= np.linalg.norm(qv) + 1e-12
            sims = mat @ qv
            n = min(top, len(sims))
            idx = np.argpartition(-sims, n - 1)[:n]
            idx = idx[np.argsort(-sims[idx])]
            results.append([[passages[int(i)]["id"], round(float(sims[int(i)]), 4)] for i in idx])
    json.dump({"model": MODEL, "embedded": embedded, "results": results}, sys.stdout)


if __name__ == "__main__":
    main()
