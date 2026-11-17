# Target label, then URL. A label holds what the target tests.
URLS="https://allowed.oa.test/ https://denied.oa.test/ https://sub.allowed.oa.test/ https://viaint.oa.test/ https://198.19.50.20/ https://ctl.oa.test/ https://unk.oa.test/ https://xallowed.oa.test/ https://xdenied.oa.test/ https://198.19.50.20:8443/"
cli() { # cli NAME URL CMD...: records exit code and the most specific error line.
  name=$1; u=$2; shift 2
  out=$("$@" 2>&1); rc=$?
  if [ $rc = 0 ]; then echo "$name $u OK rc=0"; else echo "$name $u REFUSED rc=$rc $(printf '%s' "$out" | grep -iE 'certif|ssl|tls|constraint|verif|handshake|error' | head -2 | tr '\n' ' ' | cut -c1-200)"; fi
}
