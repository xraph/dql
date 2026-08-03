#!/usr/bin/env bash
#
# Fails when benchstat reports a statistically significant slowdown beyond
# THRESHOLD percent. benchstat always exits 0, so the pass/fail decision is made
# here.
#
# Usage: bench-gate.sh old.txt new.txt
# Env:   THRESHOLD  percent slowdown that fails the build (default 20)
#
# Only the sec/op section is gated. B/op and allocs/op changes are reported in
# the pull-request comment but do not fail the build on their own — an
# allocation change is often an intentional trade.
#
# benchstat marks changes it cannot distinguish from noise as "~" in the
# "vs base" column, and those are skipped. That is what implements
# "significant changes only" — no p-value parsing needed here.
set -euo pipefail

OLD="${1:?usage: bench-gate.sh old.txt new.txt}"
NEW="${2:?usage: bench-gate.sh old.txt new.txt}"
THRESHOLD="${THRESHOLD:-20}"

CSV="$(mktemp)"
trap 'rm -f "$CSV"' EXIT

benchstat -format csv "$OLD" "$NEW" > "$CSV"

# CSV layout, repeated once per metric section:
#   ,<old file>,,<new file>,,,
#   ,sec/op,CI,sec/op,CI,vs base,P
#   <bench name>,<old>,<ci>,<new>,<ci>,<vs base>,<p>
#   geomean,...                      <- summary, not a benchmark
awk -F, -v t="$THRESHOLD" '
  # Section header: remember which metric the following rows describe.
  /^,/ {
    metric = $2
    next
  }
  # Only gate wall-clock. geomean is a summary row, not a benchmark.
  metric != "sec/op" { next }
  $1 == "geomean"    { next }
  $1 == ""           { next }

  {
    delta = $6
    # "~" means benchstat could not distinguish the change from noise.
    if (delta == "~" || delta == "") next
    if (delta ~ /^\+/) {
      pct = delta
      gsub(/[+%]/, "", pct)
      if (pct + 0 > t) {
        printf "REGRESSION: %s slowed by %s (threshold %s%%)\n", $1, delta, t
        bad = 1
      }
    }
  }
  END {
    if (bad) {
      print ""
      print "Benchmarks regressed beyond the threshold. If this is an accepted"
      print "trade, raise THRESHOLD or note the reason in the pull request."
      exit 1
    }
    print "No significant slowdown beyond " t "%."
  }
' "$CSV"
