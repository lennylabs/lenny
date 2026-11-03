import os, google.auth, google.auth.transport.requests as tr
from common import swap, sleep
D = "/var/run/netagent/gcp"
swap(D, 1)
creds, proj = google.auth.default(scopes=["https://www.googleapis.com/auth/cloud-platform"])
print("creds:", type(creds).__module__, type(creds).__name__, "project:", proj)
creds.refresh(tr.Request()); print("refresh token:", creds.token)
sess = tr.AuthorizedSession(creds)
print("call1 ->", sess.get(os.environ["API_URL"]).json())
swap(D, 2); sleep()
print("call2 ->", sess.get(os.environ["API_URL"]).json())
