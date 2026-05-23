# Contract: Alert Rules (vpub-exporter)

> 본 문서는 monitoring 레포의 `config/rules/hyperliquid_vpub_rule.yaml` 로 들어갈 알람 룰의 명세다.
> `alertLevel` (camelCase) 은 alertmanager 라우팅과 직접 결합 — 5종(`critical`/`high`/`medium`/`low`/`disk`) 안에서만 사용.
> 실제 yaml 은 `promtool check rules` 통과 필수.
>
> **컨벤션 정정 노트 (2026-05-23)**: monitoring 레포의 기존 룰 (`band_v3_alert_rule.yaml`, `hlmon_alert_rule.yaml`, `cosmos_sigs_alert_rule.yaml` 등) 을 직접 확인하여 정정. 핵심 차이: ① 라벨 키 `alertLevel` (snake X), ② `groups:` 래퍼 **없음** (parser 가 wrap 함), ③ 모든 룰에 `alertEvent`/`instance`/`target` 라벨 필수.

## 0. 출력 파일 형식

monitoring 레포의 parser 는 룰 yaml 을 `{groups: [.]}` 로 wrap 한 뒤 promtool check 한다. 따라서 본 contract / 실제 yaml 은 **단일 RuleGroup 노드** (즉 `name + rules:` 가 최상위) 로 작성한다:

```yaml
name: hyperliquid_vpub_rule
rules:
  - alert: VpubServiceDown
    expr: vpub_service_up == 0
    for: 1m
    labels:
      alertEvent: "vpub:service:down"
      alertLevel: "critical"
      instance: "{{ $labels.instance }}"
      target: "{{ $labels.target }}"
      chain: "hyperliquid"
    annotations:
      summary: "vpub: validator-publisher.service down"
      description: "{{ $labels.instance }} validator-publisher.service 1분+ inactive"
  ...
```

### 0.1 모든 룰 공통 라벨

| 키 | 값 | 출처 |
|---|---|---|
| `alertEvent` | 룰 식별자 (snake/콜론 구분, 예: `vpub:service:down`) | 본 contract 가 룰별 지정 |
| `alertLevel` | `critical`/`high`/`medium`/`low`/`disk` 중 하나 | 본 contract 가 룰별 지정 |
| `instance` | `"{{ $labels.instance }}"` | Prometheus 자동 |
| `target` | `"{{ $labels.target }}"` | scrape target 라벨 |
| `chain` | `"hyperliquid"` | 본 contract 고정 |

기존 룰들은 `target` 라벨이 거의 항상 존재. 우리도 동일 패턴 유지.

### 0.2 promtool 로컬 검증

```bash
# 원본 (parser 가 wrap 하기 전 형태)
promtool check rules monitoring/rules/hyperliquid_vpub_rule.yaml
```

기존 monitoring 레포 룰들도 모두 `groups:` 없이 직접 작성됐고 promtool 이 그대로 통과. parser 가 PR merge 시 `{groups: [.]}` wrap.

---

## 1. Tier 0 룰 (US1) — 4 건

