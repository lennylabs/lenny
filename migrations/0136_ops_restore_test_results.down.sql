-- Reverse 0136_ops_restore_test_results: drop the §25.11 test-restore
-- result record. The lenny-restore-test CronJob stops recording outcomes
-- and the lenny-ops restore-test gauges go unset after this table is
-- removed.

DROP TABLE IF EXISTS ops_restore_test_results;
