import os
from azure.identity import ManagedIdentityCredential, DefaultAzureCredential
mode = os.environ.get("MODE", "mi")
cred = DefaultAzureCredential() if mode == "default" else ManagedIdentityCredential(**({"client_id": os.environ["X_CLIENT_ID"]} if os.environ.get("X_CLIENT_ID") else {}))
try:
    t = cred.get_token("https://management.azure.com/.default")
    print("token:", t.token, "expires_on:", t.expires_on)
except Exception as e:
    print("ERR", type(e).__name__, str(e)[:800])
