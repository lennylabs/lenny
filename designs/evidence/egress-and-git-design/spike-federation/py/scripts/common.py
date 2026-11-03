import os, shutil, time
def swap(d, v):
    shutil.copy(f"{d}/token.v{v}", f"{d}/token.tmp"); os.replace(f"{d}/token.tmp", f"{d}/token")
def sleep():
    time.sleep(int(os.environ.get("SLEEP", "31")))
