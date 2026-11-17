import sys
T="lenny-egress.lenny-system.svc:3128"; TG="target.lenny-system.svc.cluster.local:7000"
for i in range(1,4):
    print(f"""---
apiVersion: v1
kind: Pod
metadata: {{name: c{i}, namespace: agents, labels: {{app: client}}}}
spec:
  containers:
  - {{name: probe, image: ob-tools:1, imagePullPolicy: Never, command: [ob, tlsprobe, -addr, "{T}", -connect, "{TG}", -ca, /ca/ca.crt, -rate, "5"], volumeMounts: [{{name: ca, mountPath: /ca, readOnly: true}}]}}
  - {{name: wca, image: ob-tools:1, imagePullPolicy: Never, command: [ob, watchfile, /ca/ca.crt], volumeMounts: [{{name: ca, mountPath: /ca, readOnly: true}}]}}
  volumes: [{{name: ca, configMap: {{name: lenny-egress-ca}}}}]""")
for i in range(1,6):
    print(f"""---
apiVersion: v1
kind: Pod
metadata: {{name: p{i}, namespace: agents, labels: {{app: prop}}}}
spec:
  containers:
  - {{name: w, image: ob-tools:1, imagePullPolicy: Never, command: [ob, watchfile, /m/v], volumeMounts: [{{name: m, mountPath: /m, readOnly: true}}]}}
  volumes: [{{name: m, configMap: {{name: ob-prop}}}}]""")
print("""---
apiVersion: v1
kind: Pod
metadata: {name: psub, namespace: agents, labels: {app: prop}}
spec:
  containers:
  - {name: w, image: ob-tools:1, imagePullPolicy: Never, command: [ob, watchfile, /m/v], volumeMounts: [{name: m, mountPath: /m/v, subPath: v, readOnly: true}]}
  volumes: [{name: m, configMap: {name: ob-prop}}]
---
apiVersion: v1
kind: Pod
metadata: {name: soak, namespace: agents}
spec:
  restartPolicy: Never
  containers:
  - {name: s, image: ob-tools:1, imagePullPolicy: Never, command: [soak, -proxy, "%s", -target, "%s", -cred, "agent:good", -n, "200", -rate, "100", -max, "480s", -report, "60s", -probe, "1s", -proxy-tls, -proxy-ca, /ca/ca.crt], volumeMounts: [{name: ca, mountPath: /ca, readOnly: true}]}
  volumes: [{name: ca, configMap: {name: lenny-egress-ca}}]
---
apiVersion: v1
kind: Pod
metadata: {name: writer, namespace: lenny-system}
spec:
  serviceAccountName: ob
  restartPolicy: Never
  containers:
  - {name: w, image: ob-tools:1, imagePullPolicy: Never, command: [ob, writecm, agents, ob-prop, v, "24", 25s, 55s]}
""" % (T,TG))
