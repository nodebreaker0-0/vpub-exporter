---
description: "vpub-exporter implementation tasks (Tier 0+1+2)"
---

# Tasks: vpub-exporter

**Input**: `/specs/001-vpub-exporter/{spec.md, plan.md, research.md, data-model.md, contracts/{metrics.md, alerts.md}, quickstart.md}`

**Prerequisites**: 모두 작성 완료 (Phase A — 2026-05-23)

**Tests**: 포함. spec.md FR-005~011 의 패턴 매칭 / 외부 호출 파싱은 단위 테스트 필수 (plan.md Testing 항목).

**Organization**: phase 별 + user story (US1/2/3) 라벨로 묶음. US1 만으로도 MVP.

## Format

`[T###] [P?] [Story] Description (path)`

- `[P]` — 다른 파일·의존성 없어 병렬 가능
- `[USx]` — spec.md user story 매핑 (US1=P1, US2=P2, US3=P3)
- 모든 경로는 `vpub-exporter/` 기준 상대경로

---

## Phase 1 — Setup (Shared Infrastructure)

**Purpose**: Go 모듈, 빌드, CI 스켈레톤. 의존성 없음 — 가장 먼저.

- [x] **T001** Go 모듈 초기화 (`go.mod`). module path 는 research.md R-008 확정값 사용 (가설: `github.com/bharvest/vpub-exporter`). `go 1.21` 명시.
- [x] **T002** [P] `.gitignore` — `*.env`, `bin/`, `vendor/`, `coverage.out`, `*.tmp`
- [x] **T003** [P] `Makefile` — target: `build` (linux/amd64), `test`, `vet`, `lint`, `promtool-check`, `clean`
- [x] **T004** [P] `README.md` 스켈레톤 (운영자용. Phase N 에서 채움)
- [x] **T005** [P] `.github/workflows/ci.yml` — go build + go test + promtool check rules. 푸시 / PR 트리거.

**Checkpoint 1**: `make build` 가 빈 main 으로라도 동작 + CI 가 push 시 실행.

---

## Phase 2 — Foundational (Blocking Prerequisites)

**Purpose**: collector / 외부 의존성 인터페이스 + HTTP server + systemd unit. user story phase 들의 전제.

**⚠️ CRITICAL**: 이 phase 끝나기 전엔 Phase 3+ 시작 금지.

### Config / Entry

- [x] **T010** `internal/config/config.go` — env / flag 파싱 + 검증. plan.md §Project Structure 의 env 목록 전체 (`VPUB_LOG_DIR`, `VPUB_BINARY_PATH`, `VPUB_BINARY_URL`, `VPUB_BRIDGE_RPC_NAMES`, `VPUB_RPC_<NAME>_URL`, `VPUB_SLACK_BOT_TOKEN`, `VPUB_OUTCOME_CHANNEL`, `VPUB_LOG_*_PATTERNS`, `VPUB_VOTE_*_PATTERNS`, `VPUB_DISAGREEMENT_PATTERNS`, `--listen-addr`, `--scrape-interval`, `--service-name`).
- [x] **T011** [P] `internal/config/config_test.go` — env override / default / validation 검증.
- [x] **T012** `cmd/vpub-exporter/main.go` — entry. flag/env 로드 → registry → collector 등록 → `http.Server` 띄우고 `/metrics` 핸들러. SIGTERM/SIGINT graceful shutdown (외부 호출 goroutine 도 context 로 취소).

### Exporter Self / Registry

- [x] **T013** `internal/collectors/base.go` — collector 공통: 캐시 mutex 패턴, `vpub_exporter_collection_duration_seconds`, `vpub_exporter_collection_errors_total` 등록 helper.
- [x] **T014** [P] `internal/collectors/base_test.go` — 캐시 / 에러 카운트 동작.

### 외부 의존성 인터페이스 (stub — Phase 3+ 에서 채움)

