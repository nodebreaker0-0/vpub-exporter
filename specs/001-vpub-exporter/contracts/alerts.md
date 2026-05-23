# Contract: Alert Rules (vpub-exporter)

> 본 문서는 monitoring 레포의 `config/rules/hyperliquid_vpub_rule.yaml` 로 들어갈 알람 룰의 명세다.
> `alert_level` 은 모니터링 레포의 alertmanager 라우팅과 직접 결합 — 5종(`critical`/`high`/`medium`/`low`/`disk`) 안에서만 사용.
> 실제 yaml 은 `promtool check rules` 통과 필수.

## 0. 출력 파일 형식

monitoring 레포의 parser 는 룰 yaml 을 `{groups: [.]}` 로 wrap 한 뒤 promtool check 한다. 따라서 본 contract 의 yaml 은 **group 노드 1개**의 내용을 그대로 담는다:

```yaml
name: hyperliquid_vpub
rules:
  - alert: VpubServiceDown
    expr: ...
    for: 1m
    labels: {alert_level: critical, chain: hyperliquid}
    annotations:
      summary: ...
      description: ...
  ...
```

라벨 `chain: hyperliquid` 는 alertmanager 필터링 용 (현재 chain=band/axelar 필터 예시가 alertmanager.toml 에 있음).

## 1. Tier 0 룰 (US1) — 4 건

```yaml
- alert: VpubServiceDown
  expr: vpub_service_up == 0
  for: 1m
  labels: {alert_level: critical, chain: hyperliquid}
  annotations:
    summary: "vpub: v-publisher.service down"
    description: "{{ $labels.instance }} v-publisher.service 가 1분 이상 inactive"

- alert: VpubChildMissing
  expr: vpub_child_count < 3
  for: 30s
  labels: {alert_level: critical, chain: hyperliquid}
  annotations:
    summary: "vpub: visor child 누락"
    description: "{{ $labels.instance }} visor child 수 = {{ $value }} (정상 3)"

- alert: VpubLogStale
  expr: time() - vpub_component_log_mtime_seconds > 300
  for: 2m
  labels: {alert_level: high, chain: hyperliquid}
  annotations:
    summary: "vpub {{ $labels.component }} 로그 5분+ 멈춤"
    description: "{{ $labels.instance }} {{ $labels.component }} 로그 mtime 갱신 없음 → hang 가능"

- alert: VpubLogStaleLong
  expr: time() - vpub_component_log_mtime_seconds > 1800
  for: 1m
  labels: {alert_level: critical, chain: hyperliquid}
  annotations:
    summary: "vpub {{ $labels.component }} 로그 30분+ 멈춤"
    description: "{{ $labels.instance }} {{ $labels.component }} 30분+ hang — 즉시 점검"
```

## 2. Tier 1 룰 (US2) — 8 건

```yaml
- alert: VpubBridgeRpcMajorityDown
  expr: sum by (instance) (vpub_bridge_rpc_up) < 4
  for: 5m
  labels: {alert_level: high, chain: hyperliquid}
  annotations:
    summary: "vpub bridge: RPC quorum 위태"
    description: "메인넷 RPC 살아있는 수 = {{ $value }} (정상 ≥ 4)"

- alert: VpubBridgeRpcSingleDown
  expr: vpub_bridge_rpc_up == 0
  for: 10m
  labels: {alert_level: medium, chain: hyperliquid}
  annotations:
    summary: "vpub bridge: RPC {{ $labels.name }} down"
    description: "{{ $labels.instance }} RPC {{ $labels.name }} 10분+ down"

- alert: VpubBridgeRpcDisagreement
  expr: increase(vpub_bridge_rpc_disagreement_total[15m]) > 5
  for: 2m
  labels: {alert_level: high, chain: hyperliquid}
  annotations:
    summary: "vpub bridge: RPC disagreement 빈발"
    description: "15분 내 RPC 합의 실패 {{ $value }} 건 — 잘못된 vote 위험"

- alert: VpubBridgeStaleVote
  expr: time() - vpub_bridge_last_vote_success_unix > 3600
  for: 5m
  labels: {alert_level: high, chain: hyperliquid}
  annotations:
    summary: "vpub bridge: 1h+ 성공 vote 없음"
    description: "{{ $labels.instance }} 마지막 성공 bridge vote 이후 1h 경과"

- alert: VpubBridgeStaleVoteLong
  expr: time() - vpub_bridge_last_vote_success_unix > 21600
  for: 5m
  labels: {alert_level: critical, chain: hyperliquid}
  annotations:
    summary: "vpub bridge: 6h+ 성공 vote 없음"
    description: "{{ $labels.instance }} 마지막 성공 bridge vote 이후 6h 경과 — 비정상"

- alert: VpubBridgeAllFail
  expr: |
    increase(vpub_bridge_vote_total{status="ok"}[1h]) == 0
    and increase(vpub_bridge_vote_total{status="fail"}[1h]) > 0
  for: 5m
  labels: {alert_level: high, chain: hyperliquid}
  annotations:
    summary: "vpub bridge: 1h 시도 vote 전부 실패"
    description: "{{ $labels.instance }} 최근 1h vote 시도가 있었지만 모두 실패"

- alert: VpubOracleStaleVote
  expr: time() - vpub_oracle_last_vote_success_unix > 7200
  for: 5m
  labels: {alert_level: high, chain: hyperliquid}
  annotations:
    summary: "vpub oracle: 2h+ 성공 vote 없음"
    description: "{{ $labels.instance }} 마지막 reference oracle update 이후 2h+"
    # NOTE: 임계는 가동 첫날 oracle publish 사이클 확정 후 조정 (research.md)

- alert: VpubOutcomePendingLong
  expr: vpub_outcome_slack_msg_24h > 5
  for: 30m
  labels: {alert_level: medium, chain: hyperliquid}
  annotations:
    summary: "vpub outcome: 미검토 액션 적체"
    description: "최근 24h outcome 채널 메시지 {{ $value }} 건 — 검토 진행 필요"

- alert: VpubSlackTokenInvalid
  expr: vpub_slack_api_ok == 0
  for: 5m
  labels: {alert_level: critical, chain: hyperliquid}
  annotations:
    summary: "vpub: Slack token 만료 / API down"
    description: "publisher 가 보내는 모든 슬랙 알람이 누락 중일 수 있음 (auth.test fail)"
```

