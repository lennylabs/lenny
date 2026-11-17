import os, sys
from azure.identity import WorkloadIdentityCredential, DefaultAzureCredential
from azure.core import PipelineClient
from azure.core.pipeline.policies import BearerTokenCredentialPolicy
from azure.core.rest import HttpRequest
from common import swap, sleep
D = "/var/run/netagent/azure"
swap(D, 1)
mode = os.environ.get("MODE", "wi")
kw = {} if os.environ.get("DISABLE_ID") == "0" else {"disable_instance_discovery": True}
if mode == "default-args":
    cred = DefaultAzureCredential(workload_identity_client_id=os.environ["X_CLIENT_ID"], workload_identity_tenant_id=os.environ["X_TENANT_ID"], **kw)
elif mode == "default":
    cred = DefaultAzureCredential(**kw)
elif mode == "args":
    cred = WorkloadIdentityCredential(tenant_id=os.environ["X_TENANT_ID"], client_id=os.environ["X_CLIENT_ID"],
                                      token_file_path=D + "/token", **kw)
else:
    cred = WorkloadIdentityCredential(**kw)
scope = "https://management.azure.com/.default"
pc = PipelineClient(base_url=os.environ["API_URL"], policies=[BearerTokenCredentialPolicy(cred, scope)])
def call(n):
    try:
        r = pc.send_request(HttpRequest("GET", os.environ["API_URL"]))
        print(f"call{n} ->", r.status_code, r.text())
    except Exception as e:
        print(f"call{n} ERR", type(e).__name__, str(e)[:600])
call(1)
swap(D, 2); sleep()
call(2)
