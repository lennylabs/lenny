## 13.2 Network isolation

The base policy `allow-pod-egress-base` allows only the gRPC LNK-GWCONTROL
(port 50051) and DNS. Port 8443 is added by a supplemental policy and applies
only to pods whose pool selects it.
