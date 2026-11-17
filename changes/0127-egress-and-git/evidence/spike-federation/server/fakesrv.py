"""Throwaway fake STS / metadata / API server for the federation spike.

Routes by path (host is only logged). Listens on HTTP :80, HTTP :8080 and HTTPS :443.
Every request is logged as one JSON line to stdout and /logs/server.jsonl.
"""
import base64
import datetime
import hashlib
import json
import os
import ssl
import threading
import time
import urllib.parse
import zlib
import re
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

EXP = int(os.environ.get("EXP", "20"))
MSI_SECRET = os.environ.get("MSI_SECRET", "s3cr3t-identity-header")
LOG = open("/logs/server.jsonl", "a", buffering=1)
LOCK = threading.Lock()


def jwt_claims(tok):
    try:
        p = tok.split(".")[1]
        p += "=" * (-len(p) % 4)
        return json.loads(base64.urlsafe_b64decode(p))
    except Exception:
        return None


def tag(tok):
    c = jwt_claims(tok) or {}
    return "v%s-%s" % (c.get("ver", "x"), hashlib.sha1(tok.encode()).hexdigest()[:6])


def iso(t):
    return datetime.datetime.fromtimestamp(t, datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


class H(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, *a):
        pass

    def _log(self, kind, extra):
        hdrs = {k: v for k, v in self.headers.items() if k.lower() in (
            "host", "authorization", "metadata-flavor", "metadata", "x-identity-header",
            "x-amz-target", "x-amz-security-token", "content-type", "user-agent",
            "x-goog-api-client", "x-amz-date", "x-client-sku", "x-client-ver")}
        rec = {"ts": time.strftime("%H:%M:%S"), "port": self.server.server_port,
               "peer": self.client_address[0], "kind": kind, "method": self.command,
               "path": self.path, "headers": hdrs}
        rec.update(extra)
        line = json.dumps(rec)
        with LOCK:
            print(line, flush=True)
            LOG.write(line + "\n")

    def _send(self, code, body, ctype="application/json", extra=None):
        if isinstance(body, (dict, list)):
            body = json.dumps(body)
        b = body.encode() if isinstance(body, str) else body
        self.send_response(code)
        self.send_header("Content-Type", ctype)
        self.send_header("Content-Length", str(len(b)))
        for k, v in (extra or {}).items():
            self.send_header(k, v)
        self.end_headers()
        self.wfile.write(b)

    def _body(self):
        n = int(self.headers.get("Content-Length") or 0)
        return self.rfile.read(n).decode(errors="replace") if n else ""

    def do_GET(self):
        self.route("")

    def do_POST(self):
        self.route(self._body())

    def route(self, body):
        u = urllib.parse.urlparse(self.path)
        path, q = u.path, urllib.parse.parse_qs(u.query)
        form = urllib.parse.parse_qs(body) if body and "=" in body and not body.lstrip().startswith("{") else {}
        f1 = {k: v[0] for k, v in form.items()}
        target = self.headers.get("X-Amz-Target", "")
        now = int(time.time())

        # ---------- AWS STS ----------
        if f1.get("Action") == "AssumeRoleWithWebIdentity" or target.endswith(".AssumeRoleWithWebIdentity"):
            if target:
                j = json.loads(body or "{}")
                tok, role, sess = j.get("WebIdentityToken", ""), j.get("RoleArn"), j.get("RoleSessionName")
            else:
                tok, role, sess = f1.get("WebIdentityToken", ""), f1.get("RoleArn"), f1.get("RoleSessionName")
            t = tag(tok)
            akid = "ASIAFAKE" + t.upper().replace("-", "")
            self._log("aws-sts", {"RoleArn": role, "RoleSessionName": sess, "token_tag": t,
                                  "token_claims": jwt_claims(tok), "issued_akid": akid,
                                  "form_keys": sorted(f1.keys())})
            exp = now + EXP
            if target:
                return self._send(200, {"Credentials": {"AccessKeyId": akid, "SecretAccessKey": "fakesecret",
                                                        "SessionToken": "sess-" + t, "Expiration": exp},
                                        "AssumedRoleUser": {"Arn": "arn:aws:sts::111122223333:assumed-role/x/%s" % sess,
                                                            "AssumedRoleId": "AROAFAKE:%s" % sess}},
                                  "application/x-amz-json-1.1")
            xml = f"""<AssumeRoleWithWebIdentityResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/">
<AssumeRoleWithWebIdentityResult><SubjectFromWebIdentityToken>sub</SubjectFromWebIdentityToken>
<Audience>sts.amazonaws.com</Audience><AssumedRoleUser><Arn>arn:aws:sts::111122223333:assumed-role/x/{sess}</Arn>
<AssumedRoleId>AROAFAKE:{sess}</AssumedRoleId></AssumedRoleUser><Credentials><SessionToken>sess-{t}</SessionToken>
<SecretAccessKey>fakesecret</SecretAccessKey><Expiration>{iso(exp)}</Expiration><AccessKeyId>{akid}</AccessKeyId>
</Credentials><Provider>https://issuer.netagent.test</Provider></AssumeRoleWithWebIdentityResult>
<ResponseMetadata><RequestId>11111111-2222-3333-4444-555555555555</RequestId></ResponseMetadata>
</AssumeRoleWithWebIdentityResponse>"""
            return self._send(200, xml, "text/xml")

        # ---------- AWS DynamoDB (fake service) ----------
        if target.startswith("DynamoDB_"):
            auth = self.headers.get("Authorization", "")
            akid = auth.split("Credential=")[1].split("/")[0] if "Credential=" in auth else None
            self._log("aws-service", {"op": target, "akid": akid,
                                      "security_token": self.headers.get("X-Amz-Security-Token")})
            b = json.dumps({"TableNames": ["seen-" + str(akid)]})
            return self._send(200, b, "application/x-amz-json-1.0", {"x-amz-crc32": str(zlib.crc32(b.encode()))})

        # ---------- GCP STS ----------
        if path == "/v1/token" and self.command == "POST":
            if body.lstrip().startswith("{"):
                f1 = json.loads(body)
            tok = f1.get("subject_token", "")
            t = tag(tok)
            self._log("gcp-sts", {"audience": f1.get("audience"), "subject_token_type": f1.get("subject_token_type"),
                                  "scope": f1.get("scope"), "grant_type": f1.get("grant_type"),
                                  "options": f1.get("options"), "token_tag": t, "token_claims": jwt_claims(tok)})
            return self._send(200, {"access_token": "gcp-sts-at-" + t,
                                    "issued_token_type": "urn:ietf:params:oauth:token-type:access_token",
                                    "token_type": "Bearer", "expires_in": EXP})
        if path.endswith(":generateAccessToken"):
            self._log("gcp-iamcreds", {"body": body})
            at = self.headers.get("Authorization", "").replace("Bearer ", "")
            return self._send(200, {"accessToken": "gcp-sa-at-from-" + at, "expireTime": iso(now + EXP)})

        # ---------- GCE metadata ----------
        if path == "/" or path.startswith("/computeMetadata"):
            flavor = self.headers.get("Metadata-Flavor")
            if flavor != "Google":
                self._log("gce-md-reject", {"reason": "missing Metadata-Flavor"})
                return self._send(403, "Missing Metadata-Flavor:Google header", "text/plain")
            mh = {"Metadata-Flavor": "Google"}
            sa = "/computeMetadata/v1/instance/service-accounts/default"
            path = re.sub(r"/service-accounts/[^/]+", "/service-accounts/default", path)
            if path == "/" or path in ("/computeMetadata/v1", "/computeMetadata/v1/", "/computeMetadata/v1/instance", "/computeMetadata/v1/instance/"):
                self._log("gce-md", {})
                return self._send(200, "computeMetadata/\n", "text/plain", mh)
            if path == sa + "/token":
                c = int(time.time())
                self._log("gce-md-token", {"query": q})
                return self._send(200, {"access_token": "gce-md-at-%d" % c, "expires_in": EXP,
                                        "token_type": "Bearer"}, extra=mh)
            if path in (sa + "/email",):
                self._log("gce-md", {})
                return self._send(200, "tenant-a@proj-a.iam.gserviceaccount.com", "text/plain", mh)
            if path in (sa, sa + "/"):
                self._log("gce-md", {"query": q})
                return self._send(200, {"aliases": ["default"], "email": "tenant-a@proj-a.iam.gserviceaccount.com",
                                        "scopes": ["https://www.googleapis.com/auth/cloud-platform"]}, extra=mh)
            if path == sa + "/scopes":
                self._log("gce-md", {})
                return self._send(200, "https://www.googleapis.com/auth/cloud-platform\n", "text/plain", mh)
            if path == "/computeMetadata/v1/project/project-id":
                self._log("gce-md", {})
                return self._send(200, "proj-a", "text/plain", mh)
            if path == "/computeMetadata/v1/project/numeric-project-id":
                self._log("gce-md", {})
                return self._send(200, "123456789", "text/plain", mh)
            if path == "/computeMetadata/v1/universe/universe-domain":
                self._log("gce-md", {})
                return self._send(200, "googleapis.com", "text/plain", mh)
            self._log("gce-md-404", {})
            return self._send(404, "not found", "text/plain", mh)

        # ---------- Azure managed identity (App Service style) ----------
        if path == "/msi/token":
            hdr = self.headers.get("X-IDENTITY-HEADER")
            ok = hdr == MSI_SECRET
            self._log("az-msi", {"query": q, "header_ok": ok})
            if not ok:
                return self._send(401, {"error": "bad identity header"})
            c = int(time.time())
            return self._send(200, {"access_token": "az-msi-at-%d" % c, "expires_on": str(c + EXP),
                                    "resource": q.get("resource", [""])[0], "token_type": "Bearer",
                                    "client_id": q.get("client_id", ["00000000-0000-0000-0000-00000000aaaa"])[0]})

        # ---------- Azure (Entra) authority ----------
        if path.endswith("/discovery/instance"):
            self._log("az-instance-discovery", {"query": q})
            ae = q.get("authorization_endpoint", ["https://login.azure.test/t/oauth2/v2.0/authorize"])[0]
            ten = ae.split("/")[3]
            return self._send(200, {"tenant_discovery_endpoint": "https://login.azure.test/%s/v2.0/.well-known/openid-configuration" % ten,
                                    "api-version": "1.1", "metadata": [{"preferred_network": "login.azure.test",
                                    "preferred_cache": "login.azure.test", "aliases": ["login.azure.test"]}]})
        if path.endswith("/.well-known/openid-configuration"):
            ten = path.split("/")[1]
            self._log("az-oidc-discovery", {"tenant": ten})
            b = "https://login.azure.test/" + ten
            return self._send(200, {"token_endpoint": b + "/oauth2/v2.0/token",
                                    "authorization_endpoint": b + "/oauth2/v2.0/authorize",
                                    "device_authorization_endpoint": b + "/oauth2/v2.0/devicecode",
                                    "end_session_endpoint": b + "/oauth2/v2.0/logout",
                                    "issuer": b + "/v2.0", "jwks_uri": b + "/discovery/v2.0/keys",
                                    "response_types_supported": ["code", "id_token", "code id_token", "id_token token"],
                                    "subject_types_supported": ["pairwise"],
                                    "id_token_signing_alg_values_supported": ["RS256"],
                                    "token_endpoint_auth_methods_supported": ["client_secret_post", "private_key_jwt"],
                                    "scopes_supported": ["openid", "profile", "email", "offline_access"],
                                    "tenant_region_scope": "NA", "cloud_instance_name": "azure.test"})
        if path.endswith("/oauth2/v2.0/token"):
            ten = path.split("/")[1]
            tok = f1.get("client_assertion", "")
            t = tag(tok)
            self._log("az-token", {"tenant": ten, "client_id": f1.get("client_id"), "grant_type": f1.get("grant_type"),
                                   "scope": f1.get("scope"), "client_assertion_type": f1.get("client_assertion_type"),
                                   "token_tag": t, "token_claims": jwt_claims(tok)})
            return self._send(200, {"token_type": "Bearer", "expires_in": EXP, "ext_expires_in": EXP,
                                    "access_token": "az-at-%s-%s" % (ten[:8], t)})

        # ---------- fake Cloud Resource Manager (google-auth project-id lookup) ----------
        if path.startswith("/v1/projects/") and self.command == "GET":
            self._log("gcp-crm", {"authorization": self.headers.get("Authorization")})
            if os.environ.get("CRM_STATUS") == "403":
                return self._send(403, {"error": {"code": 403, "message": "Permission denied", "status": "PERMISSION_DENIED"}})
            return self._send(200, {"projectNumber": path.split("/")[3], "projectId": "proj-a"})

        # ---------- fake service APIs (log the bearer) ----------
        if path.startswith("/api/"):
            self._log("api", {"authorization": self.headers.get("Authorization")})
            return self._send(200, {"ok": True, "saw": self.headers.get("Authorization")})

        self._log("unhandled", {"body": body[:500]})
        return self._send(404, {"error": "unhandled", "path": self.path})


def serve(port, tls):
    s = ThreadingHTTPServer(("0.0.0.0", port), H)
    if tls:
        ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        ctx.load_cert_chain("/certs/server.pem", "/certs/server-key.pem")
        s.socket = ctx.wrap_socket(s.socket, server_side=True)
    s.serve_forever()


if __name__ == "__main__":
    for p, t in ((80, False), (8080, False), (443, True)):
        threading.Thread(target=serve, args=(p, t), daemon=True).start()
    print("fakesrv up EXP=%d" % EXP, flush=True)
    while True:
        time.sleep(3600)
