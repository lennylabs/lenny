-- Reverse 0174: drop the fixed "default"-tenant billing and audit
-- sequences. Both are owned by the migration role (created directly,
-- not through the FOR ROLE lenny_ddl default privilege), so a plain
-- DROP SEQUENCE reverses them; no role or grant machinery to unwind
-- beyond that, since 0173 already owns the lenny_ddl role lifecycle.
DROP SEQUENCE IF EXISTS billing_seq_37a8eec1ce19687d132fe29051dca629d164e2c4;
DROP SEQUENCE IF EXISTS audit_seq_37a8eec1ce19687d132fe29051dca629d164e2c4;