- [x] **T015** [P] `internal/systemd/systemd.go` — `interface ServiceProbe { IsActive() (bool, error); MainPID() (int, error); NRestarts() (int, error) }`. 실제 구현은 T030.
- [x] **T016** [P] `internal/procs/procs.go` — `interface ChildLister { CountChildren(parentPID int) (int, error) }`. 실제 구현은 T031.
- [x] **T017** [P] `internal/logfs/logfs.go` — `interface LogDirStat { LatestMtime(componentDir string) (time.Time, string, error) }`. 실제 구현은 T032.
- [x] **T018** [P] `internal/logtail/logtail.go` — `interface Tailer { Subscribe(file string, patterns []*regexp.Regexp) <-chan Match }`. 실제 구현은 T040.
- [x] **T019** [P] `internal/rpc/rpc.go` — `interface RPCProbe { Probe(ctx, url string) (latency time.Duration, err error) }` (eth_blockNumber). 실제 구현은 T041.
- [x] **T020** [P] `internal/slackapi/slackapi.go` — `interface Slack { AuthTest(ctx, token string) (bool, error); History24h(ctx, token, channelID string) (int, error) }`. 실제 구현은 T042.
- [x] **T021** [P] `internal/binary/binary.go` — `interface Probe { LocalMtime(path string) (time.Time, error); RemoteLastModified(ctx, url string) (time.Time, error) }`. 실제 구현은 T050.

### Systemd unit / env

- [x] **T022** [P] `systemd/vpub-exporter.service` — `EnvironmentFile=/etc/vpub-exporter.env`, `User=admin`, `ProtectSystem=full`, `ReadOnlyPaths=/home/admin/v-publisher`, `RestrictSUIDSGID=yes`, `NoNewPrivileges=yes`, `LimitNOFILE=65536`. constitution Operational Constraints + spec.md QS-5 직결.
- [x] **T023** [P] `env/vpub-exporter.env.example` — 모든 env 변수의 placeholder + 주석. 시크릿 자리에 `# CHANGE_ME` 표시. config.json 참조 금지 주석.

### `/metrics` 동작 검증

- [x] **T024** `tests/integration/metrics_endpoint_test.go` — httptest 로 `/metrics` 호출, prefix `vpub_` 만 등장, exporter self metrics 응답 확인. 모든 collector 가 빈 cache 일 때도 panic 없음.

**Checkpoint 2**: `make build && ./bin/vpub-exporter --listen-addr :8002` 로 빈 exporter 가 떠서 `vpub_exporter_*` 만 노출. SIGTERM 정상 종료.

---

## Phase 3 — User Story 1 (P1, MVP) — Publisher 가 죽으면 즉시 안다

**Goal**: spec.md FR-001~004 + Tier 0 알람 4건 가동. publisher 정지 / child 죽음 / 로그 hang 을 외부에서 감지.

**Independent Test**: quickstart.md QS-1.1~1.3.

### 외부 의존성 실제 구현

- [x] **T030** [P] [US1] `internal/systemd/systemd_dbus.go` — `go-systemd/v22/dbus` 로 `GetUnitProperties("<service>.service")` 호출, `ActiveState`/`MainPID`/`NRestarts` 추출. research.md R-010 결정.
- [x] **T031** [P] [US1] `internal/procs/procs_proc.go` — `/proc/<pid>/task` 디렉토리 엔트리 수 카운트. 또는 fallback 으로 `pgrep -P`.
- [x] **T032** [P] [US1] `internal/logfs/logfs_os.go` — `ReadDir` + filename glob (`YYYYMMDD` 형식 가정 — R-002 확정 후 정정) + 최신 mtime 추출.

### Collectors

- [x] **T033** [US1] `internal/collectors/service.go` — `vpub_service_up`, `vpub_child_count`, `vpub_service_restart_total` 정의/등록. 5s + 10s + 30s 주기로 systemd/procs 호출 후 캐시 갱신. FR-001/002/004 매핑. // FR 주석 필수.
- [x] **T034** [US1] `internal/collectors/logmtime.go` — `vpub_component_log_mtime_seconds{component=...}`. 4개 컴포넌트 디렉토리 각각 stat. 30s 주기. FR-003.

### 테스트

