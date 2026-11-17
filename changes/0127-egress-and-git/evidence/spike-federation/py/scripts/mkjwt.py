import jwt, time, os
from cryptography.hazmat.primitives.asymmetric import rsa
from cryptography.hazmat.primitives import serialization
k = rsa.generate_private_key(public_exponent=65537, key_size=2048)
open("/run-na/issuer-key.pem","wb").write(k.private_bytes(serialization.Encoding.PEM, serialization.PrivateFormat.PKCS8, serialization.NoEncryption()))
auds = {"aws": "sts.amazonaws.com",
        "gcp": "//iam.googleapis.com/projects/123456789/locations/global/workloadIdentityPools/netagent-pool/providers/netagent",
        "azure": "api://AzureADTokenExchange"}
now = int(time.time())
for p, aud in auds.items():
    for v in (1, 2):
        tok = jwt.encode({"iss": "https://issuer.netagent.test", "sub": "tenant-a:session-123", "aud": aud,
                          "iat": now, "nbf": now, "exp": now + 86400, "ver": v}, k, algorithm="RS256", headers={"kid": "test-1"})
        open(f"/run-na/{p}/token.v{v}", "w").write(tok)
    open(f"/run-na/{p}/token", "w").write(open(f"/run-na/{p}/token.v1").read())
print("ok")
