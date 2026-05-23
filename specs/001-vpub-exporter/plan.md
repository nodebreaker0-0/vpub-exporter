# Implementation Plan: vpub-exporter

**Branch**: `001-vpub-exporter` | **Date**: 2026-05-23 | **Spec**: ./spec.md

**Input**: ./spec.md (User stories US1/US2/US3, FR-001..020, SC-001..008)

## Summary

hl-validator-publisher (visor + bridge-voter + reference-oracle-publisher + outcome-voter) 의 외부 모니터링 Prometheus exporter 를 Go 로 작성한다. publisher 와 같은 머신에 배치, port 8002 에서 `/metrics` 노출. B-Harvest 의 기존 `monitoring/` 레포 (parser + Prometheus + Alertmanager + Ansible) 에 새 agent TOML + 공통 rule yaml 을 추가해 자동 라우팅에 통합.

기능은 Tier 0 (process/child/log mtime) → Tier 1 (RPC/vote/outcome/slack) → Tier 2 (upgrade tracking) 순으로 한 코드베이스에 통합 구현. 1 binary / 1 systemd / 1 endpoint. publisher 에 대한 모든 영향은 read-only.

## Technical Context

**Language/Version**: Go 1.21+
**Primary Dependencies**: `github.com/prometheus/client_golang/prometheus`, `github.com/prometheus/client_golang/prometheus/promhttp`. systemd 연동은 dbus (`github.com/coreos/go-systemd/v22/dbus`) 또는 `os/exec` 로 `systemctl is-active` 호출.
**Storage**: stateless. 모든 메트릭은 in-memory. 캐시 TTL 은 collector 별 scrape 주기.
**Testing**: `go test` + table-driven tests. 로그 패턴 매칭, mtime 계산, Slack API 응답 파싱은 단위 테스트 필수. 통합 테스트는 testdata 디렉토리의 가짜 로그 파일 + httptest 서버.
**Target Platform**: Linux amd64 (publisher 머신, Tokyo)
**Project Type**: single CLI binary (Prometheus exporter)
**Performance Goals**: `/metrics` P95 < 200ms (FR-017, SC-003). collector tick 30s 기본, RPC 헬스/Slack auth.test 는 60s.
**Constraints**: RSS < 100MB, CPU < 5% / 1 core (SC-004). 외부 호출 timeout: Slack 5s / RPC 5s / HTTP HEAD 10s. `/metrics` 응답은 캐시된 값으로 즉시 (FR-017).
**Scale/Scope**: 단일 publisher 머신, RPC 7개 (메인넷), 컴포넌트 4종, 메트릭 20여 개, 룰 13개.

## Constitution Check

> Gate: pass before Phase 0 research. Re-check after Phase 1 design.

| 원칙 | 통과 | 비고 (운영 검증 2026-05-23 포함) |
|---|---|---|
| I. Outside-the-Box Monitoring | ✅ | publisher 와 별도 프로세스. publisher dies 시 `VpubServiceDownTestnet` 정확히 발화 (실측 검증) |
| II. No Side Effects on Publisher | ✅ | read-only 확정 (FR-016). QS-5 검증 — publisher 파일 변경 0 |
| III. Monitoring 레포 Convention | ✅ | metric prefix `vpub_`, alertLevel 5종, promtool + yamllint check gate (FR-018, 019). testnet/mainnet 분기 22 rules |
| IV. Secrets Never in Code/Metrics | ✅ | env-only (FR-015). `/metrics` + journal 시크릿 grep 0 hit |
| V. Non-Blocking Scrape | ✅ | 외부 호출 별도 goroutine + cache (FR-017). p95 ~4ms (200ms 임계의 2%) |
| VI. Time-Sensitive Truth from Logs | ✅ | 패턴은 env override 가능 (FR-020). R-003 testnet 9.7h 실로그로 default 확정 |
| VII. Tier Gating | ✅ | Tier 0 MVP testnet 가동 → 안정 확인 후 Tier 1 + Tier 2 통합. opt 3 stub 모드 |

위반 항목 없음. Complexity Tracking 비움. 운영 발견 → constitution Operational Constraints 에 PrivateTmp=no / dbus Service interface 보강 (2026-05-23).

## Project Structure

### Documentation (this feature)

```text
specs/001-vpub-exporter/
├── spec.md              # WHAT / WHY
├── plan.md              # 이 파일 — HOW
├── research.md          # Phase 0: NEEDS CLARIFICATION 해소
├── data-model.md        # Phase 1: Component / RPC / VoteEvent 등 엔티티 상세
├── contracts/
│   ├── metrics.md       # 메트릭 인터페이스 명세 (이름, 타입, 라벨, 의미)
│   └── alerts.md        # alert rule yaml 명세 (룰 13개)
├── quickstart.md        # Phase 1: testnet 가동 + 검증 시나리오
├── checklists/
│   └── requirements.md  # spec quality
└── tasks.md             # Phase 2: __SPECKIT_COMMAND_TASKS__ 가 채움
```

### Source Code (vpub-exporter 폴더 안)