- [x] **T035** [P] [US1] `internal/systemd/systemd_dbus_test.go` — mock dbus (또는 fake `Conn`) 로 다양한 상태 (active/inactive/failed) 처리.
- [x] **T036** [P] [US1] `internal/procs/procs_proc_test.go` — testdata 에 `/proc` 시뮬레이션 디렉토리, 3/0/4 children 케이스.
- [x] **T037** [P] [US1] `internal/logfs/logfs_os_test.go` — fixture 로그 디렉토리 (회전된 여러 파일 + 동일 시간대 중복) 에서 최신 파일 정확 식별.
- [x] **T038** [P] [US1] `internal/collectors/service_test.go` — mock interface 주입, 메트릭 값 검증.
- [x] **T039** [P] [US1] `internal/collectors/logmtime_test.go` — fake LogDirStat 으로 4 컴포넌트 동시 검증.

### Tier 0 alert rules

- [x] **T040X** [US1] `monitoring/rules/hyperliquid_vpub_rule_tier0.yaml` — contracts/alerts.md §1 의 4 룰 (VpubServiceDown, VpubChildMissing, VpubLogStale, VpubLogStaleLong). 본 파일은 vpub-exporter 폴더 안에 우선 작성 후 Phase N 에서 monitoring 레포로 복사.
- [x] **T041X** [US1] `Makefile` 의 `promtool-check` target 으로 위 yaml 통과 확인.

**Checkpoint 3** (MVP): testnet 가동 후 quickstart QS-1.1, QS-1.2, QS-1.3 모두 합격. 이 시점에 monitoring 레포 PR (Tier 0 only) 가능.

---

## Phase 4 — User Story 2 (P2) — 컴포넌트 본업이 끝까지 가는지 본다

**Goal**: FR-005~011 + Tier 1 알람 9건. RPC / vote / outcome / Slack health.

**⚠️ Prerequisite**: Phase 3 의 Foundational 부분이 안정 (Tier 0 가 1주 false-positive <10%).

### 외부 의존성 실제 구현

- [x] **T040** [P] [US2] `internal/logtail/logtail_poll.go` — 파일 tail (open → seek end → 30s 마다 새 줄 읽기 + 매 30s rotation 체크). 패턴 매칭은 `regexp` 컴파일 후 cache. research.md R-011.
- [x] **T041** [P] [US2] `internal/rpc/rpc_http.go` — `net/http` 로 JSON-RPC `{"method":"eth_blockNumber"}` 호출. 5s timeout. latency 반환. R-012 (직접 구현).
- [x] **T042** [P] [US2] `internal/slackapi/slackapi_http.go` — `auth.test`, `conversations.history` 두 endpoint 만. Bearer token 헤더. JSON 응답 파싱 (`ok` 필드, messages 배열 길이). R-012.

### Collectors

- [x] **T043** [US2] `internal/collectors/bridge_rpc.go` — `vpub_bridge_rpc_up{name}`, `vpub_bridge_rpc_latency_seconds{name}` (Histogram), `vpub_bridge_rpc_check_total{name,status}`. 30s 주기. config 에서 받은 RPC 이름 리스트 순회. FR-005.
- [x] **T044** [US2] `internal/collectors/vote_logs.go` — bridge + oracle 공용 모듈. logtail 구독 → 패턴 매칭으로 `vpub_bridge_vote_total{status}`, `vpub_oracle_vote_total{status}`, `vpub_bridge_last_vote_success_unix`, `vpub_oracle_last_vote_success_unix`, `vpub_bridge_rpc_disagreement_total`. FR-006~008.
- [x] **T045** [US2] `internal/collectors/outcome_logs.go` — outcome-voter 로그 tail → `vpub_outcome_log_warn_total`, `vpub_outcome_log_crit_total`. FR-009.
- [x] **T046** [US2] `internal/collectors/outcome_slack.go` — Slack `conversations.history` 5분 주기 → `vpub_outcome_slack_msg_24h`. rate limit (429) 시 직전 값 유지 + error counter 증가. FR-010.
- [x] **T047** [US2] `internal/collectors/slack_health.go` — Slack `auth.test` 60s 주기 → `vpub_slack_api_ok`. FR-011.

### 테스트

