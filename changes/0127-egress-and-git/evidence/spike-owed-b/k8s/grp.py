import sys
cl,w=sys.argv[1],sys.argv[2]  # control-plane node, worker node
N=int(sys.argv[3])
print("""apiVersion: v1
kind: Namespace
metadata: {name: lenny-system}
---
apiVersion: v1
kind: Namespace
metadata: {name: agents}
---
apiVersion: v1
kind: ServiceAccount
metadata: {name: ob, namespace: lenny-system}
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata: {name: ob-admin}
roleRef: {apiGroup: rbac.authorization.k8s.io, kind: ClusterRole, name: cluster-admin}
subjects: [{kind: ServiceAccount, name: ob, namespace: lenny-system}]
---
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata: {name: default-deny-egress, namespace: agents}
spec: {podSelector: {}, policyTypes: [Egress]}
---
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata: {name: egress-profile-proxied, namespace: agents}
spec:
  podSelector: {}
  policyTypes: [Egress]
  egress:
  - to:
    - namespaceSelector: {matchLabels: {kubernetes.io/metadata.name: lenny-system}}
      podSelector: {matchLabels: {app: lenny-egress}}
    ports: [{protocol: TCP, port: 3128}]
---
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata: {name: egress-group-group-a, namespace: lenny-system}
spec:
  podSelector: {matchLabels: {group: group-a}}
  policyTypes: [Ingress]
  ingress:
  - from:
    - namespaceSelector: {matchLabels: {kubernetes.io/metadata.name: agents}}
      podSelector: {matchLabels: {lenny.dev/egress-group: group-a}}
    ports: [{protocol: TCP, port: 3128}]
---
apiVersion: v1
kind: Pod
metadata: {name: stamper, namespace: lenny-system}
spec:
  serviceAccountName: ob
  nodeName: %s
  tolerations: [{operator: Exists}]
  containers: [{name: s, image: ob-tools:4, imagePullPolicy: Never, command: [ob, sleep]}]""" % cl)
def srv(name,node,labels):
    print(f"""---
apiVersion: v1
kind: Pod
metadata: {{name: {name}, namespace: lenny-system, labels: {labels}}}
spec:
  nodeName: {node}
  tolerations: [{{operator: Exists}}]
  containers: [{{name: s, image: ob-tools:4, imagePullPolicy: Never, command: [ob, serve, ":3128"]}}]""")
srv("ga-cross",cl,"{app: lenny-egress, group: group-a}")
srv("ga-same",w,"{app: lenny-egress, group: group-a}")
srv("default-envoy",w,"{app: lenny-egress}")
def cli(name,labels="{app: client}"):
    print(f"""---
apiVersion: v1
kind: Pod
metadata: {{name: {name}, namespace: agents, labels: {labels}}}
spec:
  nodeName: {w}
  containers: [{{name: c, image: ob-tools:4, imagePullPolicy: Never, command: [ob, sleep]}}]""")
for i in range(N): cli(f"x{i}")
for i in range(N): cli(f"s{i}")
for i in range(12): cli(f"b{i}")
cli("wrong","{app: client, lenny.dev/egress-group: group-b}")
cli("unset")
