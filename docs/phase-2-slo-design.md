# Query Cost and Projection SLO Baseline（P2-1-005）

状态：DONE（本地可重复基线；外部告警编排留给部署层）

## Query cost envelope

Record list queries remain workspace-authorized and use the existing field/operator
allowlist. The service now adds a second, explicit runtime budget:

| Budget | Default | Configuration |
| --- | ---: | --- |
| Maximum page rows | 100 | `RECORD_HUB_QUERY_MAX_PAGE_ROWS` |
| Maximum encoded page | 1 MiB | `RECORD_HUB_QUERY_MAX_RESPONSE_BYTES` |
| Maximum repository time | 2s | `RECORD_HUB_QUERY_MAX_DURATION` |

The existing view limits remain in force: 128 columns, 16 filters, 4 sorts and
32 values in one `in` filter. A query that exceeds the page, encoded response or
execution budget returns `QUERY_COST_EXCEEDED` and emits a bounded rejection
metric. No tenant, table, record, field, cursor or payload is used as a metric
label.

## Projection freshness baseline

The operations snapshot derives the latest checkpoint timestamp/version and
exposes them as `freshness` in the API. Default warning thresholds are:

| Signal | Default threshold | Configuration |
| --- | ---: | --- |
| Processing backlog | 100 events | `RECORD_HUB_PROJECTION_BACKLOG_WARNING` |
| Projected lag age | 30s | `RECORD_HUB_PROJECTION_LAG_WARNING` |
| Failure budget | 10 rejected/failed inbox items | `RECORD_HUB_PROJECTION_FAILURE_BUDGET` |

The metrics endpoint exposes, per low-cardinality consumer label:

- `record_hub_projection_backlog`
- `record_hub_projection_last_projected_timestamp_seconds`
- `record_hub_projection_last_projected_version`
- `record_hub_projection_lag_age_seconds`
- `record_hub_projection_error_budget_remaining`
- `record_hub_projection_slo_breach`
- handler duration, applied, failure, gap, redelivery and DLQ counters

`slo_breach=1` is the deployment-layer alert predicate. The in-process
registry intentionally does not send notifications; Prometheus/Alertmanager or
another local collector can alert on the stable metric names and threshold
gauges. Failure budget is an operational baseline over the current bounded
inbox view, not a claim of a rolling-window error-rate calculation.

## Acceptance

```bash
make check
go test ./server/internal/observability ./server/internal/config ./server/internal/modules/records ./server/internal/modules/projection -count=1
```
