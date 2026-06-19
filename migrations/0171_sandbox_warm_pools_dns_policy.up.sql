-- §13.2 per-pool DNS opt-out for sandbox_warm_pools, alongside
-- egress_profile (migration 0079).
--
-- dns_policy is the §13.2 per-pool DNS resolver selection the warm pool
-- controller honors when stamping agent pods. Empty (the default) routes
-- the pool's pods through the dedicated lenny-system CoreDNS instance,
-- where the controller stamps dnsPolicy:None plus a dnsConfig targeting
-- the dedicated Service. The only other accepted value is
-- 'cluster-default': the pool's pods revert to the Kubernetes default
-- ClusterFirst resolver (kube-system CoreDNS), and the controller stamps
-- the lenny.dev/dns-policy: cluster-default label so the supplemental
-- allow-pod-egress-kube-dns NetworkPolicy admits their kube-system DNS
-- egress. Opting out removes the dedicated instance's query logging,
-- rate limiting, and response filtering, and the §13.2 cross-control
-- (enforced in poolstore.ValidateDNSPolicy) permits it only for
-- 'standard' (runc) pools. Defaults to '': a pool that never opts out
-- keeps the dedicated CoreDNS instance.
ALTER TABLE sandbox_warm_pools
    ADD COLUMN dns_policy TEXT NOT NULL DEFAULT '';
