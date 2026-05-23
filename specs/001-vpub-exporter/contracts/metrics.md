# Contract: Prometheus Metrics (vpub-exporter)

> 본 문서는 vpub-exporter 가 `/metrics` 에 노출하는 모든 메트릭의 **공개 인터페이스**다.
> 메트릭 이름·타입·라벨이 바뀌면 monitoring 레포의 룰/대시보드가 깨진다 — 변경 시 contract 부터.

## 표기

- **Type**: Gauge / Counter / Histogram
- **Labels**: 라벨 이름과 cardinality 표기 (낮을수록 좋음)
- **Source**: 데이터를 어디서 얻는지 (systemd / proc / log file / RPC / Slack API / HTTP HEAD)
- **Refresh**: collector 가 새 값을 캐시에 채우는 주기 (`/metrics` 스크레이프 주기와는 독립)
- **FR**: spec.md 의 functional requirement 매핑

---

## A. Tier 0 — 프로세스 / 진행성 (US1)

### `vpub_service_up`
- **Type**: Gauge (0/1)
- **Labels**: 없음 (인스턴스 자체에 머신 라벨이 붙음)
- **Source**: systemd dbus `ActiveState == "active"` → 1, else 0
- **Refresh**: 5s
- **FR**: FR-001
- **Meaning**: v-publisher.service systemd active 상태.

### `vpub_child_count`
- **Type**: Gauge (int)
- **Labels**: 없음
- **Source**: systemd `MainPID` → `/proc/<pid>/task` 카운트 또는 `pgrep -P <pid>`
- **Refresh**: 10s
- **FR**: FR-002
- **Meaning**: visor 가 spawn 한 child 프로세스 개수. 정상 = 3 (bridge / oracle / outcome).

### `vpub_service_restart_total`
- **Type**: Counter
- **Labels**: 없음
- **Source**: systemd dbus `NRestarts` property (게이지지만 monotonic 증가 보장 → Counter 로 노출)
- **Refresh**: 30s
- **FR**: FR-004
- **Meaning**: systemd 가 v-publisher 를 재시작한 누적 횟수.

### `vpub_component_log_mtime_seconds`
- **Type**: Gauge (unix sec)
- **Labels**: `component=visor|bridge-voter|reference-oracle-publisher|outcome-voter` (4)
- **Source**: 컴포넌트 로그 디렉토리에서 최신 파일 `stat().mtime`
- **Refresh**: 30s
- **FR**: FR-003
- **Meaning**: 해당 컴포넌트가 마지막으로 로그를 기록한 시각. PromQL `time() - <metric>` 로 staleness 계산.

---

## B. Tier 1 — Bridge / Oracle / Outcome / Slack (US2)

### `vpub_bridge_rpc_up`
- **Type**: Gauge (0/1)
- **Labels**: `name=<rpc_name>` (≤ 7 메인넷 / ≤ 3 testnet)
- **Source**: 각 RPC URL 에 `eth_blockNumber` JSON-RPC 호출 (timeout 5s)
- **Refresh**: 30s
- **FR**: FR-005
- **Meaning**: 해당 RPC 의 마지막 헬스체크 응답 여부.

### `vpub_bridge_rpc_latency_seconds`
- **Type**: Histogram (buckets: 0.05, 0.1, 0.2, 0.5, 1, 2, 5, 10)
- **Labels**: `name=<rpc_name>`
- **Source**: 위 호출의 응답 시간
- **Refresh**: 30s
- **FR**: FR-005
- **Meaning**: RPC 별 latency 분포. p95/p99 PromQL 산출 가능.

### `vpub_bridge_rpc_check_total`
- **Type**: Counter
- **Labels**: `name=<rpc_name>`, `status=ok|fail|timeout`
- **Source**: RPC 헬스체크 결과 누적
- **Refresh**: 30s
- **FR**: FR-005
- **Meaning**: 디버깅용 누적 카운터.

### `vpub_bridge_rpc_disagreement_total`
- **Type**: Counter
- **Labels**: 없음
- **Source**: bridge-voter 로그 tail → 패턴 매칭 (default 패턴: `"disagree" | "mismatch" | "consensus failure"` — env `VPUB_DISAGREEMENT_PATTERNS` 로 오버라이드 가능)
- **Refresh**: 로그 tail (real-time)
- **FR**: FR-006
- **Meaning**: RPC 간 결과 불일치 이벤트 누적. 잘못된 vote 위험 시그널.

### `vpub_bridge_vote_total`
- **Type**: Counter
- **Labels**: `status=ok|fail`
- **Source**: bridge-voter 로그 tail → 패턴 매칭
- **Refresh**: 로그 tail
- **FR**: FR-006
- **Meaning**: bridge vote 제출 누적 결과.

### `vpub_bridge_last_vote_success_unix`
- **Type**: Gauge (unix sec)
- **Labels**: 없음
- **Source**: bridge-voter 로그의 마지막 ok vote timestamp. 초기값 = exporter start time.
- **Refresh**: 로그 tail
- **FR**: FR-007
- **Meaning**: 마지막 성공 bridge vote 시각. PromQL `time() - <metric>` 로 staleness.

### `vpub_oracle_vote_total`
- **Type**: Counter
- **Labels**: `status=ok|fail`
- **Source**: reference-oracle-publisher 로그 tail
- **Refresh**: 로그 tail
- **FR**: FR-008
- **Meaning**: oracle vote 제출 누적 결과.

### `vpub_oracle_last_vote_success_unix`
- **Type**: Gauge (unix sec)
- **Labels**: 없음
- **Source**: oracle 로그의 마지막 ok vote timestamp
- **Refresh**: 로그 tail
- **FR**: FR-007
- **Meaning**: 마지막 성공 oracle vote 시각.

