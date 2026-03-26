# fantasy-baseball (fb)

`fb` is a local-first CLI for ESPN fantasy baseball pitcher operations.

It keeps your workflow in SQLite, uses deterministic rules, and gates write actions behind preflight, confirmation, verification, and audit trails.

## v1 Scope

### Included
- Forecaster probable-start ingestion + normalization
- ESPN roster/league sync (read + bounded candidate ingestion)
- Pitcher analysis, planning, and pickup recommendations
- Transaction planning
- Single-item transaction execution with hard preflight + verification
- Pitcher lineup planning + single-item lineup execution
- Monitoring for stale/blocked/invalidated artifacts
- Operator diagnostics via `fb doctor`

### Write capabilities in v1
- Real writes are limited to **single-item** ESPN mutations:
  - transaction add/drop via `fb execute transaction`
  - pitcher lineup slot move via `fb execute lineup`
- Planning/recommendation commands remain non-mutating.
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
- `fb transactions` - transaction planning and analysis
- `fb lineup pitchers` - lineup planning and analysis
- `fb execute` - transaction/lineup execution operations
- `fb doctor` - operator readiness checks

## Recommended Weekly Routine

1. Refresh inputs
```bash
./fb espn sync roster
./fb forecaster sync --url
./fb espn free-agents pitchers --limit 25
```

2. Build decision artifacts
```bash
./fb pitchers plan
./fb pickups plan
./fb transactions plan --top 10
./fb lineup pitchers plan
```

3. Build planning artifacts
```bash
./fb transactions plan --top 10
./fb lineup pitchers plan
```

4. Execute one item at a time (direct ops path)
```bash
./fb execute transaction --add "Aaron Nola" --drop "Shota Imanaga"
./fb execute transaction --add "Aaron Nola" --drop "Shota Imanaga" --confirm

./fb execute lineup --player "Kris Bubic" --to-slot P
./fb execute lineup --player "Kris Bubic" --to-slot P --confirm
```

5. Verify and inspect
```bash
./fb execute pending
./fb execute verify --execution-id <id>
./fb execute reconcile --execution-id <id>
```

## End-to-End Example (Copy/Paste)

```bash
./fb init
./fb doctor

./fb espn validate
./fb espn sync roster
./fb forecaster sync --url
./fb espn free-agents pitchers --limit 25

./fb pitchers plan --from 2026-09-15 --to 2026-09-24
./fb pickups plan --from 2026-09-15 --to 2026-09-24
./fb transactions plan --from 2026-09-15 --to 2026-09-24 --top 10
./fb lineup pitchers plan

./fb execute transaction --add "Aaron Nola" --drop "Shota Imanaga"
./fb execute transaction --add "Aaron Nola" --drop "Shota Imanaga" --confirm
./fb execute history --execution-id 1

./fb execute lineup --player "Kris Bubic" --to-slot P
./fb execute lineup --player "Kris Bubic" --to-slot P --confirm

./fb doctor
```

## Direct Ops Workflow

```bash
./fb execute transaction --add "Aaron Nola" --drop "Shota Imanaga"
./fb execute transaction --add "Aaron Nola" --drop "Shota Imanaga" --confirm
./fb execute transaction --add "Roki Sasaki" --next-day --confirm
```

Direct execution still uses the same guardrails:
- identity resolution
- immediate preflight gate
- explicit confirmation
- single-item execution
- verification + audit

## Direct Lineup Ops

```bash
./fb execute lineup --player "Kris Bubic" --to-slot P
./fb execute lineup --player "Kris Bubic" --to-slot P --confirm
```

## Preflight

Preflight is run automatically inside `fb execute transaction` and `fb execute lineup` right before write.

## Useful View Modes

Several commands provide focused views without needing separate subcommands:

```bash
./fb pitchers plan --view start-sit
./fb pickups plan
./fb transactions plan
```

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
./fb forecaster show starts --from YYYY-MM-DD --to YYYY-MM-DD --include-tbd
```
- Re-import forecaster source and rerun planning.

### Execution blocked
- Check execution details:
```bash
./fb execute history --execution-id <id>
./fb execute resolve --execution-id <id>
```
- Re-run execute command after refreshing source data if add is unavailable or drop is no longer rostered.

### Unresolved execution attempts
```bash
./fb execute pending
./fb execute verify --execution-id <id>
./fb execute reconcile --execution-id <id>
```

### Overall readiness
```bash
./fb doctor
```

## JSON Output

Most commands support `--json` for scripting/OpenClaw usage.

Examples:

```bash
./fb doctor --json
./fb execute history --execution-id 8 --json
./fb execute transaction --add "Aaron Nola" --drop "Shota Imanaga" --json
```

## Operator Notes

- `fb doctor` is the main readiness command.
- `fb healthcheck` remains a minimal config/db check.
- Real writes are explicit via `fb execute transaction` and `fb execute lineup`.
