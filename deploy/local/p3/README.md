# Phase 3 local fixtures

The three files are single-tenant/workspace exact policies for the controlled
owner commands. Convert one or more `policies` arrays to the
`RECORD_HUB_COMMAND_POLICIES` environment variable when starting Record Hub;
the fixtures are not loaded automatically and do not contain secrets. This is
deliberately default-off: an operator must explicitly choose the tenant and
workspace before enabling a policy.

The matching Fluxion worker must set:

```text
FLUXION_RECORD_HUB_TENANT_ID=tenant-p3-fluxion
FLUXION_RECORD_HUB_WORKSPACE_ID=workspace-p3-fluxion
```

The workload issuer, subject, audience and scope must match exactly. Any blank
tenant/workspace mapping makes the owner reject the command. The owner-side
adapters are note-only APPEND/VOID commands and do not mutate workflow
decisions.