- [x] **T048** [P] [US2] `internal/logtail/logtail_poll_test.go` — testdata 의 회전된 로그 파일 (어제/오늘) 에서 신/구 줄 정확히 emit, 회전 감지.
- [x] **T049** [P] [US2] `internal/rpc/rpc_http_test.go` — httptest 서버로 정상/timeout/500 케이스.
- [x] **T050** [P] [US2] `internal/slackapi/slackapi_http_test.go` — fixture JSON 응답으로 auth.test ok/fail, history count 계산.
- [x] **T051** [P] [US2] `internal/collectors/bridge_rpc_test.go` — mock RPCProbe 로 7개 중 3 down 케이스.
- [x] **T052** [P] [US2] `internal/collectors/vote_logs_test.go` — testdata 로그 fixture (bridge / oracle 정상 / 실패 / disagreement 혼합).
- [x] **T053** [P] [US2] `internal/collectors/outcome_logs_test.go`, `outcome_slack_test.go`, `slack_health_test.go` — 각 mock 으로 카운트 검증.

### Tier 1 alert rules

- [x] **T054** [US2] `monitoring/rules/hyperliquid_vpub_rule_tier1.yaml` — contracts/alerts.md §2 의 9 룰 (VpubBridgeRpcMajorityDown, VpubBridgeRpcSingleDown, VpubBridgeRpcDisagreement, VpubBridgeStaleVote, VpubBridgeStaleVoteLong, VpubBridgeAllFail, VpubOracleStaleVote, VpubOutcomePendingLong, VpubSlackTokenInvalid). R-004/R-005 확정 후 임계 조정.
- [x] **T055** [US2] promtool check 통과.

**Checkpoint 4**: quickstart QS-2.1~2.5 합격. Tier 1 룰 PR.

---

## Phase 5 — User Story 3 (P3) — 새 publisher 바이너리 announce 트래킹

**Goal**: FR-012~013 + Tier 2 알람 2건.

### 외부 의존성 + Collector

- [x] **T060** [P] [US3] `internal/binary/binary_http.go` — `os.Stat(localPath).ModTime()` + `http.Head(url)` → `Last-Modified` 헤더 파싱. 10s timeout.
- [x] **T061** [US3] `internal/collectors/binary.go` — `vpub_binary_local_mtime_unix`, `vpub_binary_remote_mtime_unix`, `vpub_binary_remote_check_ok`. local=60s, remote=10m 주기. FR-012/013.

### 테스트

- [x] **T062** [P] [US3] `internal/binary/binary_http_test.go` — httptest 서버에 `Last-Modified` 설정 / 404 / timeout 케이스.
- [x] **T063** [P] [US3] `internal/collectors/binary_test.go` — mock Probe.

### Tier 2 alert rules

- [x] **T064** [US3] `monitoring/rules/hyperliquid_vpub_rule_tier2.yaml` — VpubBinaryUpdateAvailable, VpubBinaryRemoteCheckFail.

**Checkpoint 5**: quickstart QS-3.1, QS-3.2 합격.

---

## Phase N — Polish / Cross-Cutting

**Purpose**: 운영 배포 + 문서 + 통합.

### Monitoring 레포 통합 파일

- [x] **T070** [P] `monitoring/agents/Main_hyperliquid_VPUB_<region>.toml` — `vpub-exporter` job + `node-exporter` job 2개. `<IP>` placeholder. labels `chain=hyperliquid, network=mainnet`. (사용자가 IP / region 채움). 본 파일은 우리 폴더에 우선 작성 후 monitoring 레포 PR 시 복사.
- [x] **T071** `monitoring/rules/hyperliquid_vpub_rule.yaml` — Tier 0+1+2 합본 (T040X + T054 + T064 통합). 단일 group `hyperliquid_vpub`. promtool check 통과.
- [ ] **T072** [P] `monitoring/README.md` 안내 (위 파일들이 어디서 왔는지, 어떻게 업데이트하는지).

### README / 운영 문서

- [x] **T073** [P] `README.md` 완성 — 설치 / 빌드 / 환경변수 / systemd / 트러블슈팅 / 메트릭 목록 (contracts/metrics.md 링크).
- [x] **T074** [P] `env/vpub-exporter.env.example` 최종 (모든 R-001~007 확정값 반영).