```text
vpub-exporter/
├── CLAUDE.md                        # agent 진입 컨텍스트 (slim)
├── README.md                        # 운영자용 README (Phase 1 산출)
├── go.mod, go.sum
├── Makefile                         # build / test / promtool-check
├── cmd/
│   └── vpub-exporter/
│       └── main.go                  # entry, flag/env, HTTP server, signal handling
├── internal/
│   ├── config/                      # env / flag 정의 + 검증
│   ├── systemd/                     # is-active / NRestarts / MainPID 조회
│   ├── procs/                       # child count (MainPID → /proc/<pid>/task)
│   ├── logfs/                       # 로그 디렉토리 stat / 회전 인식
│   ├── logtail/                     # 파일 tail + 패턴 매칭
│   ├── rpc/                         # Arbitrum RPC 헬스체크 (eth_blockNumber)
│   ├── slackapi/                    # auth.test / conversations.history
│   ├── binary/                      # HTTP HEAD 폴링
│   └── collectors/                  # Prometheus collector 모음
│       ├── service.go               # FR-001, 002, 004
│       ├── logmtime.go              # FR-003
│       ├── bridge_rpc.go            # FR-005
│       ├── vote_logs.go             # FR-006, 007, 008 (bridge + oracle)
│       ├── outcome_logs.go          # FR-009
│       ├── outcome_slack.go         # FR-010
│       ├── slack_health.go          # FR-011
│       └── binary.go                # FR-012, 013
├── systemd/
│   └── vpub-exporter.service        # 운영 unit (EnvironmentFile=/etc/vpub-exporter.env)
├── env/
│   └── vpub-exporter.env.example    # 시크릿 placeholder
├── testdata/
│   ├── logs/                        # 가짜 로그 fixture
│   └── slack/                       # Slack API 응답 fixture
└── tests/
    ├── collectors/                  # collector 별 단위 테스트
    ├── logtail/                     # 패턴 매칭 단위 테스트
    └── integration/                 # /metrics e2e (httptest)
```

**Structure Decision**:
- 단일 Go 모듈 (`github.com/bharvest/vpub-exporter` — 정확한 path 는 사용자 컨벤션 확인 후 확정).
- collector 별 파일 분리 (코드 검토 단위 = FR 단위 매핑).
- 외부 의존 (`systemd`, `rpc`, `slackapi`, `binary`) 는 interface 로 추상화 + mock 으로 단위 테스트.
- monitoring 레포 통합용 파일 2개는 본 폴더에서 작성 후 **수동 복사 → monitoring 레포 PR**:
  - `monitoring/config/agents/Main_hyperliquid_VPUB_<region>.toml` (사용자가 IP 채움)
  - `monitoring/config/rules/hyperliquid_vpub_rule.yaml` (contracts/alerts.md 의 룰들)

### Repository Integration

monitoring 레포는 **별도 git repo** — 본 폴더와 별개 PR 라이프사이클.

- 본 PR(vpub-exporter 코드): `/Users/ijeseon/hl-agent/validator/vpub-exporter/` 에서 작업
- 후속 PR(monitoring 통합): `validator/monitoring/` 레포에 위 2개 파일 추가 + `promtool check rules` CI 통과

## Complexity Tracking

| Violation | Why Needed | Simpler Alternative Rejected Because |
|---|---|---|
| — | — | — |

위반 없음.

## Phases

### Phase 0 — Research (research.md)

- 가동 첫날 확정 필요한 7개 NEEDS CLARIFICATION 해소 (로그 디렉토리, 컴포넌트별 파일명 패턴, vote/disagreement/warn/crit 로그 라인 문구, oracle publish 주기, RPC quorum, 메인넷 binary URL).
- 의존성 best practices: prometheus client_golang 사용 패턴 (gauge / counter / histogram, registry, promhttp), go-systemd dbus 사용법.

### Phase 1 — Design & Contracts

- `data-model.md`: Component / RPCProvider / VoteEvent / OutcomeMessage / BinaryVersionMarker 엔티티 정의.
- `contracts/metrics.md`: 메트릭 20여 개의 인터페이스 명세 (이름, 타입, 라벨, 단위, 의미, 수집 방법).
- `contracts/alerts.md`: 알람 룰 13개 yaml.
- `quickstart.md`: testnet 가동 + Tier 0/1/2 별 검증 시나리오 (SC-001..008 매핑).
- 본 plan.md 의 Constitution Check 재실행 — 변경 시 표 갱신.

### Phase 2 — Tasks (tasks.md)

`/speckit.tasks` 가 생성. 본 plan + spec + contracts 기반:

- Phase 1 Setup: go.mod, Makefile, CI workflow
- Phase 2 Foundational: cmd/main.go, internal/config, /metrics handler, systemd unit
- Phase 3 US1 (P1, MVP): service / logmtime / procs collectors + Tier 0 룰
- Phase 4 US2 (P2): bridge_rpc / vote_logs / outcome_logs / outcome_slack / slack_health + Tier 1 룰
- Phase 5 US3 (P3): binary collector + Tier 2 룰
- Phase N Polish: README, env 템플릿, monitoring 레포용 두 파일

## Key Rules

- 메트릭/알람 변경은 `contracts/` 에 먼저 반영 후 코드.
- spec.md 의 FR-NNN 와 코드 파일 / 테스트 / 알람 룰을 cross-ref (주석에 `// FR-005` 형식).
- 환경변수는 `internal/config` 한 곳에서만 읽음. 다른 패키지는 struct 주입.
- 시크릿 의심되는 값은 zap/slog 의 핵심 필드 마스킹 사용 (`token=*****`).
- 가동 첫날 확정 항목은 quickstart.md 의 체크리스트로 한 번 더 확인.
