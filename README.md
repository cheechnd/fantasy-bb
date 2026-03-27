# fantasy-baseball (`fb`)

`fb` is a local-first CLI for ESPN fantasy baseball pitcher workflows.

It keeps decision artifacts and execution history in SQLite, and gates real writes behind:
- deterministic resolution
- immediate preflight
- explicit confirmation
- post-write verification
- auditable execution events

## v1 Scope

### Included
- Forecaster probable-start ingestion and normalization
- ESPN roster + free-agent snapshot sync
- Pitcher planning
- Pickup planning
- Transaction planning
- Lineup planning
- Direct single-item transaction execution (`add/drop` and `add-only`)
- Direct single-item lineup slot execution
- Execution follow-up (`pending`, `verify`, `reconcile`, `resolve`)
- Multi-team context switching
- Operator diagnostics (`fb doctor`)

### Intentionally Out Of Scope
- Hitters
- Batch/unattended execution
- Waiver/FAAB automation
- Browser automation
- Web dashboard
- LLM-generated strategy

## Build And Bootstrap

```bash
go build -o fb ./cmd/fb
./fb init
./fb doctor
```

## Config And Auth

Default config path:
- `~/.fantasy-baseball/config.json`

Start from the example:

```bash
cp config.json.example ~/.fantasy-baseball/config.json
```

Required for ESPN:
- `league.league_id`
- `league.team_id`
- `league.season`
- `auth.espn_s2_env`
- `auth.swid_env`

Export cookies:

```bash
export ESPN_S2="..."
export ESPN_SWID="{...}"
```

Validate:

```bash
./fb espn validate
./fb doctor
```

## Multi-Team Context

`fb` supports multiple teams in one app directory using a team registry (`teams.json`) and current-team pointer.

### Quick setup

```bash
# import your existing single-team config as a named team
./fb team import-legacy my-main-team --alias main --set-current

# add a second team
./fb team add second-team --alias alt --league-id 123 --team-id 4 --season 2026

# switch context
./fb team use alt
./fb team current
./fb team list
```

You can always override context per command:

```bash
./fb --team my-main-team espn status
./fb --team second-team pitchers plan
```

Shell/OpenClaw-friendly export:

```bash
eval "$(./fb team env alt)"
```

This sets:
- `FB_TEAM`

Team references (`--team` and team subcommands) accept either:
- full team name
- alias

## Command Model

### Decision Commands (non-mutating)
- `fb pitchers plan|last`
- `fb pickups plan|last`
- `fb transactions plan|last`
- `fb lineup plan|last`

### Ops Commands (mutating, one item per command)
- `fb execute transaction`
- `fb execute lineup`
- `fb execute history|pending|verify|reconcile|resolve`

### Source Data
- `fb espn sync ...`
- `fb espn show ...`
- `fb espn status`
- `fb forecaster sync|show|status|clear`

## Weekly Routine

```bash
# 1) refresh source data
./fb espn sync roster
./fb espn sync free-agents pitchers --limit 100
./fb forecaster sync --url
./fb espn status

# 2) generate decisions
./fb pitchers plan
./fb pickups plan
./fb transactions plan
./fb lineup plan

# 3) execute one transaction
./fb execute transaction --add "Roki Sasaki" --drop "Sandy Alcantara"
./fb execute transaction --add "Roki Sasaki" --drop "Sandy Alcantara" --confirm

# 4) execute one lineup move
./fb execute lineup --player "Kris Bubic" --to-slot P
./fb execute lineup --player "Kris Bubic" --to-slot P --confirm

# 5) follow-up if needed
./fb execute pending
./fb execute verify --execution-id <id>
./fb execute reconcile --execution-id <id>
```

## Transaction Ops

Direct transaction execution (no plan approval step required):

```bash
# add/drop
./fb execute transaction --add "Shota Imanaga" --drop "Sandy Alcantara"
./fb execute transaction --add "Shota Imanaga" --drop "Sandy Alcantara" --confirm

# add-only (use open bench)
./fb execute transaction --add "Roki Sasaki"
./fb execute transaction --add "Roki Sasaki" --confirm

# next-day effective execution
./fb execute transaction --add "Roki Sasaki" --next-day --confirm
```

## Lineup Ops

```bash
./fb execute lineup --player "Brandon Woodruff" --to-slot BE
./fb execute lineup --player "Brandon Woodruff" --to-slot BE --confirm
```

Supported target slots:
- `P`
- `SP`
- `RP`
- `BE`

## Status Vocabulary

### Preflight
- `executable`
- `blocked`
- `stale`
- `conflict`
- `unknown`

### Execution
- `started`
- `submitted`
- `succeeded`
- `failed`
- `aborted`
- `ambiguous`

### Verification
- `verified`
- `verification_pending`
- `unverified`
- `verification_failed`
- `unknown`

## Safety Model

- One mutation per command
- Explicit `--confirm` required for real writes
- Immediate preflight before each write
- Duplicate protection for already-resolved executions
- No silent retries
- Verification tracked separately from write submission
- Ambiguity surfaced explicitly

## Troubleshooting

### Execution blocked/stale

```bash
./fb execute transaction --add "..." --drop "..."
./fb execute pending
./fb execute resolve --execution-id <id>
./fb espn sync roster
./fb espn sync free-agents pitchers --limit 100
```

### Verify unresolved attempt

```bash
./fb execute verify --execution-id <id>
./fb execute reconcile --execution-id <id>
./fb execute history --execution-id <id> --json
```

### No forecaster match

```bash
./fb forecaster show starts --from YYYY-MM-DD --to YYYY-MM-DD --include-tbd
./fb forecaster sync --url
```

### Readiness

```bash
./fb doctor
```

## JSON Output

Most commands support `--json` for automation/OpenClaw.

Examples:

```bash
./fb doctor --json
./fb espn status --json
./fb execute history --execution-id 14 --json
./fb execute transaction --add "Roki Sasaki" --drop "Sandy Alcantara" --json
```

## Notes

- `fb healthcheck` remains a minimal legacy-style config/database check.
- `fb doctor` is the primary operator readiness command.
