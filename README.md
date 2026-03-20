# fantasy-baseball

`fantasy-baseball` (`fb`) is a local-first CLI for fantasy baseball pitcher planning.

It ingests probable starter projections into SQLite, syncs your ESPN roster in read-only mode, and runs weekly pitcher analysis.

## What it does

- imports forecaster-style probable starter tables from file or URL
- normalizes messy rows (`OFF`, `TBD`, blank/misaligned cells, noisy opponent text)
- stores imports, normalized starts, and parse warnings in SQLite
- analyzes weekly pitcher decisions for your roster
- detects two-start pitchers
- ranks streamers from an optional free-agent pool
- syncs ESPN league + roster snapshots (read-only)
- saves analysis runs/results for later inspection

## What it does not do

- lineup/add-drop execution
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

Run weekly pitcher report (manual JSON):

```bash
./fb pitchers report \
  --roster ./samples/roster.json \
  --from 2026-09-15 \
  --to 2026-09-22
```

Run weekly pitcher report (ESPN roster source):

```bash
./fb pitchers report --espn --from 2026-09-15 --to 2026-09-22
```

## Input files

### `roster.json`
Required field per item:
- `player_name`

Optional:
- `mlb_team`
- `role` (`SP|RP|P|unknown`)
- `status` (`active|injured|stash|unknown`)
- `locked` (bool)
- `must_hold` (bool)
- `notes`

Example: [samples/roster.json](/Users/jakebot/dev/fantasy-bb/samples/roster.json)

### `free_agents.json`
Required field per item:
- `player_name`

Optional:
- `mlb_team`
- `role` (`SP|RP|P|unknown`)
- `watch` (bool)
- `ownership_pct` (0-100)
- `notes`

Example: [samples/free_agents.json](/Users/jakebot/dev/fantasy-bb/samples/free_agents.json)

## Core workflows

### 1. Import and inspect forecaster data

```bash
./fb forecaster import --file ./tmp/table.html
./fb forecaster source-status
./fb forecaster list --from 2026-09-15 --to 2026-09-22
./fb forecaster warnings --limit 25
```

### 2. Analyze your roster

```bash
./fb pitchers analyze-week --roster ./samples/roster.json --from 2026-09-15 --to 2026-09-22
./fb pitchers two-start --roster ./samples/roster.json --from 2026-09-15 --to 2026-09-22
./fb pitchers explain-matches --roster ./samples/roster.json --from 2026-09-15 --to 2026-09-22
```

### 2b. Analyze from ESPN roster snapshots

```bash
./fb espn sync roster
./fb pitchers analyze-week --espn --from 2026-09-15 --to 2026-09-22
./fb pitchers two-start --espn --from 2026-09-15 --to 2026-09-22
./fb pitchers explain-matches --espn --from 2026-09-15 --to 2026-09-22
```

### 3. Rank streamers

```bash
./fb pitchers streamers \
  --roster ./samples/roster.json \
  --pool ./samples/free_agents.json \
  --from 2026-09-15 \
  --to 2026-09-22 \
  --top 10
```

### 4. Re-open the latest saved analysis

```bash
./fb pitchers last-report
```

## Matching behavior

Matching is deterministic and inspectable:

- normalize player name (case, punctuation, whitespace)
- exact normalized-name match first
- use `mlb_team` as tie-breaker when available
- output `matched`, `unmatched`, or `ambiguous_match`
- use `pitchers explain-matches` for debug visibility

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
- `fb espn show roster [--pitchers-only] [--sync-run <id>]`
- `fb espn show league [--sync-run <id>]`
- `fb espn status` (alias: `source-status`)
- `fb espn warnings [--sync-run <id>] [--limit <n>]`

### Pitchers
- `fb pitchers analyze-week --roster <path> ...`
- `fb pitchers two-start --roster <path> ...`
- `fb pitchers streamers --roster <path> --pool <path> ...`
- `fb pitchers report --roster <path> ...`
- `fb pitchers explain-matches --roster <path> ...`
- `fb pitchers last-report`

For `analyze-week`, `two-start`, `report`, and `explain-matches`, you can use either:
- `--roster <path>` (manual JSON) or
- `--espn` (latest ESPN snapshot), optionally `--sync-run <id>`

In `--espn` mode, starter-focused analysis excludes clear RP-only roster players from report inputs.

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

## Development

Run tests:

```bash
go test ./...
```
