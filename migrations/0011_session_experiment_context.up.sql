-- The §10.7 experiment context recorded on a session.
--
-- When the ExperimentRouter enrolls a session in an experiment at
-- creation, it records the experiment id, the assigned variant, and
-- whether the enrollment was inherited from a delegating parent. An
-- empty experiment_id means the session is not enrolled. The built-in
-- eval endpoint copies this context onto each EvalResult so the
-- results API can aggregate scores by variant.

ALTER TABLE sessions
    ADD COLUMN experiment_id         TEXT    NOT NULL DEFAULT '',
    ADD COLUMN experiment_variant_id TEXT    NOT NULL DEFAULT '',
    ADD COLUMN experiment_inherited  BOOLEAN NOT NULL DEFAULT false;
