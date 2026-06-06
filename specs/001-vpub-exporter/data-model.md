# Phase 1 Data Model — vpub-exporter

> 외부 인터페이스는 Prometheus 메트릭 (contracts/metrics.md). 본 문서는 exporter 내부에서 사용하는 도메인 엔티티의 형태를 정의.

## Component

| 필드 | 타입 | 의미 |
|---|---|---|
| name | enum: `visor` / `bridge-voter` / `reference-oracle-publisher` / `outcome-voter` | 컴포넌트 식별자 (메트릭 라벨로 사용) |
| log_dir | string | R-026 (HF README 2026-06-06): 절대경로, `<VPUB_COMPONENT_LOG_DIR>/<name>/` — 모든 4 컴포넌트 동일 구조. visor 도 `<...>/visor/`. (옛 R-001 의 visor==root 가정 폐기.) |
| latest_log_path | string | `log_dir` 안의 최신 파일 (mtime 가장 큼) |
| latest_log_mtime | unix sec | `stat(latest_log_path).mtime` |

상태 전이: 정상 / stale (5m+) / dead-long (30m+) — 알람 룰이 판단.

## RPCProvider

| 필드 | 타입 | 의미 |
|---|---|---|
| name | string | 메트릭 라벨 (e.g. `alchemy`) |
| url | string (secret) | env 로 주입, 메트릭에 노출 X |
| last_check_at | time | 마지막 헬스체크 시각 |
| last_check_ok | bool | 마지막 결과 |
| last_latency | float (sec) | 마지막 응답 시간 |

상태 전이: ok ↔ fail/timeout.

## VoteEvent (로그에서 추출)

| 필드 | 타입 | 의미 |
|---|---|---|
| component | enum `bridge` / `oracle` | 어느 컴포넌트의 vote 인지 |
| status | enum `ok` / `fail` | 결과 |
| at | time | 로그 timestamp (가용 시) 또는 매칭 시각 |

용도: counter 증가 + `last_vote_success_unix` 게이지 갱신.

## DisagreementEvent (로그에서 추출)

| 필드 | 타입 | 의미 |
|---|---|---|
| at | time | 매칭 시각 |

용도: `vpub_bridge_rpc_disagreement_total` counter 증가.

## OutcomeMessageStat

| 필드 | 타입 | 의미 |
|---|---|---|
| fetched_at | time | 마지막 Slack API 호출 시각 |
| msg_count_24h | int | 채널 최근 24h 메시지 수 |
| last_error | string (optional) | 호출 실패 시 사유 |

## BinaryVersionMarker

| 필드 | 타입 | 의미 |
|---|---|---|
| local_path | string | publisher 바이너리 절대경로 |
| local_mtime | unix sec | `stat().mtime` |
| remote_url | string | HEAD 대상 |
| remote_last_modified | unix sec | HTTP `Last-Modified` 파싱 |
| remote_check_ok | bool | 마지막 HEAD 성공 여부 |
| remote_checked_at | time | 마지막 HEAD 시각 |

## Cache 패턴

- 모든 위 엔티티는 collector 별 in-memory map 에 보관.
- `/metrics` 응답은 이 map 만 읽음 (외부 호출 0).
- 갱신은 collector 의 별도 goroutine 이 tick 마다 수행.
- 동시성: `sync.RWMutex` 또는 `atomic.Value` 로 read-mostly 보호.
