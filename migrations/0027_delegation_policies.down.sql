-- Reverses 0027_delegation_policies. Dropping the table cascades the
-- lenny_app grants.
DROP TABLE IF EXISTS delegation_policies;
