# fantasy-baseball (fb)

`fb` is a local-first CLI for ESPN fantasy baseball pitcher operations.

It keeps your workflow in SQLite, uses deterministic rules, and gates write actions behind explicit review, preflight, confirmation, verification, and audit trails.

## v1 Scope

### Included
- Forecaster probable-start ingestion + normalization
- ESPN roster/league sync (read + bounded candidate ingestion)
- Pitcher analysis, planning, and pickup recommendations
- Transaction planning and approval workflow
- Ad hoc pitcher add/drop requests
- Single-item transaction execution with hard preflight + verification
- Pitcher lineup planning/approval/preflight/single-item execution
- Monitoring for stale/blocked/invalidated artifacts
- Operator diagnostics via `fb doctor`

### Write capabilities in v1
- Real writes are limited to **single-item** ESPN mutations:
  - transaction add/drop via `fb transactions run` / `fb transactions run-ad-hoc`
  - pitcher lineup slot move via `fb lineup pitchers run`
- Planning/recommendation/monitoring commands remain non-mutating.
- ESPN ingestion commands (`fb espn ...`) are read-only data pulls.

### Intentionally out of scope
- Hitters
- Batch/unattended execution
- Waiver/FAAB automation
- Browser automation
- Web dashboard
- LLM-generated strategy decisions

## Install and Build

```bash
go build -o fb ./cmd/fb
./fb init
./fb doctor
```

## Config

Default config location: `~/.fantasy-baseball/config.json`

Use the example as baseline:

```bash
cp config.json.example ~/.fantasy-baseball/config.json
```

Required for live ESPN usage:
- `league.league_id`
- `league.team_id`
- `league.season`
- `auth.espn_s2_env`
- `auth.swid_env`

Export cookie env vars:

```bash
export ESPN_S2="..."
export ESPN_SWID="{...}"
```

Validate setup:

```bash
./fb espn validate
./fb doctor
```

## Core Command Groups

- `fb forecaster` - probable-start source ingestion and inspection
- `fb espn` - roster/league/candidate ingestion and source inspection
- `fb pitchers` - analysis and weekly pitcher plan
- `fb pickups` - free-agent pickup recommendations
- `fb transactions` - transaction planning, review, ad hoc creation, and execution
- `fb lineup pitchers` - lineup plan/review/preflight/execution
- `fb monitor` - stale/blocked/invalidated artifact visibility
- `fb doctor` - operator readiness checks

## Recommended Weekly Routine

1. Refresh inputs
```bash
./fb espn sync roster
./fb forecaster import --url
./fb espn free-agents pitchers --limit 25
```

2. Build decision artifacts
```bash
./fb pitchers plan
./fb pickups recommend
./fb transactions plan --top 10
./fb lineup pitchers plan
```

3. Review + approve
```bash
./fb transactions review --plan-id <id>
./fb transactions approve --plan-id <id> --item <id> --note "weekly gain"

./fb lineup pitchers review --plan-id <id>
./fb lineup pitchers approve --plan-id <id> --item <id>
```

4. Preflight and execute one item at a time
```bash
./fb transactions preflight
./fb transactions run --item <approved_item_id>
./fb transactions run --item <approved_item_id> --confirm

./fb lineup pitchers preflight
./fb lineup pitchers run --item <approved_lineup_item_id> --confirm
```

5. Verify and monitor
```bash
./fb transactions execution-pending
./fb transactions execution-verify --execution-id <id>
./fb transactions execution-reconcile --execution-id <id>

./fb monitor summary
./fb monitor approvals
./fb monitor execution
```

## End-to-End Example (Copy/Paste)

```bash
./fb init
./fb doctor

./fb espn validate
./fb espn sync roster
./fb forecaster import --url
./fb espn free-agents pitchers --limit 25

./fb pitchers plan --from 2026-09-15 --to 2026-09-24
./fb pickups recommend --from 2026-09-15 --to 2026-09-24
./fb transactions plan --from 2026-09-15 --to 2026-09-24 --top 10

./fb transactions review --plan-id 1
./fb transactions approve --plan-id 1 --item 3 --note "best weekly delta"
./fb transactions queue

./fb transactions preflight --item 3
./fb transactions run --item 3
./fb transactions run --item 3 --confirm
./fb transactions execution-history --execution-id 1

./fb lineup pitchers plan
./fb lineup pitchers review --plan-id 1
./fb lineup pitchers approve --plan-id 1 --item 2
./fb lineup pitchers run --item 2 --confirm

./fb monitor summary
./fb doctor
```

## Ad Hoc Workflow

Use when you already know the move.

```bash
./fb transactions ad-hoc --add "Aaron Nola" --drop "Shota Imanaga"
./fb transactions ad-hoc-list --request-id 4
./fb transactions run-ad-hoc --request-id 4
./fb transactions run-ad-hoc --request-id 4 --confirm
```

Ad hoc still uses the same guardrails:
- identity resolution
- immediate preflight gate
- explicit confirmation
- single-item execution
- verification + audit

## Monitoring vs Preflight

- **Preflight** (`fb transactions preflight`, `fb lineup pitchers preflight`) checks current executability right now for queued items.
- **Monitoring** (`fb monitor ...`) checks whether saved artifacts/approvals/execution outcomes have gone stale, blocked, or invalid over time.

Use both.

## Status Vocabulary

### Review state
- `pending`, `approved`, `rejected`, `deferred`

### Preflight status
- `executable`, `blocked`, `stale`, `conflict`, `unknown`

### Execution status
- `started`, `submitted`, `succeeded`, `failed`, `aborted`, `ambiguous`

### Verification status
- `verified`, `verification_pending`, `unverified`, `verification_failed`, `unknown`

### Monitoring status
- `fresh`, `stale`, `blocked`, `invalidated`, `unknown`

## Safety Model

- No batch execution
- One item per command
- Confirmation required for real writes
- Immediate preflight rerun before write
- Duplicate protection blocks re-execution after successful/verified attempts
- Verification stored separately from write submission
- Ambiguity is explicit (not silently treated as success)

## Troubleshooting

### “No rows found” or unmatched players
- Confirm date window has probable starts:
```bash
./fb forecaster list --from YYYY-MM-DD --to YYYY-MM-DD --include-tbd
```
- Re-import forecaster source and rerun planning.

### Execution blocked
- Check queue/preflight details:
```bash
./fb transactions execution-queue
./fb transactions preflight --item <id>
```
- Re-run planning/approval if add is unavailable or drop is no longer rostered.

### Unresolved execution attempts
```bash
./fb transactions execution-pending
./fb transactions execution-verify --execution-id <id>
./fb transactions execution-reconcile --execution-id <id>
```

### Overall readiness
```bash
./fb doctor
./fb monitor summary
```

## JSON Output

Most commands support `--json` for scripting/OpenClaw usage.

Examples:

```bash
./fb doctor --json
./fb monitor summary --json
./fb transactions execution-history --execution-id 8 --json
./fb lineup pitchers queue --json
```

## Backward Compatibility Notes

- `fb healthcheck` is retained as a basic legacy check command.
- `fb doctor` is the preferred v1 operator readiness command.
- Existing workflow focuses on `fb transactions run` and `fb lineup pitchers run` for real writes.
