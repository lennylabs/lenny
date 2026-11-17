#!/bin/bash
R=spike-owed-b; cd $R; . ./kc.sh pki
echo "### sub-hour duration, as the cert-manager webhook sees it"
cat <<'EOF' > k8s/shortdur.yaml
apiVersion: cert-manager.io/v1
kind: Certificate
metadata: {name: short, namespace: lenny-system}
spec: {secretName: short, duration: 10m, renewBefore: 9m, dnsNames: [x.y], issuerRef: {name: lenny-egress-ca, kind: Issuer}}
EOF
$K apply -f k8s/pki.yaml; $K -n lenny-system wait certificate/lenny-egress-ca --for=condition=Ready --timeout=60s
$K apply --dry-run=server -f k8s/shortdur.yaml 2>&1 | cut -c1-300
$K apply -f k8s/leaves.yaml; $K -n lenny-system wait certificate --all --for=condition=Ready --timeout=60s
$K -n lenny-system get certificate -o 'custom-columns=N:.metadata.name,NB:.status.notBefore,NA:.status.notAfter,RT:.status.renewalTime,REV:.status.revision'
$K -n lenny-system get issuer
$K -n lenny-system get secret lenny-egress-tls -o jsonpath='{.type}{"\n"}'; $K -n lenny-system get secret lenny-egress-tls -o jsonpath='{.data.tls\.crt}' | base64 -d | openssl x509 -noout -subject -issuer -dates -ext subjectAltName,extendedKeyUsage,keyUsage
