# Phase 3 local fixtures

`fluxion-project-annotate-policy.json` is the single-tenant/workspace exact
policy for the first controlled command. Convert its `policies` array to the
`RECORD_HUB_COMMAND_POLICIES` environment variable when starting Record Hub;
the fixture is not loaded automatically and does not contain a secret.

The matching Fluxion worker must set:

```text
FLUXION_RECORD_HUB_TENANT_ID=tenant-p3-fluxion
FLUXION_RECORD_HUB_WORKSPACE_ID=workspace-p3-fluxion
```

The workload issuer, subject, audience and scope must match exactly. Any blank
tenant/workspace mapping makes the Fluxion owner reject the command.
