# P4 deterministic fixtures

`scripts/bootstrap-p4-fixtures.sh` materializes the Phase 4 topology, tenant,
approval-slice, recovery-target and release-window fixtures into a disposable
directory (default `.runtime/p4/fixtures`).

The source spec is stable and contains no secrets, timestamps, random IDs or
business payloads. Generated `manifest.json` records normalized file hashes.
The fixtures configure feature flags OFF by default so a Beta run cannot enable
external approvals accidentally.
