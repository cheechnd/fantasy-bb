# fantasy-baseball (`fb`)

`fb` is a local-first CLI for ESPN fantasy baseball pitcher workflows.

It keeps decision artifacts and execution history in SQLite, and gates real writes behind:
- deterministic resolution
- immediate preflight
- explicit confirmation
- post-write verification
- auditable execution events

Design philosophy:
- `fb` is transactional and informational, not strategic
- `fb` provides facts, projections, preflight, execution, and verification
- strategy/judgment belongs in your skill/agent layer

## v1 Scope

### Included
- Forecaster probable-start ingestion and normalization
- ESPN roster + free-agent snapshot sync
- ESPN reliever depth chart sync
- Factual rostered-pitcher projection views (`fb pitchers plan|last`)
- Factual available-pitcher projection views (`fb pickups plan|last`)
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
- `league.timezone` (defaults to `America/New_York`)
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

# initialize team-local database (recommended right after add)
./fb --team alt init
./fb --team alt db migrate

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

Note: `fb team add` registers the team entry immediately, but the team DB path is prepared when you run a DB-touching command (for example `./fb --team <alias> init` and `./fb --team <alias> db migrate`).
`fb init` uses the active team context; pass `--team <alias>` to initialize a specific team DB.

## Command Model

### Decision Commands (non-mutating)
- `fb pitchers plan|last`
- `fb pickups plan|last`

Decision commands are intentionally neutral:
- no “top candidates/top streamers/upgrades” advice framing
- no “watch/monitor/risky buckets” advice framing
- no hidden strategy assumptions

### Ops Commands (mutating, one item per command)
- `fb execute transaction`
- `fb execute lineup`
- `fb execute history|pending|verify|reconcile|resolve`

Lineup planning/review commands were intentionally removed; lineup is now an explicit direct operation via `fb execute lineup`.

### Source Data
- `fb espn sync ...`
- `fb espn show ...`
- `fb espn status`
- `fb mlb schedule`
- `fb relievers sync|show|status`
- `fb forecaster sync|show|status|clear`

`fb espn show matchup` provides live weekly matchup facts:
- current matchup score (team vs opponent)
- pitching starts used
- pitching starts max/remaining
- matchup scoring-period span, including `multi_week_scoring_matchup` for All-Star or playoff-length scoring windows

Examples:

```bash
./fb espn show matchup
./fb --team wt espn show matchup --matchup-period 6
./fb espn show matchup --json
./fb mlb schedule                 # today
./fb mlb schedule --date 2026-05-10
./fb mlb schedule --from 2026-05-10 --to 2026-05-12
```

`fb mlb schedule --json` uses the requested display timezone (default: local machine timezone) for `game_date`, `game_time`, and `game_datetime`. The raw UTC timestamp is exposed separately as `game_datetime_utc` so late West Coast games do not appear to belong to the next local baseball date.

Roster context views (useful after games start):

```bash
# current scoring period roster view
./fb espn sync roster
./fb espn show roster

# explicit scoring period roster view
./fb espn sync roster --scoring-period-id 49
./fb espn show roster --scoring-period-id 49

# next-day effective roster view (auto-resolves next scoring period)
./fb espn sync roster --next-day
./fb espn show roster --next-day
```

`fb forecaster show` includes:
- `starts`
- `week`
- `warnings`

Free-agent snapshots now include acquisition status:
- `ACQ_STATUS=FREEAGENT` means immediately addable
- `ACQ_STATUS=WAIVERS` means claim/waiver flow (not immediately addable)
- `waiver_process_datetime` is ESPN's waiver processing timestamp when ESPN provides `waiverProcessDate`; it is formatted in `league.timezone` and is not necessarily the claim deadline

Reliever depth chart facts:

```bash
./fb relievers sync
./fb relievers show
./fb relievers status
```

