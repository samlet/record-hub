# Phase 4 Batch 1 fixtures and live gate

`../p4-batch1-spec.json` is the non-secret acceptance specification for Phase 3
closure. It fixes the identity overlap window, six independent client
directions, recovery assertions, mixed-owner load, rolling upgrade rules and
fault cases without embedding credentials or business payloads.

The executable entry point is:

```text
make p4-batch1
```

The gate always runs safe contract/static checks and writes a machine-readable
report below `build/evidence/phase4/batch1-<run-id>/`. Live checks are opt-in:

```text
RECORD_HUB_P4_BATCH1_LIVE=1 make p4-batch1
```

Live recovery and rollback still require explicit disposable source/restore
targets and immutable old/new artifacts. Missing live prerequisites are
recorded as `SKIPPED`; they are never converted into a PASS by a fake service,
an in-memory repository or a direct business-table mutation.
