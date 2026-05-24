#!/usr/bin/env bash
# mainnet_burst_check.sh — 메인넷 가동 첫 24h 부하 체크포인트.
#
# 권장 실행 시점:
#   T+1h    바로 위험 신호 (메모리/CPU/scrape latency) 식별
#   T+6h    안정화 상태 확인 + collection_duration p95 SC-003 (< 1s) 합격
#   T+24h   daily log rotation 한 사이클 후 disk usage / RSS drift 확인
#
# 실행:
#   bash scripts/mainnet_burst_check.sh
#   bash scripts/mainnet_burst_check.sh --json   # 머신 파싱용
#
# 환경변수:
#   VPUB_LOCAL    /metrics 호스트                   default localhost:8002
#   PROM_URL      Prometheus base URL                default http://monitor.bharvest.io:9090
#   INSTANCE      Prometheus instance label          default Main_hyperliquid_VPUB_apn1
#   USER_HOME     publisher 사용자 홈                default /home/ubuntu
#
# 출력 컬럼별 의미 + 임계:
#   metrics_p95      /metrics 응답 p95                < 200ms (SC-003)
#   rss_mb           vpub-exporter RSS 평균            < 400MB (mainnet drop-in 임계)
#   cpu_pct          vpub-exporter CPU 평균            < 20% (CPUQuota)
#   coll_dur_p95     collection_duration p95          < 1s   (SC-003)
#   visor_log_mb     daily visor 로그 파일 크기        — drift 관찰
#   pub_log_mb       daily publisher 컴포넌트 로그 합  — drift 관찰
#   rpc_up_n         vpub_bridge_rpc_up == 1 시리즈 수  ≥ 4 (mainnet R-005)
#   ticker_errors    collection_errors_total 누적     낮을수록 좋음

set -euo pipefail

VPUB_LOCAL="${VPUB_LOCAL:-localhost:8002}"
PROM_URL="${PROM_URL:-http://monitor.bharvest.io:9090}"
INSTANCE="${INSTANCE:-Main_hyperliquid_VPUB_apn1}"
USER_HOME="${USER_HOME:-/home/ubuntu}"
JSON=0
[ "${1:-}" = "--json" ] && JSON=1

# 1) /metrics latency p95 (100 sample)
metrics_p95=$(for _ in $(seq 1 100); do
  curl -o /dev/null -w "%{time_total}\n" -s "http://$VPUB_LOCAL/metrics"
  sleep 0.2
done | sort -n | awk 'BEGIN{c=0}{a[c++]=$1}END{printf "%.4f", a[int(c*0.95)]}')

# 2) RSS / CPU 5분 평균
pid=$(systemctl show -p MainPID --value vpub-exporter 2>/dev/null || echo 0)
rss_mb="n/a"
cpu_pct="n/a"
if [ "$pid" != "0" ]; then
  read rss_mb cpu_pct <<EOF
$(for _ in $(seq 1 5); do
    ps -o rss=,pcpu= -p "$pid" 2>/dev/null
    sleep 60
  done | awk '{r+=$1; c+=$2; n++} END{printf "%.1f %.2f", r/n/1024, c/n}')
EOF
fi

# 3) collection_duration p95 (Prometheus quantile over 1h)
coll_dur_p95=$(curl -sG "$PROM_URL/api/v1/query" \
  --data-urlencode "query=histogram_quantile(0.95, sum by (le) (rate(vpub_exporter_collection_duration_seconds_bucket{instance=\"$INSTANCE\"}[1h])))" \
  | jq -r '.data.result[0].value[1] // "n/a"')

# 4) 로그 파일 크기 (today UTC)
today=$(date -u +%Y%m%d)
visor_log_mb=$(sudo du -m "$USER_HOME/v-publisher/log/$today" 2>/dev/null | awk '{print $1}' || echo "n/a")
pub_log_mb=$(sudo du -cm /tmp/validator-publisher/*/$today 2>/dev/null | tail -1 | awk '{print $1}' || echo "n/a")

# 5) RPC up 시리즈 수
rpc_up_n=$(curl -sG "$PROM_URL/api/v1/query" \
  --data-urlencode "query=count(vpub_bridge_rpc_up{instance=\"$INSTANCE\"} == 1)" \
  | jq -r '.data.result[0].value[1] // "0"')

# 6) collector errors 누적 (모든 collector)
ticker_errors=$(curl -sG "$PROM_URL/api/v1/query" \
  --data-urlencode "query=sum(vpub_exporter_collection_errors_total{instance=\"$INSTANCE\"})" \
  | jq -r '.data.result[0].value[1] // "0"')

# Report
if [ "$JSON" = "1" ]; then
  jq -n \
    --arg metrics_p95 "$metrics_p95" \
    --arg rss_mb "$rss_mb" \
    --arg cpu_pct "$cpu_pct" \
    --arg coll_dur_p95 "$coll_dur_p95" \
    --arg visor_log_mb "$visor_log_mb" \
    --arg pub_log_mb "$pub_log_mb" \
    --arg rpc_up_n "$rpc_up_n" \
    --arg ticker_errors "$ticker_errors" \
    '{metrics_p95: $metrics_p95, rss_mb: $rss_mb, cpu_pct: $cpu_pct,
      coll_dur_p95: $coll_dur_p95, visor_log_mb: $visor_log_mb,
      pub_log_mb: $pub_log_mb, rpc_up_n: $rpc_up_n,
      ticker_errors: $ticker_errors}'
  exit 0
fi

cat <<EOF
==== vpub-exporter mainnet burst check ====
host: $VPUB_LOCAL    instance: $INSTANCE    prom: $PROM_URL
time: $(date -u +%Y-%m-%dT%H:%M:%SZ)
-------------------------------------------
metrics_p95         $metrics_p95 s          (임계 < 0.200 s)
rss_mb              $rss_mb MB              (임계 < 400 MB)
cpu_pct             $cpu_pct %              (임계 < 20%)
coll_dur_p95        $coll_dur_p95 s         (임계 < 1 s — SC-003)
visor_log_mb today  $visor_log_mb MB        (drift 관찰)
pub_log_mb today    $pub_log_mb MB          (drift 관찰)
rpc_up_n            $rpc_up_n / 7           (임계 ≥ 4 — R-005)
ticker_errors       $ticker_errors          (낮을수록 좋음)
EOF

# Verdict
fail=""
awk -v v="$metrics_p95" 'BEGIN{exit !(v+0 > 0.2)}' && fail="$fail metrics_p95"
awk -v v="$rss_mb"      'BEGIN{exit !(v+0 > 400)}' && fail="$fail rss"
awk -v v="$cpu_pct"     'BEGIN{exit !(v+0 > 20)}'  && fail="$fail cpu"
awk -v v="$coll_dur_p95" 'BEGIN{exit !(v+0 > 1)}'  && fail="$fail collection_duration"
awk -v v="$rpc_up_n"    'BEGIN{exit !(v+0 < 4)}'   && fail="$fail rpc_quorum"
if [ -n "$fail" ]; then
  echo "VERDICT: ❌ failing —$fail"
  exit 1
fi
echo "VERDICT: ✅ all green"
