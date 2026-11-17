#!/bin/bash
# Does an Issuer switch re-issue a leaf that is outside its renewal window? Also a mistaken single-Certificate rekey.
. spike-owed-b/s2-lib.sh
cat <<'EOF' | $K apply -f -
apiVersion: cert-manager.io/v1
kind: Certificate
metadata: {name: longleaf, namespace: lenny-system}
spec:
  secretName: longleaf
  duration: 24h
  renewBefore: 8h
  dnsNames: [longleaf.lenny-system.svc]
  usages: [server auth, client auth]
  privateKey: {algorithm: ECDSA, size: 256}
  issuerRef: {name: lenny-egress-ca, kind: Issuer}
EOF
$N wait certificate/longleaf --for=condition=Ready --timeout=60s
echo "before: leaf issuer $(issuer_of longleaf), renewalTime $($N get certificate longleaf -o jsonpath='{.status.renewalTime}'), revision $($N get certificate longleaf -o jsonpath='{.status.revision}')"
new_ca lenny-egress-ca-5; switch_issuer lenny-egress-ca-5; mark "issuer switched"
sleep 150
echo "150 s after the switch: leaf issuer $(issuer_of longleaf), revision $($N get certificate longleaf -o jsonpath='{.status.revision}'), Ready=$($N get certificate longleaf -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}')"
$N get certificate longleaf -o jsonpath='{.status.conditions}'; echo
cmctl --kubeconfig $R/kc-pki renew longleaf -n lenny-system
sleep 5
echo "after cmctl renew: leaf issuer $(issuer_of longleaf), revision $($N get certificate longleaf -o jsonpath='{.status.revision}')"
echo "ca.crt in the leaf Secret: $($N get secret longleaf -o jsonpath='{.data.ca\.crt}' | base64 -d | openssl x509 -noout -subject)"
