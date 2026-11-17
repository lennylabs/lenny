import os, google.auth, google.auth.transport.requests as tr
from google.auth import compute_engine
try:
    creds, proj = google.auth.default(scopes=["https://www.googleapis.com/auth/cloud-platform"])
except Exception as e:
    print("ERR default:", type(e).__name__, str(e)[:500]); raise SystemExit
print("creds:", type(creds).__module__, type(creds).__name__, "project:", proj)
sess = tr.AuthorizedSession(creds)
for i in (1, 2):
    print(f"call{i} ->", sess.get(os.environ["API_URL"]).json())
print("email:", creds.service_account_email)