```yaml
- alert: VpubServiceDown
  expr: vpub_service_up{disable_alarm!='true'} == 0
  for: 1m
  labels:
    alertEvent: "vpub:service:down"
    alertLevel: "critical"
    instance: "{{ $labels.instance }}"
    target: "{{ $labels.target }}"
    chain: "hyperliquid"
  annotations:
    summary: "vpub: validator-publisher.service down"
    description: "{{ $labels.instance }} validator-publisher.service 가 1분 이상 inactive — bridge/oracle vote 누락 위험."

- alert: VpubChildMissing
  expr: vpub_child_count{disable_alarm!='true'} < 3
  for: 30s
  labels:
    alertEvent: "vpub:visor:child_missing"
    alertLevel: "critical"
    instance: "{{ $labels.instance }}"
    target: "{{ $labels.target }}"
    chain: "hyperliquid"
  annotations:
    summary: "vpub: visor child 누락 ({{ $value }}/3)"
    description: "{{ $labels.instance }} visor child 수 = {{ $value }} (정상 3). bridge / oracle / outcome 중 일부 hang/사망."

- alert: VpubLogStale
  expr: (time() - vpub_component_log_mtime_seconds{disable_alarm!='true',component!="visor"}) > 300
  for: 2m
  labels:
    alertEvent: "vpub:log:stale"
    alertLevel: "high"
    instance: "{{ $labels.instance }}"
    target: "{{ $labels.target }}"
    chain: "hyperliquid"
  annotations:
    summary: "vpub {{ $labels.component }} 로그 5분+ 멈춤"
    description: "{{ $labels.instance }} {{ $labels.component }} 로그 mtime 5분+ 갱신 없음 — hang 가능."

- alert: VpubLogStaleLong
  expr: (time() - vpub_component_log_mtime_seconds{disable_alarm!='true',component!="visor"}) > 1800
  for: 1m
  labels:
    alertEvent: "vpub:log:stale_long"
    alertLevel: "critical"
    instance: "{{ $labels.instance }}"
    target: "{{ $labels.target }}"
    chain: "hyperliquid"
  annotations:
    summary: "vpub {{ $labels.component }} 로그 30분+ 멈춤"
    description: "{{ $labels.instance }} {{ $labels.component }} 30분+ hang — 즉시 점검."
```

> `disable_alarm!='true'` 는 기존 룰 (band/celestia/eth 등) 의 일관 컨벤션 — 인스턴스 단위로 알람 일시 끄기.

---

## 2. Tier 1 룰 (US2) — 9 건