`fb relievers sync` reads ESPN's editorial reliever depth chart and persists bullpen facts separately from ESPN fantasy eligibility. The generic ESPN player `role` field can remain `P` for dual-eligible pitchers, while reliever output and enriched ESPN roster/free-agent JSON can include:
- `relief_role`
- `relief_role_team`
- `relief_role_source`
- `relief_role_as_of`
- `relief_role_match_status`
- `relief_role_conflict_flag`

This keeps bullpen role, scheduled starts, fantasy eligibility, waiver state, and roster slot as separate facts. The reliever parser records source date, fetch time, parse coverage, unmatched rows, ambiguous rows, and conflicts. If ESPN changes the article layout enough that parse coverage drops, the sync is recorded as failed instead of publishing incomplete data as current.

## Weekly Routine

```bash
# 1) refresh source data
./fb espn sync roster
./fb espn sync free-agents pitchers --limit 100
./fb relievers sync
./fb forecaster sync --url
./fb espn status
./fb espn show matchup

# 2) generate decisions
./fb pitchers plan
./fb pickups plan

# use your OpenClaw skill/agent to choose actions from these factual views

# 3) execute one transaction
./fb execute transaction --add "Roki Sasaki" --drop "Sandy Alcantara"
./fb execute transaction --add "Roki Sasaki" --drop "Sandy Alcantara" --confirm

# 4) optionally execute one lineup move
./fb execute lineup --player "Kris Bubic" --to-slot P
./fb execute lineup --player "Kris Bubic" --to-slot P --confirm

# 5) follow-up if needed
./fb execute pending
./fb execute verify --execution-id <id>
./fb execute reconcile --execution-id <id>

# optional: preflight/preview against next-day effective roster context
./fb execute preflight --next-day
./fb execute dry-run --next-day
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

# explicit scoring-period context
./fb execute transaction --add "Seth Lugo" --drop "Tatsuya Imai" --scoring-period-id 49 --confirm
```

Notes:
- Transaction resolution/preflight treat `WAIVERS` as not immediately available.
- For direct `fb execute transaction`, `--next-day` / `--scoring-period-id` apply to drop-target roster resolution too.
- Add-only preflight checks active roster capacity separately from ESPN IL capacity. In JSON output, use `active_roster_capacity_excluding_il`, `current_open_active_roster_slots_excluding_il`, and `effective_open_active_roster_slots_excluding_il` to determine whether a direct add has a usable non-IL roster slot.
- If a player shows `On Waivers` in ESPN UI, sync candidates first and confirm `ACQ_STATUS`:

```bash
./fb espn sync free-agents pitchers --limit 100
./fb espn show free-agents --limit 100 | grep "<Player Name>"
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

If blocked on availability, inspect `ACQ_STATUS`:
- `WAIVERS` => not immediately executable via direct add
- `FREEAGENT` => immediately addable (subject to other preflight checks)

### Verify unresolved attempt

```bash
./fb execute verify --execution-id <id>
./fb execute reconcile --execution-id <id>
./fb execute history --execution-id <id> --json
```

### No forecaster match

```bash
./fb forecaster show starts --from YYYY-MM-DD --to YYYY-MM-DD --include-tbd
./fb forecaster show starts --mlb-team NYY
./fb forecaster sync --url
```

### Readiness

```bash
./fb doctor
```

## JSON Output

Most commands support `--json` for automation/OpenClaw.

Projection-oriented output is intentionally fact-level, not strategy-level:
- `fb pitchers plan|last` and `fb pickups plan|last` print one row per projected start
- `fb pitchers plan --json` and `fb pickups plan --json` expose `starts`
- transaction-plan JSON exposes `add_starts` and `drop_starts`
- each start includes `game_date`, `opponent`, `home_away`, `projected_fpts`, and `status`
- no-start players return an empty array (`"starts": []`)

Consumers should calculate counts, totals, rankings, and best-start choices themselves. `fb` preserves the source projection facts and avoids recommendation framing.

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