### `vpub_outcome_log_warn_total`, `vpub_outcome_log_crit_total`
- **Type**: Counter (2개)
- **Labels**: 없음
- **Source**: outcome-voter 로그 tail → 패턴 매칭 (default `"\bwarn\b" | "WARN"` / `"\bcrit" | "ERROR"` — env override)
- **Refresh**: 로그 tail
- **FR**: FR-009
- **Meaning**: 로그 수준별 누적. Slack 1차 알람의 백업 카운터.

### `vpub_outcome_slack_msg_24h`
- **Type**: Gauge (int)
- **Labels**: 없음
- **Source**: Slack `conversations.history` of `VPUB_OUTCOME_CHANNEL`, oldest = now-24h
- **Refresh**: 5m (rate limit 보호)
- **FR**: FR-010
- **Meaning**: 최근 24h outcome 채널 메시지 수. 검토 적체 fallback.

### `vpub_slack_api_ok`
- **Type**: Gauge (0/1)
- **Labels**: 없음
- **Source**: Slack `auth.test` (200 + `ok=true` → 1)
- **Refresh**: 60s
- **FR**: FR-011
- **Meaning**: Slack 토큰 유효성. 0 = publisher 가 보내는 모든 슬랙 알람이 누락 중일 수 있음.

---

## C. Tier 2 — 업그레이드 (US3)

### `vpub_binary_local_mtime_unix`
- **Type**: Gauge (unix sec)
- **Labels**: 없음
- **Source**: `stat(VPUB_BINARY_PATH).mtime`
- **Refresh**: 60s
- **FR**: FR-012
- **Meaning**: 로컬 publisher 바이너리 파일의 최종 수정 시각.

### `vpub_binary_remote_mtime_unix`
- **Type**: Gauge (unix sec)
- **Labels**: 없음
- **Source**: HTTP HEAD `VPUB_BINARY_URL` → `Last-Modified` 헤더 파싱
- **Refresh**: 10m
- **FR**: FR-013
- **Meaning**: HF 가 게시한 최신 publisher 바이너리의 수정 시각.

### `vpub_binary_remote_check_ok`
- **Type**: Gauge (0/1)
- **Labels**: 없음
- **Source**: 위 HEAD 호출 성공 여부
- **Refresh**: 10m
- **FR**: FR-013
- **Meaning**: 업그레이드 트래킹 활성/비활성. 0 = URL 변경/네트워크 단절.

---

## D. Exporter 자기 메트릭

기본 `promhttp.Handler` 가 노출하는 `go_*`, `process_*` 외에:

### `vpub_exporter_collection_duration_seconds`
- **Type**: Histogram
- **Labels**: `collector=service|logmtime|bridge_rpc|vote_logs|outcome_logs|outcome_slack|slack_health|binary`
- **Source**: 각 collector 의 한 tick 소요 시간
- **Refresh**: tick 마다 관측
- **Meaning**: collector 별 성능 디버깅.

### `vpub_exporter_collection_errors_total`
- **Type**: Counter
- **Labels**: `collector=...`, `kind=timeout|api|parse|io`
- **Source**: collector 내부 에러
- **Meaning**: 외부 호출 실패 / 파싱 실패 추적.

---

## E. 라벨 / cardinality 정책

- **chain**, **network** 라벨은 메트릭에 **추가하지 않음** — monitoring 레포의 agent TOML 에서 인스턴스 라벨로 주입됨.
- **instance** 라벨도 prometheus 가 자동 부여 → 메트릭에서 명시 X.
- 새 라벨 추가 시 cardinality 폭증 위험 — 본 contract 수정 + 리뷰 필수.

---

## F. 메트릭 ↔ FR ↔ Alert rule 매트릭스

| 메트릭 | FR | 알람 룰 (alerts.md) |
|---|---|---|
| `vpub_service_up` | 001 | VpubServiceDown |
| `vpub_child_count` | 002 | VpubChildMissing |
| `vpub_service_restart_total` | 004 | (대시보드용, 알람 없음) |
| `vpub_component_log_mtime_seconds` | 003 | VpubLogStale, VpubLogStaleLong |
| `vpub_bridge_rpc_up` | 005 | VpubBridgeRpcMajorityDown, VpubBridgeRpcSingleDown |
| `vpub_bridge_rpc_latency_seconds` | 005 | (대시보드용) |
| `vpub_bridge_rpc_disagreement_total` | 006 | VpubBridgeRpcDisagreement |
| `vpub_bridge_vote_total` | 006 | VpubBridgeAllFail |
| `vpub_bridge_last_vote_success_unix` | 007 | VpubBridgeStaleVote, VpubBridgeStaleVoteLong |
| `vpub_oracle_vote_total` | 008 | (VpubOracleAllFail 후속 검토) |
| `vpub_oracle_last_vote_success_unix` | 007 | VpubOracleStaleVote |
| `vpub_outcome_log_warn_total` | 009 | (대시보드용) |
| `vpub_outcome_log_crit_total` | 009 | (Slack 1차 — 추가 룰 미적용) |
| `vpub_outcome_slack_msg_24h` | 010 | VpubOutcomePendingLong |
| `vpub_slack_api_ok` | 011 | VpubSlackTokenInvalid |
| `vpub_binary_local_mtime_unix` | 012 | VpubBinaryUpdateAvailable |
| `vpub_binary_remote_mtime_unix` | 013 | VpubBinaryUpdateAvailable, VpubBinaryRemoteCheckFail |
| `vpub_binary_remote_check_ok` | 013 | VpubBinaryRemoteCheckFail |