```yaml
- alert: VpubBridgeRpcMajorityDown
  expr: sum by (instance) (vpub_bridge_rpc_up{disable_alarm!='true'}) < 4
  for: 5m
  labels:
    alertEvent: "vpub:bridge:rpc_quorum"
    alertLevel: "high"
    instance: "{{ $labels.instance }}"
    target: "{{ $labels.target }}"
    chain: "hyperliquid"
  annotations:
    summary: "vpub bridge: RPC quorum 위태"
    description: "메인넷 RPC 살아있는 수 = {{ $value }} (정상 ≥ 4)"

- alert: VpubBridgeRpcSingleDown
  expr: vpub_bridge_rpc_up{disable_alarm!='true'} == 0
  for: 10m
  labels:
    alertEvent: "vpub:bridge:rpc_down"
    alertLevel: "medium"
    instance: "{{ $labels.instance }}"
    target: "{{ $labels.target }}"
    chain: "hyperliquid"
  annotations:
    summary: "vpub bridge: RPC {{ $labels.name }} down"
    description: "{{ $labels.instance }} bridge voter 의 RPC {{ $labels.name }} 10분+ down"

- alert: VpubBridgeRpcDisagreement
  expr: increase(vpub_bridge_rpc_disagreement_total{disable_alarm!='true'}[15m]) > 5
  for: 2m
  labels:
    alertEvent: "vpub:bridge:disagreement"
    alertLevel: "high"
    instance: "{{ $labels.instance }}"
    target: "{{ $labels.target }}"
    chain: "hyperliquid"
  annotations:
    summary: "vpub bridge: RPC disagreement 빈발"
    description: "15분 내 RPC 합의 실패 {{ $value }} 건 — 잘못된 vote 위험"

- alert: VpubBridgeStaleVote
  expr: (time() - vpub_bridge_last_vote_success_unix{disable_alarm!='true'}) > 3600
  for: 5m
  labels:
    alertEvent: "vpub:bridge:stale_vote"
    alertLevel: "high"
    instance: "{{ $labels.instance }}"
    target: "{{ $labels.target }}"
    chain: "hyperliquid"
  annotations:
    summary: "vpub bridge: 1h+ 성공 vote 없음"
    description: "{{ $labels.instance }} 마지막 성공 bridge vote 이후 1h 경과"

- alert: VpubBridgeStaleVoteLong
  expr: (time() - vpub_bridge_last_vote_success_unix{disable_alarm!='true'}) > 21600
  for: 5m
  labels:
    alertEvent: "vpub:bridge:stale_vote_long"
    alertLevel: "critical"
    instance: "{{ $labels.instance }}"
    target: "{{ $labels.target }}"
    chain: "hyperliquid"
  annotations:
    summary: "vpub bridge: 6h+ 성공 vote 없음"
    description: "{{ $labels.instance }} 마지막 성공 bridge vote 이후 6h 경과 — 비정상"

- alert: VpubBridgeAllFail
  expr: |
    (
      increase(vpub_bridge_vote_total{status="ok",disable_alarm!='true'}[1h]) == 0
      and increase(vpub_bridge_vote_total{status="fail",disable_alarm!='true'}[1h]) > 0
    )
  for: 5m
  labels:
    alertEvent: "vpub:bridge:all_fail"
    alertLevel: "high"
    instance: "{{ $labels.instance }}"
    target: "{{ $labels.target }}"
    chain: "hyperliquid"
  annotations:
    summary: "vpub bridge: 1h 시도 vote 전부 실패"
    description: "{{ $labels.instance }} 최근 1h vote 시도가 있었지만 모두 실패"

- alert: VpubOracleStaleVote
  # R-004 ✅ 확정 (2026-05-23 testnet 9.7h): 평균 4.3s, max 102s.
  # 임계 300s = max 의 3x 안전마진.
  expr: (time() - vpub_oracle_last_vote_success_unix{disable_alarm!='true'}) > 300
  for: 5m
  labels:
    alertEvent: "vpub:oracle:stale_vote"
    alertLevel: "high"
    instance: "{{ $labels.instance }}"
    target: "{{ $labels.target }}"
    chain: "hyperliquid"
  annotations:
    summary: "vpub oracle: 5m+ 성공 vote 없음"
    description: "{{ $labels.instance }} 마지막 reference oracle update 이후 5m+ (정상 평균 4.3s)"

- alert: VpubOracleStaleVoteLong
  expr: (time() - vpub_oracle_last_vote_success_unix{disable_alarm!='true'}) > 1800
  for: 1m
  labels:
    alertEvent: "vpub:oracle:stale_vote_long"
    alertLevel: "critical"
    instance: "{{ $labels.instance }}"
    target: "{{ $labels.target }}"
    chain: "hyperliquid"
  annotations:
    summary: "vpub oracle: 30m+ 성공 vote 없음"
    description: "{{ $labels.instance }} reference oracle 사실상 정지 — oracle 미참여 손실"

- alert: VpubBridgeStateStuck
  # FR-012a — bridge-voter state.json 의 last_scanned_block 이 5분 이상 변하지 않음.
  # 로그가 멈춰도 state 만 보면 알 수 있음. 가장 강한 bridge health 시그널.
  expr: delta(vpub_bridge_state_last_scanned_block{disable_alarm!='true'}[5m]) == 0
  for: 2m
  labels:
    alertEvent: "vpub:bridge:state_stuck"
    alertLevel: "high"
    instance: "{{ $labels.instance }}"
    target: "{{ $labels.target }}"
    chain: "hyperliquid"
  annotations:
    summary: "vpub bridge: state 진행 멈춤 (5m)"
    description: "{{ $labels.instance }} bridge-voter-state.json last_scanned_block 5분 이상 변화 없음 — Arbitrum 스캔 정지"

- alert: VpubBridgeRpcAuthError
  # FR-005 보강 — RPC HTTP 401 (Must be authenticated!) 누적 감지.
  # testnet 5/22 에서 alchemy 키 만료로 실제 관찰. 키 교체 즉시 필요.
  expr: increase(vpub_bridge_rpc_check_total{status="auth_error",disable_alarm!='true'}[10m]) > 5
  for: 1m
  labels:
    alertEvent: "vpub:bridge:rpc_auth_error"
    alertLevel: "high"
    instance: "{{ $labels.instance }}"
    target: "{{ $labels.target }}"
    chain: "hyperliquid"
  annotations:
    summary: "vpub bridge: RPC {{ $labels.name }} 인증 실패 빈발"
    description: "10분 내 RPC 401 응답 {{ $value }}회 — 키 만료/오류 의심"

- alert: VpubOutcomePendingLong
  expr: vpub_outcome_slack_msg_24h{disable_alarm!='true'} > 5
  for: 30m
  labels:
    alertEvent: "vpub:outcome:pending"
    alertLevel: "medium"
    instance: "{{ $labels.instance }}"
    target: "{{ $labels.target }}"
    chain: "hyperliquid"
  annotations:
    summary: "vpub outcome: 미검토 액션 적체"
    description: "최근 24h outcome 채널 메시지 {{ $value }} 건 — 검토 진행 필요"

- alert: VpubSlackTokenInvalid
  expr: vpub_slack_api_ok{disable_alarm!='true'} == 0
  for: 5m
  labels:
    alertEvent: "vpub:slack:token_invalid"
    alertLevel: "critical"
    instance: "{{ $labels.instance }}"
    target: "{{ $labels.target }}"
    chain: "hyperliquid"
  annotations:
    summary: "vpub: Slack token 만료 / API down"
    description: "publisher 가 보내는 모든 슬랙 알람이 누락 중일 수 있음 (auth.test fail)"
```

