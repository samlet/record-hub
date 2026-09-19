# P3 deterministic fixture set

`scripts/bootstrap-p3-fixtures.sh` materializes this batch's topology and
owner-policy inputs into a disposable `.runtime/p3/fixtures` directory (or
`RECORD_HUB_P3_FIXTURE_DIR`). The generated files contain no secrets and have
stable content and hashes; timestamps and random IDs are intentionally absent.

The script does not write business tables directly. The topology supervisor
uses these files to configure migrations, exact tenant/workspace policy, and
the workload issuer before the real APIs/workers are started.