### 통합 테스트

- [ ] **T075** [P] `tests/integration/full_metrics_test.go` — 모든 collector 활성화 + mock 외부 의존성 + `/metrics` 응답에서 모든 메트릭 prefix `vpub_` 확인.
- [ ] **T076** [P] `tests/integration/secrets_leak_test.go` — env 에 더미 token 주입 → `/metrics` 본문에 0 hit + log 출력에 0 hit. **Constitution IV 회귀 게이트**.
- [ ] **T077** `Makefile` 에 `make verify` target — `test + vet + promtool-check + secrets_leak` 한 번에.

### Quickstart 합격 마무리

- [ ] **T078** quickstart.md QS-4 (성능) 측정 후 결과 본 task 에 기록. SC-003/004 합격 확인.
- [ ] **T079** quickstart.md QS-5 (read-only) 검증 + lsof / strace 로 fopen 모드 O_RDONLY 100% 확인.
- [ ] **T080** quickstart.md QS-6 (Constitution 회귀) 6개 체크 모두 ✅.

---

## Phase 6 — Per-component binary tracking (R-019, 2026-05-24)

**Trigger**: 2026-05-24 운영 로그 분석에서 visor 가 `/<child>/active` 를 polling 하여 child × 3 (bridge-voter / outcome-voter / reference-oracle-publisher) 을 자체 download 함을 발견. visor URL 단일 추적으로는 child 업데이트/실패 감지 불가.

**Resolution**: visor 는 HEAD 추적 (사람 install 시그널), child × 3 은 visor 로그의 `INFO visor: downloading new binary` 라인 시각과 file mtime 비교. download 성공/실패 모두 자동 resolve.

- [x] **T081** [US3] `internal/config/config.go` — `BinaryTargets map[ComponentName]string` 추가, legacy `VPUB_BINARY_PATH` 흡수, `VPUB_BINARY_TARGETS` 신규 env 파싱 (unknown component 거부).
- [x] **T082** [US3] `internal/collectors/binary.go` — `local_mtime_unix` / `remote_mtime_unix` / `remote_check_ok` 모두 `{component}` GaugeVec 으로. remote 는 visor 만. 한 component 실패가 다른 component 막지 않게 loop.
- [x] **T083** [US3] `internal/collectors/download_logs.go` 신규 — visor 로그 패턴 `INFO visor: downloading new binary self.binary_name="<child>"` → `vpub_binary_download_started_unix{component=<child>}` gauge. 4 component 외 label cardinality 가드.
- [x] **T084** [US3] `internal/collectors/{binary,download_logs}_test.go` — multi-component happy path / per-component error isolation / cardinality guard / log pattern fidelity (production 라인으로 fixture).
- [x] **T085** [US3] `monitoring/rules/hyperliquid_vpub_rule_tier2.yaml` — `VpubBinaryUpdateAvailable` → `VpubVisorBinaryUpdateAvailable` (medium, `component="visor"` matcher) + `VpubChildBinaryDownloadFailed` (high, `download_started_unix - local_mtime > 60`). 메시지 summary 한 줄에 `humanizeTimestamp + humanizeDuration` 압축 (monitoring alarmer slack-only summary 호환).
- [x] **T086** [US3] 통합본 + monitoring 레포 sync + parser-wrap promtool check (26 rules pass).
- [x] **T087** [US3] spec.md / research.md / contracts/{metrics,alerts}.md / README.md / quickstart.md QS-3 / env.example 백포트.
- [x] **T088** [US3] testnet 실측 검증:
  - VpubVisorBinaryUpdateAvailable firing → `:red_circle: ... HF Last-Modified 2026-05-22 03:50:10 UTC (local 보다 20594d ...)` (ddoa-low)
  - VpubChildBinaryDownloadFailed firing → `:warning: vpub: bridge-voter 다운로드 실패 — visor download 로그 후 20596d ...` (ddoa-high)
  - 양쪽 알람 firing 후 file mtime 원복 시 자동 resolve.
  - 운영 발견: ddoa-high 채널에 vo_slack_bot invite 필요했음.

