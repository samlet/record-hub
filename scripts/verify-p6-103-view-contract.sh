#!/usr/bin/env bash
set -Eeuo pipefail

# P6-103: validate bounded view/query/export policy. This static gate does not
# execute queries, create indexes, or export records.
root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
spec="$root_dir/deploy/local/p6/p6-103-view-contract-spec.json"
output="${RECORD_HUB_P6_VIEW_CONTRACT_OUTPUT:-$root_dir/docs/phase-6-views-contract.json}"

for command in date git jq rg python3; do
  command -v "$command" >/dev/null || { echo "P6-103 missing command: $command" >&2; exit 2; }
done

jq -e '
  .version == 1 and .phase == "6" and .task == "P6-103" and
  .liveTraffic == false and .failClosed == true and
  .view.unknownFieldAction == "REJECT" and
  .view.unindexedQueryAction == "REJECT" and
  .pagination.stableOrderRequired == true and
  .cost.overBudgetAction == "REJECT" and
  .export.redaction == "DROP_AND_AUDIT"
' "$spec" >/dev/null

python3 - "$spec" <<'PY'
import json
import pathlib
import re
import sys

spec = json.loads(pathlib.Path(sys.argv[1]).read_text())
view = spec["view"]
if not re.fullmatch(view["idPattern"], "records-by-status"):
    raise SystemExit("P6-103 view id pattern is unusable")
if not {"eq", "in", "exists", "gte", "lte"}.issubset(set(view["filterOperators"])):
    raise SystemExit("P6-103 filter operator set is incomplete")
if set(view["sortDirections"]) != {"asc", "desc"} or view["stableTieBreaker"] != "_id":
    raise SystemExit("P6-103 sort policy is incomplete")
page = spec["pagination"]
if page["defaultPageSize"] < 1 or page["defaultPageSize"] > page["maxPageSize"] or page["maxPageSize"] > 100:
    raise SystemExit("P6-103 page bounds are invalid")
if page["cursor"] != "opaque-signed" or page["cursorScope"] != "tenant-workspace-table-view":
    raise SystemExit("P6-103 cursor policy is not scoped")
cost = spec["cost"]
if min(cost["maxEstimatedUnits"], cost["maxExecutionMillis"], cost["maxRowsScanned"], cost["maxConcurrentQueriesPerWorkspace"]) <= 0:
    raise SystemExit("P6-103 cost limits must be positive")
rate = spec["rateLimit"]
if rate["scope"] != "tenant-workspace" or rate["backpressureStatus"] != 429 or rate["retryAfterSeconds"] <= 0:
    raise SystemExit("P6-103 rate/backpressure policy is invalid")
export = spec["export"]
if not {"jsonl", "csv"}.issubset(set(export["formats"])):
    raise SystemExit("P6-103 export formats are incomplete")
for field in ("rawPayload", "workflowInput", "accessToken", "privateKey", "secret", "sealedBid", "quote", "bankAccount"):
    if field not in export["forbiddenFields"]:
        raise SystemExit(f"P6-103 export field is not forbidden: {field}")
required_audit = {"exportId", "actor", "scope", "viewId", "fieldSet", "rowCount", "redactedFields", "occurredAt"}
if not required_audit.issubset(set(export["requiredAuditFields"])):
    raise SystemExit("P6-103 export audit fields are incomplete")
PY

rg -q 'bounded view query|CursorQuery|LimitQuery|RecordPage' "$root_dir/api/openapi.yaml" "$root_dir/server/internal/modules/records"
rg -q 'rateLimit|RateLimiter|RATE_LIMITED|Retry-After' "$root_dir/server/internal/observability" "$root_dir/server/internal/app" "$root_dir/server/internal/web"
rg -q 'redact|sensitive|PII|safe export' "$root_dir/docs/phase-6-requirements.md" "$root_dir/docs/phase-6-acceptance-plan.md" "$root_dir/server/internal"

mkdir -p "$(dirname "$output")"
jq -S -n \
  --arg generatedAt "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  --arg sourceCommit "$(git -C "$root_dir" rev-parse HEAD)" \
  --argjson view "$(jq -c '.view' "$spec")" \
  --argjson pagination "$(jq -c '.pagination' "$spec")" \
  --argjson cost "$(jq -c '.cost' "$spec")" \
  --argjson rateLimit "$(jq -c '.rateLimit' "$spec")" \
  --argjson export "$(jq -c '.export' "$spec")" \
  '{schemaVersion:1,phase:"6",task:"P6-103",status:"PASS",generatedAt:$generatedAt,liveTraffic:false,sourceCommit:$sourceCommit,view:$view,pagination:$pagination,cost:$cost,rateLimit:$rateLimit,export:$export,prerequisite:{phase5:"INDEPENDENT_GATE",connectorEnablement:"NOT_GRANTED"},next:"Allow only bounded, signed-cursor views; reject over-budget/unindexed queries and audit all redacted exports."}' \
  >"$output"
cat "$output"
