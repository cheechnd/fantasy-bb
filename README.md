# fantasy-baseball

`fantasy-baseball` (`fb`) is a local-first CLI for fantasy baseball pitcher planning.

It ingests probable starter projections into SQLite, syncs your ESPN roster in read-only mode, and runs weekly pitcher analysis.

## What it does

- imports forecaster-style probable starter tables from file or URL
- normalizes messy rows (`OFF`, `TBD`, blank/misaligned cells, noisy opponent text)
- stores imports, normalized starts, and parse warnings in SQLite
- analyzes weekly pitcher decisions for your roster
- detects two-start pitchers
- syncs ESPN league + roster snapshots (read-only)
- ingests a bounded ESPN free-agent pitcher candidate pool (read-only)
- saves analysis runs/results for later inspection
- builds saved weekly pitcher plans with deterministic start/sit buckets
- generates saved read-only pickup/streamer recommendations with upgrade comparisons
- generates saved read-only add/drop transaction plans with deterministic move buckets

## What it does not do

- lineup/add-drop execution
- transaction execution/waiver submission/FAAB bidding
- browser automation
- web dashboard

## Quickstart

```bash
cd /Users/jakebot/dev/fantasy-bb
go mod tidy
go build -o fb ./cmd/fb
./fb init
```

Import probable starts:

```bash
./fb forecaster import --file ./tmp/table.html
```

Sync ESPN roster (read-only):

```bash
export ESPN_S2="your_espn_s2_cookie"
export ESPN_SWID="{your-swid-cookie}"
./fb espn validate
./fb espn sync roster
./fb espn show roster --pitchers-only
```

Run weekly pitcher report (ESPN roster source):

```bash
./fb pitchers report --from 2026-09-15 --to 2026-09-22
```

Build a weekly pitcher plan:

```bash
./fb pitchers plan --from 2026-09-15 --to 2026-09-22
./fb pitchers start-sit --from 2026-09-15 --to 2026-09-22
./fb pitchers plan-last
```

Fetch bounded free-agent pitchers and build pickup recommendations:

```bash
./fb espn free-agents pitchers --limit 25
./fb pickups recommend --from 2026-09-15 --to 2026-09-22
./fb pickups top-streamers --from 2026-09-15 --to 2026-09-22 --top 15
./fb pickups last
./fb transactions plan --from 2026-09-15 --to 2026-09-22
./fb transactions top --from 2026-09-15 --to 2026-09-22
```

## Typical workflow

### 1. Import and inspect forecaster data

```bash
./fb forecaster import --file ./tmp/table.html
./fb forecaster status
./fb forecaster list --from 2026-09-15 --to 2026-09-22
./fb forecaster warnings --limit 25
```

### 2. Sync ESPN and analyze your roster

```bash
./fb espn sync roster
./fb pitchers analyze-week --from 2026-09-15 --to 2026-09-22
./fb pitchers two-start --from 2026-09-15 --to 2026-09-22
./fb pitchers explain-matches --from 2026-09-15 --to 2026-09-22
```

### 3. Re-open the latest saved analysis

```bash
./fb pitchers last-report
```

### 4. Generate a weekly pitcher plan

```bash
./fb espn sync roster
./fb forecaster import --file ./tmp/forecaster_sample.html
./fb pitchers plan
./fb pitchers start-sit
./fb pitchers plan-last
./fb pitchers plan-show --plan-id 1
```

### 5. Generate pickup recommendations

```bash
./fb espn free-agents pitchers --limit 25
./fb pickups recommend
./fb pickups top-streamers --top 20
./fb pickups last
./fb pickups show --recommendation-id 1
```

### 6. Generate transaction plans (read-only add/drop proposals)

```bash
./fb transactions plan --top 10
./fb transactions top --top 10
./fb transactions compare --top 10
./fb transactions last
./fb transactions show --plan-id 1
./fb transactions explain --plan-id 1
```

Recommendations are deterministic and inspectable:

- `top_candidate`: strongest overall options in the window
- `streamer`: short-window options above streamer threshold
- `upgrade`: candidate projected above a weaker rostered pitcher
- `risky_monitor`: interesting but uncertain (`TBD`, missing projection, etc.)
- `unmatched`: candidate could not be matched to forecaster starts

Transaction move buckets:

- `strong_move`: clear projected weekly upgrade with acceptable certainty
- `marginal_move`: positive projected upgrade, but smaller
- `risky_move`: intriguing move with meaningful uncertainty
- `watch_only`: low-confidence or low-delta option to monitor

## Interpreting output

- `Top overall candidates`: best free-agent options in the selected window.
- `Best streamers`: options above streamer threshold for the window.
- `Risky / monitor`: candidates with uncertainty (`TBD`, missing projection, injury/status risk).
- `Unmatched`: candidate could not be mapped to probable-start rows in the selected window.

In-season data note:

- if the selected date range has no probable starts, many rows will appear as `unmatched`.
- verify window coverage with `fb forecaster list --from ... --to ... --include-tbd`.

## Matching behavior

Matching is deterministic and inspectable:

- normalize player name (case, punctuation, whitespace)
- exact normalized-name match first
- use `mlb_team` as tie-breaker when available
- output `matched`, `unmatched`, or `ambiguous_match`
- use `pitchers explain-matches` for debug visibility

## Pitcher planning buckets

`fb pitchers plan` and `fb pitchers start-sit` assign each rostered pitcher to one bucket:

- `auto_start`: strong projected value for the window
- `likely_start`: solid projection but below auto-start threshold
- `monitor`: uncertainty (for example `TBD`, missing projection, ambiguous/unmatched data)
- `bench`: low projected value with a scheduled start
- `no_start_scheduled`: matched pitcher but no projected starts in the window

Thresholds and penalties are configurable under `planning.pitchers` in `config.json`.

## Command reference

### App/system
- `fb init`
- `fb healthcheck`
- `fb version`
- `fb config show`
- `fb db migrate`
- `fb db status`

### Forecaster
- `fb forecaster import --file <path>`
- `fb forecaster import --url <url>`
- `fb forecaster list ...`
- `fb forecaster show-week ...`
- `fb forecaster top ...`
- `fb forecaster status` (alias: `source-status`)
- `fb forecaster warnings`
- `fb forecaster clear --yes`

### ESPN (read-only)
- `fb espn validate`
- `fb espn sync roster [--dry-run]`
- `fb espn free-agents pitchers --limit <n> [--search <text>] [--team <MLB>]`
- `fb espn show roster [--pitchers-only] [--sync-run <id>]`
- `fb espn show league [--sync-run <id>]`
- `fb espn show free-agents [--candidate-run <id>] [--limit <n>]`
- `fb espn status` (alias: `source-status`)
- `fb espn warnings [--sync-run <id>] [--limit <n>]`

### Pitchers
- `fb pitchers analyze-week [--sync-run <id>] ...`
- `fb pitchers two-start [--sync-run <id>] ...`
- `fb pitchers report [--sync-run <id>] ...`
- `fb pitchers explain-matches [--sync-run <id>] ...`
- `fb pitchers last-report`
- `fb pitchers plan [--from YYYY-MM-DD --to YYYY-MM-DD --sync-run <id> --import-run <id>]`
- `fb pitchers start-sit [--from YYYY-MM-DD --to YYYY-MM-DD --sync-run <id> --import-run <id>]`
- `fb pitchers plan-last`
- `fb pitchers plan-show --plan-id <id>`

### Pickups
- `fb pickups recommend [--from YYYY-MM-DD --to YYYY-MM-DD --sync-run <id> --import-run <id> --candidate-run <id> --top <n>]`
- `fb pickups top-streamers [--min-total-fpts <n>]`
- `fb pickups last`
- `fb pickups show --recommendation-id <id>`

Pitcher analysis uses ESPN snapshots only. By default it uses the latest sync, or `--sync-run <id>` when provided.

`pickups` commands are ESPN-backed and read-only. They use latest ESPN sync + forecaster import + candidate run by default unless overridden with run IDs.

### Transactions
- `fb transactions plan [--from YYYY-MM-DD --to YYYY-MM-DD --sync-run <id> --import-run <id> --pitcher-plan-id <id> --pickup-run <id> --top <n>]`
- `fb transactions top [same source/window flags]`
- `fb transactions compare [same source/window flags]`
- `fb transactions last`
- `fb transactions show --plan-id <id>`
- `fb transactions explain --plan-id <id>`

Global flags (all commands):
- `--json`
- `--app-dir`
- `--config`
- `--db-path`
- `--log-level`
- `--environment`
- `--dry-run`
- `--require-confirmation`

## Data storage

SQLite is the system of record. Key tables:

- forecaster imports: `forecaster_import_runs`, `probable_starts`, `parse_warnings`
- pitcher analysis: `analysis_runs`, `analysis_results`, `player_match_results`
- ESPN sync snapshots: `espn_sync_runs`, `espn_raw_payloads`, `espn_league_snapshots`, `espn_roster_snapshots`
- ESPN bounded candidate ingestion: `espn_candidate_runs`, `espn_free_agent_candidates`
- saved planning artifacts: `pitcher_plans`, `pitcher_plan_items`
- pickup recommendation artifacts: `pickup_recommendation_runs`, `pickup_recommendation_items`
- transaction planning artifacts: `transaction_plans`, `transaction_plan_items`

## ESPN credentials

`fb` reads ESPN cookie values from environment variables referenced by config:

- `auth.espn_s2_env` (default: `ESPN_S2`)
- `auth.swid_env` (default: `ESPN_SWID`)

Set them before ESPN commands:

```bash
export ESPN_S2="..."
export ESPN_SWID="{...}"
```

`config.json.example` includes non-secret ESPN settings (`base_url`, `timeout_seconds`), but no cookie values.

Planning thresholds are configurable under `planning.pitchers` in `config.json`. If omitted, defaults are used.

Pickup recommendation tuning is configurable under `pickups.pitchers` in `config.json`:

- `default_candidate_limit`
- `max_candidate_limit`
- `min_streamer_total_fpts`
- `strong_upgrade_delta_fpts`
- `marginal_upgrade_delta_fpts`
- `risky_monitor_min_total_fpts`

Transaction planning tuning is configurable under `transactions.pitchers` in `config.json`:

- `top_move_limit`
- `max_pairings`
- `strong_move_delta_fpts`
- `marginal_move_delta_fpts`
- `risky_move_min_delta_fpts`
- `uncertainty_penalty_tbd`
- `uncertainty_penalty_missing_projection`
- `uncertainty_penalty_ambiguous_match`
- `allow_compare_against_likely_start`

## Development

Run tests:

```bash
go test ./...
```
