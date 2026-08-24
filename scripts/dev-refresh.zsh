#!/usr/bin/env zsh

# Intended usage:
#   source scripts/dev-refresh.zsh
# If you execute it directly, completion sourcing will only affect the subshell.

_fb_dev_refresh_main() {
  # Keep option changes local when sourced so we don't mutate the caller shell.
  emulate -L zsh
  setopt ERR_EXIT NO_UNSET PIPE_FAIL

  # Resolve repo root from this script location.
  SCRIPT_PATH="${(%):-%x}"
  SCRIPT_DIR="$(cd "$(dirname "$SCRIPT_PATH")" && pwd)"
  REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

  cd "$REPO_ROOT"

  echo "[1/4] Building fb"
  go build -o fb ./cmd/fb

  echo "[2/4] Generating zsh completion"
  ./fb completion zsh > ./scripts/fb-completions.zsh

  echo "[3/4] Loading zsh completion"
  completion_status="generated"
  if (( $+functions[compdef] )); then
    source ./scripts/fb-completions.zsh
    completion_status="loaded"
  else
    echo "      compdef not loaded; generated completion but skipped loading it"
  fi

  echo "[4/4] Running init"
  ./fb init

  if command -v jq >/dev/null 2>&1; then
    team_aliases=("${(@f)$(
      ./fb team list --json 2>/dev/null \
        | jq -r '.teams[]? | if (.alias // "") != "" then .alias else .name end'
    )}")
    if (( ${#team_aliases[@]} > 0 )); then
      echo "      Initializing registered team DBs"
      for team in "${team_aliases[@]}"; do
        echo "      - $team"
        ./fb --team "$team" init >/dev/null
      done
    fi
  else
    echo "      jq not found; skipped registered team DB init"
  fi

  echo "Done. fb is rebuilt, completion is ${completion_status}, and init ran."
}

_fb_dev_refresh_main "$@"
unset -f _fb_dev_refresh_main