## 3. Tier 2 룰 (US3) — 2 건

```yaml
- alert: VpubBinaryUpdateAvailable
  expr: vpub_binary_remote_mtime_unix - vpub_binary_local_mtime_unix > 3600
  for: 30m
  labels: {alert_level: medium, chain: hyperliquid}
  annotations:
    summary: "vpub: 새 publisher 바이너리 announced"
    description: "remote mtime > local +1h — 변경 사항 검토 후 수동 업그레이드 필요"

- alert: VpubBinaryRemoteCheckFail
  expr: vpub_binary_remote_check_ok == 0
  for: 1h
  labels: {alert_level: low, chain: hyperliquid}
  annotations:
    summary: "vpub: binary URL HEAD 실패"
    description: "{{ $labels.instance }} 업그레이드 트래킹 비활성 — URL/네트워크 점검"
```

## 4. 룰 ↔ Acceptance Scenario 매트릭스

spec.md 의 acceptance scenario 가 본 룰로 커버되는지 확인:

| Scenario | 룰 |
|---|---|
| US1-1 (`systemctl stop` → critical) | VpubServiceDown |
| US1-2 (child 누락 30s → critical) | VpubChildMissing |
| US1-3 (로그 5분 stale → high) | VpubLogStale |
| US1-4 (정상화 → resolve) | alertmanager 의 `resolveMsg = true` 가 처리 (룰 별도 불필요) |
| US2-1 (RPC quorum < 4 / 5m) | VpubBridgeRpcMajorityDown |
| US2-2 (vote 1h / 6h) | VpubBridgeStaleVote, VpubBridgeStaleVoteLong |
| US2-3 (disagreement 15m 5건) | VpubBridgeRpcDisagreement |
| US2-4 (oracle 2h) | VpubOracleStaleVote |
| US2-5 (outcome > 5 / 30m) | VpubOutcomePendingLong |
| US2-6 (slack auth.test 5m fail) | VpubSlackTokenInvalid |
| US3-1 (remote mtime > local + 1h) | VpubBinaryUpdateAvailable |
| US3-2 (HEAD 1h fail) | VpubBinaryRemoteCheckFail |

## 5. alert_level 사용 통계

- `critical` (PagerDuty + ddoa-critical): 5 룰 (VpubServiceDown, VpubChildMissing, VpubLogStaleLong, VpubBridgeStaleVoteLong, VpubSlackTokenInvalid)
- `high` (ddoa-high): 6 룰
- `medium` (ddoa-low): 3 룰 (VpubBridgeRpcSingleDown, VpubOutcomePendingLong, VpubBinaryUpdateAvailable)
- `low` (ddoa-low): 1 룰 (VpubBinaryRemoteCheckFail)
- `disk`: 0 — node-exporter / 기존 룰이 커버

## 6. 변경 절차

1. 본 contract 부터 수정.
2. yaml 갱신 후 로컬에서 `cat <file> | yq '{"groups":[.]}' | promtool check rules /dev/stdin` 통과.
3. monitoring 레포 PR.
4. spec.md 의 acceptance scenario 와 cross-ref 표 (§4) 동기화.
