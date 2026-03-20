# fantasy-baseball

`fantasy-baseball` (`fb`) is a local-first CLI for fantasy baseball pitcher planning.

It ingests probable starter projections into SQLite, then runs roster-aware weekly pitcher analysis from manual JSON inputs.

## What it does

- imports forecaster-style probable starter tables from file or URL
- normalizes messy rows (`OFF`, `TBD`, blank/misaligned cells, noisy opponent text)
- stores imports, normalized starts, and parse warnings in SQLite
- analyzes weekly pitcher decisions for your roster
- detects two-start pitchers
- ranks streamers from an optional free-agent pool
- saves analysis runs/results for later inspection

## What it does not do

- ESPN auth or roster sync
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

Run weekly pitcher report:

```bash
./fb pitchers report \
  --roster ./samples/roster.json \
  --from 2026-09-15 \
  --to 2026-09-22
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
- `fb forecaster source-status`
- `fb forecaster warnings`
- `fb forecaster clear --yes`

### Pitchers
- `fb pitchers analyze-week --roster <path> ...`
- `fb pitchers two-start --roster <path> ...`
- `fb pitchers streamers --roster <path> --pool <path> ...`
- `fb pitchers report --roster <path> ...`
- `fb pitchers explain-matches --roster <path> ...`
- `fb pitchers last-report`

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

## Development

Run tests:

```bash
go test ./...
```