**Checkpoint 6**: ✅ Phase 6 합격. visor + 3 child 모두 추적, 사람 액션과 visor 자동 동기화 실패가 의미별로 분리된 알람.

---

## Dependencies & Execution Order

### Phase 의존성

```
Phase 1 (Setup)              — 즉시 시작 가능
   ↓
Phase 2 (Foundational)       — Phase 1 후, 다음 모든 phase BLOCK
   ↓
   ├─ Phase 3 (US1, MVP)     — 독립 출시 가능
   ├─ Phase 4 (US2)          — Phase 3 머지 후 권장 (Tier 0 안정 확인)
   └─ Phase 5 (US3)          — Phase 4 와 병렬 가능
        ↓
   Phase N (Polish)          — Phase 3~5 모두 끝나면
```

### 사용자 결정: Tier 0+1+2 한 PR

CLAUDE.md §사용자 컨펌 #4 에 따라 **단일 PR**로 통합. Phase 3 끝낸 후 잠시 멈추고 사용자 검토 → 합쳐서 Phase 4/5/N 한꺼번에 진행.

### Story 내부 순서

각 user story 안에서:

```
인터페이스 stub (Phase 2)
       ↓
실제 구현 (T030~ / T040~ / T060~)
       ↓
collector 구현 (T033~ / T043~ / T061)
       ↓
단위 테스트
       ↓
alert rule yaml + promtool check
```

### Parallel 기회

| 단계 | 병렬 가능 [P] task |
|---|---|
| Phase 1 | T002, T003, T004, T005 |
| Phase 2 | T011, T014, T015~T021 (인터페이스 stub 들), T022, T023 |
| Phase 3 | T030~T032 (외부 의존성), T035~T039 (테스트), T040X |
| Phase 4 | T040~T042 (외부 의존성), T048~T053 (테스트) |
| Phase 5 | T060, T062, T063 |
| Phase N | T070, T072~T076 |

---

## Implementation Strategy

### MVP First (Phase 3 끝)

1. Phase 1 → 2 → 3 직렬
2. **STOP**: testnet 가동 + QS-1.x 검증 + 1일 모니터링
3. false-positive 0 / 누락 0 확인 → 사용자 GO
4. Phase 4, 5, N 진행
5. monitoring 레포 단일 PR (Tier 0+1+2)
6. 메인넷 가동

### Constitution Gate (매 phase 끝)

- 시크릿 grep 0 hit (`grep -rE "xoxb|0x[a-f0-9]{40}" --include=*.go`)
- 자동 액션 코드 0 hit (`grep -E "Restart|Vote|Unjail" cmd/ internal/`)
- alert_level 5종 외 0 hit (`grep -oE "alert_level: [a-z]+" monitoring/`)

### Research 확정 후 백포트

가동 첫날 research.md R-001~007 확정 시:
- R-001 → `env.example`, `systemd/vpub-exporter.service` 의 `--log-dir`
- R-002 → T032 `logfs_os.go` 의 glob 패턴 + T037 테스트 fixture
- R-003 → T010 `config.go` 의 default 패턴 5종
- R-004 → T054 `tier1.yaml` 의 `VpubOracleStaleVote` 임계
- R-005 → T054 의 `VpubBridgeRpcMajorityDown` 임계
- R-006 → `env.example` 의 `VPUB_BINARY_URL`
- R-007 → `env.example` 의 `VPUB_OUTCOME_CHANNEL`

각 변경은 본 tasks.md 의 해당 T 에 `[R-xxx 적용]` 주석 추가 후 commit.

---

## Notes

- `[P]` = 다른 파일 / 의존성 없어 다른 작업과 병행 가능
- `[USx]` = spec.md user story 매핑 (PR 검토 / commit log 에 명시)
- 모든 task 끝마다 commit (`feat(vpub): TXXX <subject>`)
- Constitution Gate 위반 시 즉시 stop + 사유 기록 후 사용자 협의
- 가동 후 false-positive 발생 시 contracts/alerts.md → spec.md → 본 tasks.md 순으로 정정 (변경은 spec-driven)
