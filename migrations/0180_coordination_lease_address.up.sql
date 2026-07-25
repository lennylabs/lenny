-- §4.6.1 individual agent-pod eviction drive — coordinator_address.
--
-- coordinator_replica records the coordinating replica's OTel
-- service.instance.id (migration 0164), which identifies the coordinator
-- but is not itself a dialable network address. The §4.6.1 eviction drive
-- forwards an eviction-checkpoint request from the replica that consumes a
-- pod's control stream to the session's recorded coordinator over the
-- gateway inter-replica control-forward path, so the mirror must also
-- carry the coordinator's dialable inter-replica address (POD_IP plus the
-- inter-replica gRPC port). The by-session mirror read returns both, so a
-- replica that must forward has a routable target rather than only an
-- identity.
--
-- Nullable: a pre-backfill row and a row written by a mirror writer that
-- does not yet record an address read NULL. The by-session read collapses
-- a NULL to the empty string, which resolves no forward target.
--
-- spec: §10.1 (coordination_lease mirror), §4.6.1 (eviction drive).
ALTER TABLE coordination_lease
    ADD COLUMN IF NOT EXISTS coordinator_address TEXT NULL;
