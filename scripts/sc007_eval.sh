#!/usr/bin/env bash
# sc007_eval.sh — SC-007 false-positive harness.
#
# Query Prometheus over a sliding window (default 7d) and tabulate, per
# vpub_* alert, how often it fired and how long it stayed firing.
#
# Inputs (env vars):
#   PROM_URL    Prometheus base URL                 default http://monitor.bharvest.io:9090
#   WINDOW      query window                        default 7d
#   STEP        evaluation step                     default 5m
#   INSTANCE    filter to one instance label        default '' (all)
#
# Output: markdown table to stdout.
#
# SC-007 합격: 의미 없는 발화 < 10% (운영자가 손으로 평가하는 컬럼).
#
# Notes:
#   - Uses ALERTS{alertstate="firing"} which Prometheus exposes for every
#     active rule. `changes()` over the window counts firing entries; the
#     mean of the binary series × window length gives total firing time.
#   - This script is read-only — no writes to Prometheus or Alertmanager.

set -euo pipefail

PROM_URL="${PROM_URL:-http://monitor.bharvest.io:9090}"
WINDOW="${WINDOW:-7d}"
STEP="${STEP:-5m}"
INSTANCE="${INSTANCE:-}"

# Window length in seconds (for total-time math).
case "$WINDOW" in
  *d) win_s=$(( ${WINDOW%d} * 86400 ));;
  *h) win_s=$(( ${WINDOW%h} * 3600 ));;
  *m) win_s=$(( ${WINDOW%m} * 60 ));;
  *)  echo "WINDOW must end in d/h/m" >&2; exit 1;;
esac

inst_filter=""
[ -n "$INSTANCE" ] && inst_filter=",instance=\"$INSTANCE\""

q() {
  curl -sG "$PROM_URL/api/v1/query" --data-urlencode "query=$1" \
    | jq -r '.data.result[] | "\(.metric.alertname // .metric.__name__ // "?")\t\(.value[1])"'
}

# 1) every vpub_ alert seen at all in the window (firing at any point).
alerts=$(q "max by (alertname) (max_over_time(ALERTS{alertname=~\"Vpub.*\"$inst_filter}[$WINDOW]))" \
         | awk '$2>0 {print $1}' | sort -u)

if [ -z "$alerts" ]; then
  echo "No vpub alerts seen in the past $WINDOW at $PROM_URL"
  exit 0
fi

# Header.
printf "| alert | fires | total firing | avg per fire | %% of window | needs review? |\n"
printf "|---|---:|---:|---:|---:|:--|\n"

for a in $alerts; do
  fires=$(curl -sG "$PROM_URL/api/v1/query" \
    --data-urlencode "query=sum(changes(ALERTS{alertname=\"$a\",alertstate=\"firing\"$inst_filter}[$WINDOW]))" \
    | jq -r '.data.result[0].value[1] // "0"')
  mean=$(curl -sG "$PROM_URL/api/v1/query" \
    --data-urlencode "query=avg_over_time(ALERTS{alertname=\"$a\",alertstate=\"firing\"$inst_filter}[$WINDOW])" \
    | jq -r '.data.result[0].value[1] // "0"')

  total_s=$(awk -v m="$mean" -v w="$win_s" 'BEGIN{printf "%.0f", m*w}')
  pct=$(awk -v m="$mean" 'BEGIN{printf "%.2f", m*100}')
  avg_s=0
  if [ "${fires%.*}" -gt 0 ] 2>/dev/null; then
    avg_s=$(awk -v t="$total_s" -v f="$fires" 'BEGIN{printf "%.0f", t/f}')
  fi

  # heuristic: high fire count + low avg = noisy probe (review)
  review=""
  awk -v f="${fires%.*}" -v a="$avg_s" 'BEGIN{exit !(f>=10 && a<300)}' \
    && review="🔴 noisy?" || review=""
  awk -v p="$pct" 'BEGIN{exit !(p>=10)}' \
    && review="$review 🔴 over 10%"

  printf "| %s | %s | %ss | %ss | %s%% | %s |\n" \
    "$a" "${fires%.*}" "$total_s" "$avg_s" "$pct" "$review"
done

echo
echo "_Window: $WINDOW, step: $STEP, Prometheus: $PROM_URL${INSTANCE:+, instance=$INSTANCE}_"
echo
echo "SC-007 합격 기준: \"needs review\" 컬럼이 마크된 알람을 운영자가 손으로 평가해 false-positive 비율 < 10%."
