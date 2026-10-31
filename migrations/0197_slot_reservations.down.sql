-- Reverse 0197: drop the slot reservation record.
-- spec: §12.4 (Redis failure behavior), §5.2 (Slot assignment atomicity).
DROP TABLE IF EXISTS slot_reservations;
