#!/bin/bash
# Runs inside the Firefox container. ff.sh MODE: policy, nss, env, none
MODE=$1; shift
P=/tmp/prof-$MODE; rm -rf $P; mkdir -p $P
cat > $P/user.js <<U
user_pref("browser.shell.checkDefaultBrowser", false);
user_pref("datareporting.policy.dataSubmissionEnabled", false);
user_pref("app.update.enabled", false);
user_pref("network.captive-portal-service.enabled", false);
user_pref("network.connectivity-service.enabled", false);
user_pref("security.OCSP.enabled", 0);
user_pref("browser.safebrowsing.malware.enabled", false);
user_pref("browser.safebrowsing.phishing.enabled", false);
U
if [ -n "$PROXYPREF" ]; then cat >> $P/user.js <<U
user_pref("network.proxy.type", 1);
user_pref("network.proxy.ssl", "127.0.0.1");
user_pref("network.proxy.ssl_port", 3128);
user_pref("network.proxy.http", "127.0.0.1");
user_pref("network.proxy.http_port", 3128);
user_pref("network.proxy.allow_hijacking_localhost", true);
U
fi
rm -f /etc/firefox-esr/policies/policies.json /usr/lib/firefox-esr/distribution/policies.json /etc/firefox/policies/policies.json
case "$MODE" in
 policy) mkdir -p /etc/firefox/policies; python3 - "$@" > /etc/firefox/policies/policies.json <<'PY'
import json,os
print(json.dumps({"policies":{"Certificates":{"Install":os.environ["CAS"].split(":")}}}))
PY
 cat /etc/firefox/policies/policies.json >&2 ;;
 nss) certutil -N -d sql:$P --empty-password; IFS=: read -ra A <<<"$CAS"; for c in "${A[@]}"; do certutil -A -d sql:$P -n "$(basename $c)" -t "C,," -i "$c"; done; certutil -L -d sql:$P ;;
esac
exec python3 /i3/mar.py $P "$@"