---

## 3. Tier 2 룰 (US3) — 2 건

```yaml
- alert: VpubBinaryUpdateAvailable
  expr: (vpub_binary_remote_mtime_unix{disable_alarm!='true'} - vpub_binary_local_mtime_unix) > 3600
  for: 30m
  labels:
    alertEvent: "vpub:binary:update"
    alertLevel: "medium"
    instance: "{{ $labels.instance }}"
    target: "{{ $labels.target }}"
    chain: "hyperliquid"
  annotations:
    summary: "vpub: 새 publisher 바이너리 announced"
    description: "remote mtime > local +1h — 변경 사항 검토 후 수동 업그레이드 필요"

- alert: VpubBinaryRemoteCheckFail
  expr: vpub_binary_remote_check_ok{disable_alarm!='true'} == 0
  for: 1h
  labels:
    alertEvent: "vpub:binary:check_fail"
    alertLevel: "low"
    instance: "{{ $labels.instance }}"
    target: "{{ $labels.target }}"
    chain: "hyperliquid"
  annotations:
    summary: "vpub: binary URL HEAD 실패"
    description: "{{ $labels.instance }} 업그레이드 트래킹 비활성 — URL/네트워크 점검"
```

---

## 4. 룰 ↔ Acceptance Scenario 매트릭스

(변경 없음. spec.md US1/2/3 의 acceptance scenario 가 본 룰로 모두 커버 — 정정 전과 동일.)

| Scenario | 룰 |
|---|---|
| US1-1 (`systemctl stop` → critical) | VpubServiceDown |
| US1-2 (child 누락 30s → critical) | VpubChildMissing |
| US1-3 (로그 5분 stale → high) | VpubLogStale |
| US1-4 (정상화 → resolve) | alertmanager 의 `resolveMsg = true` 처리 |
| US2-1 (RPC quorum < 4 / 5m) | VpubBridgeRpcMajorityDown |
| US2-2 (vote 1h / 6h) | VpubBridgeStaleVote, VpubBridgeStaleVoteLong |
| US2-3 (disagreement 15m 5건) | VpubBridgeRpcDisagreement |
| US2-4 (oracle 2h) | VpubOracleStaleVote |
| US2-5 (outcome > 5 / 30m) | VpubOutcomePendingLong |
| US2-6 (slack auth.test 5m fail) | VpubSlackTokenInvalid |
| US3-1 (remote mtime > local + 1h) | VpubBinaryUpdateAvailable |
| US3-2 (HEAD 1h fail) | VpubBinaryRemoteCheckFail |

## 5. alertLevel 사용 통계 (총 15 룰)

- `critical` (PagerDuty + ddoa-critical): 5 룰
- `high` (ddoa-high): 6 룰
- `medium` (ddoa-low): 3 룰
- `low` (ddoa-low): 1 룰
- `disk`: 0 — node-exporter / 기존 룰이 커버

## 6. 변경 절차

1. 본 contract 부터 수정.
2. yaml 갱신 (`monitoring/rules/hyperliquid_vpub_rule_tier{0,1,2}.yaml` 또는 통합본).
3. 로컬 검증: `promtool check rules <file>` 통과 (parser wrap 전 형태로 그대로).
4. monitoring 레포 PR.
5. spec.md acceptance scenario 와 §4 표 동기화.
