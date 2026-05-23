# Feature Specification: vpub-exporter (hl-validator-publisher 외부 모니터링)

**Feature Branch**: `001-vpub-exporter`
**Created**: 2026-05-23
**Status**: Draft
**Input**: builnad — "validator-publisher 가동 시작인데 바이너리가 보내주는 슬랙 외에 별도 모니터링/얼러트가 필요. B-Harvest 의 기존 monitoring 레포(parser + alertmanager + Prometheus)와 통합되는 Prometheus exporter 를 만들어 주세요."

## User Scenarios & Testing

### User Story 1 — Publisher 가 죽으면 즉시 안다 (Priority: P1, MVP)

**시나리오**: B-Harvest validator 운영자(builnad)는 메인넷에서 `hl-validator-publisher`(visor + 3 child 프로세스: bridge-voter, reference-oracle-publisher, outcome-voter) 를 dedicated 머신에서 운영한다. publisher 가 보내는 슬랙 알람은 publisher 가 살아있을 때만 동작 — publisher 자체가 죽거나 child 한 개가 hang 되면 그 사실이 슬랙에 안 뜬다. 운영자는 publisher 가 진짜로 살아서 일하고 있는지 외부에서 항상 알 수 있어야 한다.

**Why this priority**: publisher 가 죽은 채로 시간이 흐르면 bridge vote 누락 → 평판 손실, 운영자 신뢰 손실. "운영자 살아있나" 질문에 외부에서 답할 수 없으면 다른 모든 모니터링이 의미 없다. 가동 첫날부터 반드시 필요.

**Independent Test**: publisher 의 systemd 서비스를 `stop` 한 뒤 1분 이내에 Slack `#ddoa-critical` 채널 + PagerDuty 알람이 도착하면 통과. child 한 개만 죽이는 케이스도 동일 검증.

**Acceptance Scenarios**:

1. **Given** publisher 가 정상 가동 중, **When** `systemctl stop validator-publisher` 실행, **Then** 1분 이내 critical 알람이 발생한다.
2. **Given** publisher visor 는 살아있지만 child 1개가 hang/죽음 + visor 의 재spawn 도 실패, **When** child count 가 3 미만으로 30초 이상 지속, **Then** critical 알람이 발생한다. **참고 (2026-05-23 LSN-D13958 관찰)**: visor 는 child 가 죽으면 1-3 초 안에 즉시 재spawn. 따라서 이 시나리오는 publisher robustness 가 실패한 매우 드문 비정상 케이스 안전망. 정상 운영 중 발화 0건 = 합격.
3. **Given** publisher 가 살아있지만 특정 컴포넌트 로그가 5분 이상 갱신되지 않음, **When** 임계 초과 2분 지속, **Then** high 알람이 발생한다 (hang 의심).
4. **Given** 위 모든 상태, **When** publisher 정상화, **Then** alertmanager 가 resolve 알람을 자동 송신한다.

---

### User Story 2 — 컴포넌트별 본업이 끝까지 가는지 본다 (Priority: P2)

**시나리오**: publisher 프로세스는 살아있어도 컴포넌트 본업이 실패할 수 있다 — bridge-voter 의 Arbitrum RPC 가 합의에 실패하거나, 시간당 vote 가 한 건도 안 나가거나, reference-oracle-publisher 가 마지막 vote 이후 너무 오래 silent 하거나, outcome-voter 슬랙 채널에 사람이 검토할 액션이 쌓이거나, Slack 토큰 자체가 만료된 상태일 수 있다. 운영자는 이런 "조용한 실패" 를 알아채야 한다.

**Why this priority**: P1 보다는 덜 치명적이지만 (publisher 는 살아있으니까) 누적되면 슬래시 위험 (outcome 적체로 인한 사람 실수) / 평판 손실 / oracle 미참여 손실로 이어진다. testnet 가동 후 로그 패턴 확정되면 곧바로 추가.

**Independent Test**: 합성 실패 시나리오 (RPC 6개 down, 1시간 vote 0건, 슬랙 토큰 무효화) 를 의도적으로 만들면 각 시나리오마다 해당 알람만 정확히 발화하는지 확인. P1 없이도 P2 알람만 독립 동작.

**Acceptance Scenarios**:

1. **Given** bridge-voter 의 Arbitrum RPC 7개 중 4개 이상 응답, **When** 살아있는 RPC 가 4개 미만으로 5분 지속, **Then** high 알람.
2. **Given** bridge voter 가 정상 vote 중 (mainnet — testnet 은 입금 트래픽 0건이라 적용 X), **When** 마지막 성공 vote 이후 1시간 경과, **Then** high 알람. 6시간 경과 시 critical 로 escalate.
3. **Given** RPC 응답이 서로 disagreement (mismatch), **When** 15분 내 5건 초과, **Then** high 알람 (잘못된 vote 위험). **참고**: testnet 실로그엔 "disagreement" 라인 없음 — 임시 패턴 (`RPC failed` WARN) 으로 대용. 메인넷 가동 후 진짜 라인 관찰 시 재정 (R-013 후속).
4. **Given** reference oracle publisher 동작 중, **When** 마지막 성공 oracle vote 이후 5분 경과 (R-004: 정상 평균 4.3초), **Then** high 알람. 30분 경과 시 critical (mainnet) / high (testnet) 로 escalate.
5. **Given** outcome-voter 가 정상, **When** outcome_actions 채널 미검토 추정 메시지 > 5 건이 30분 지속, **Then** medium 알람 (사람 검토 적체).
6. **Given** Slack API 가 정상 동작, **When** `auth.test` 가 5분간 ok=false, **Then** critical 알람 (mainnet) / high (testnet) — "publisher slack 알람이 모두 누락 중일 수 있음".
7. **Given** bridge voter 의 `~/v-publisher/bridge-voter-<chain>-state.json` 의 `last_scanned_block` 이 진행 중, **When** 5분 동안 같은 값 유지, **Then** high 알람 (`VpubBridgeStateStuck` — Arbitrum 스캔 정지, 로그 mtime 보다 강한 시그널).
8. **Given** Arbitrum RPC 가 정상, **When** 10분 내 HTTP 401 응답 5번 초과 (`Must be authenticated!`), **Then** high 알람 (`VpubBridgeRpcAuthError` — RPC 키 만료/오류 — testnet 5/22 alchemy 에서 실관찰).

---

### User Story 3 — 새 publisher 바이너리가 announce 되면 안다 (Priority: P3)

**시나리오**: HF 가 publisher 바이너리 새 버전을 binaries URL 에 게시할 때, 운영자는 즉시 인지하고 일정/위험 평가 후 수동 업그레이드 결정을 내려야 한다. 자동 업그레이드는 위험 (변경 사항 미확인 상태로 vote 거동 변경 가능).

**Why this priority**: 즉시 위기는 아니지만 놓치면 oracle/bridge 거동 차이로 운영 분기. 가동 안정 후 추가하면 충분.

**Independent Test**: 임의의 더미 URL 을 `VPUB_BINARY_URL` 에 넣고 그 URL 의 `Last-Modified` 가 로컬 바이너리 mtime 보다 +1h 가 되도록 만든 뒤 medium 알람 발생 확인.

**Acceptance Scenarios**:

1. **Given** publisher 바이너리 로컬 mtime, **When** binaries URL HEAD 의 `Last-Modified` 가 local +1h 이상 신규, **Then** 30분 후 medium 알람 ("새 바이너리 announced").
2. **Given** URL HEAD 가 1시간 동안 실패, **When** 변경 트래킹 비활성 상태, **Then** low 알람 ("URL 변경 여부 확인 필요").

---

### Edge Cases

- publisher 머신의 디스크 fill → 로그 파일 mtime 갱신은 되지만 vote 가 fail 만 함 → P2 의 `vpub_bridge_vote_total{status="fail"}` 시그널로 잡힘. 디스크 자체는 node-exporter + 기존 `disk` alert level 로 커버 (별도 룰 X).
- Publisher 가 정상이지만 입금 트래픽이 없는 시간대 → bridge vote 가 1시간 0건일 수 있음. high → critical escalate 임계 (1h/6h) 가 합리적인지 가동 1주 후 재조정.
- monitoring 레포 prom 서버와 publisher 머신 간 네트워크 단절 → Prometheus 의 `up{job="vpub-exporter"}` 자체가 0 으로 → 기존 monitoring 룰의 `up == 0` 알람으로 커버 (별도 룰 X 또는 추가 룰 1건).
- 로그 회전 시점 (00:00 UTC) 의 mtime 갱신 race → 파일 이름 패턴 `YYYYMMDD` 인식 + 가장 최근 일자 파일 stat 로직 명확화.
- Slack rate limit 으로 conversations.history 호출 실패 → fallback 으로 직전 값 유지 + `vpub_exporter_collection_errors_total{collector="slack"}` 증가.

## Requirements

### Functional Requirements

#### Tier 0 — 프로세스 / 진행성 (P1)

- **FR-001**: System MUST 5초 이내 주기로 `validator-publisher.service` 의 systemd active 상태를 0/1 게이지로 노출한다.
- **FR-002**: System MUST visor 프로세스가 spawn 한 child 개수를 게이지로 노출한다 (정상=3).
- **FR-003**: System MUST 각 컴포넌트 로그 디렉토리 (`visor`, `bridge-voter`, `reference-oracle-publisher`, `outcome-voter`) 의 최신 파일 mtime 을 unix sec 게이지로 30초 주기 갱신한다.
- **FR-004**: System MUST systemd 서비스의 누적 restart 횟수를 카운터로 노출한다.

#### Tier 1 — 컴포넌트 본업 (P2)

- **FR-005**: System MUST bridge-voter 의 각 Arbitrum RPC (이름별) 에 대해 30초 주기 헬스체크 (`eth_blockNumber` 또는 동등) 를 수행하고 `up{name}` 게이지 + latency 히스토그램 + check_total 카운터를 노출한다. **status=401 (인증 실패) 는 별도 카운터로 분리** (실로그 관찰됨).
- **FR-006**: System MUST bridge-voter 로그에서 vote 제출 결과 (성공/실패) 와 RPC disagreement 이벤트를 패턴 매칭으로 카운트한다. vote fail 의 정확한 카운트는 **CRIT 개별 이벤트 라인** (`vote failed for deposit`) 을 사용 (scanned 라인의 `votes_failed=N` 은 cumulative gauge 라 counter 변환 부적합).
- **FR-007**: System MUST bridge 와 reference-oracle 의 마지막 성공 vote unix timestamp 를 게이지로 노출한다.
- **FR-008**: System MUST reference-oracle-publisher 로그에서 vote 결과 (성공/실패) 를 패턴 매칭으로 카운트한다. ok = `oracle action sent`, fail = `hyperliquid response status=[45]xx`.
- **FR-009**: System MUST outcome-voter 로그의 warning / critical 라인을 **outcome-voter 모듈 path 한정**으로 카운트한다 (oracle WARN price drift 폭주 제외).
- **FR-010**: System MUST Slack `conversations.history` 로 `outcome_actions_channel` 의 최근 24h 메시지 수를 5분 주기로 게이지에 노출한다 (검토 적체 fallback 추적).
- **FR-011**: System MUST Slack `auth.test` 를 1분 주기로 호출하고 토큰 유효성 0/1 게이지를 노출한다.
- **FR-012a**: System MUST `~/v-publisher/bridge-voter-<chain>-state.json` (chain ∈ testnet/mainnet) 의 `last_scanned_block` 값을 게이지로 노출하고, 파일 mtime 도 별도 게이지로 노출한다. **로그 패턴보다 강한 진행성 시그널** — bridge voter 가 일을 하고 있는지 직접 확인.

#### Tier 2 — 업그레이드 트래킹 (P3)

- **FR-012**: System MUST publisher 바이너리 로컬 파일 mtime 을 unix sec 게이지로 노출한다.
- **FR-013**: System MUST `VPUB_BINARY_URL` 의 HTTP HEAD `Last-Modified` 를 10분 주기로 폴링하고 remote mtime 게이지 + check_ok 게이지를 노출한다.

#### 운영 / 보안 / 통합 요구사항

- **FR-014**: System MUST `/metrics` HTTP endpoint 를 포트 8002 에 노출한다.
- **FR-015**: System MUST 모든 시크릿 (Slack token, RPC URL, agent key 관련) 을 환경변수로만 수신하고 메트릭/라벨/로그에 노출하지 않는다.
- **FR-016**: System MUST publisher 의 config / 상태 / 로그를 절대 수정하지 않는다 (read-only).
- **FR-017**: System MUST `/metrics` 응답을 외부 호출에 블로킹되지 않게 즉시 반환한다 (캐싱 기반).
- **FR-018**: System MUST 모니터링 레포의 alertmanager 라우팅 5종 (`critical`/`high`/`medium`/`low`/`disk`) 안에서만 alert_level 을 사용하는 룰 yaml 을 제공한다.
- **FR-019**: System MUST 메트릭 이름 prefix 를 `vpub_` 으로 통일한다.
- **FR-020**: System MUST 가동 첫날 로그 패턴 확정을 위해 vote/disagreement/warn/crit 매칭 패턴을 환경변수로 오버라이드 가능하게 한다.

### Key Entities

- **Component**: visor가 관리하는 child 프로세스. 종류: `visor`(상위), `bridge-voter`, `reference-oracle-publisher`, `outcome-voter`. 각 컴포넌트는 자체 로그 디렉토리를 가짐.
- **RPC Provider**: bridge-voter 가 사용하는 Arbitrum RPC 엔드포인트. 메인넷은 7개 (alchemy/quicknode/infura/chainstack/ankr/drpc/+1), 테스트넷은 ≥1. 이름 라벨로 식별.
- **Vote Event**: 로그에서 추출되는 vote 제출 시도. 종류: `bridge` / `oracle`. 결과: `ok` / `fail`.
- **Outcome Action Message**: outcome-voter 가 Slack 채널에 게시한 검토 요청. 운영자가 SOP 따라 검토 후 수동 처리.
- **Binary Version Marker**: 로컬 바이너리 파일 mtime + 원격 URL Last-Modified.

## Success Criteria

### Measurable Outcomes

- **SC-001**: publisher 의 `systemctl stop` 후 critical 알람이 도착하기까지 평균 **<90초**, P95 **<120초**.
- **SC-002 (재정의 2026-05-23)**: publisher robustness 로 child auto-respawn 이 정상 동작하는 한 `VpubChildMissing` 알람은 발화 **0건** = pass. visor 자체 hang/실패 시에만 발화하는 안전망 룰. (원본 "<60초 detect" 기준은 publisher 동작과 양립 불가 — `VpubServiceDown` 이 SC-002 의 실질 대체 시그널.)
- **SC-003**: vpub-exporter 의 `/metrics` 응답 시간 P95 **<200ms** (외부 호출 캐싱 효과 검증).
- **SC-004**: vpub-exporter 자체의 RSS 평균 **<100MB**, CPU 평균 **<5%** (1 core 기준).
- **SC-005**: 메인넷 가동 후 첫 1주일 동안 **slashable event 0건** (자동 vote/restart 0회 확인 — 본 도구의 read-only 특성 보존).
- **SC-006**: 메인넷 가동 후 첫 1개월 동안 publisher down 사고 감지율 **100%** (외부에서 봤을 때 down 인데 알람 못 받은 케이스 0건).
- **SC-007**: 가동 1주일 후 alert 임계치 false-positive 비율 **<10%** (불필요한 알람 / 총 알람).
- **SC-008**: Tier 0 첫 PR 머지 → publisher 가동 머신에 배포까지 **<24h** (단순 install/start).

## Assumptions

- publisher 머신은 dedicated, Tokyo region, systemd 가동, user=`admin`, WorkingDirectory `/home/admin/v-publisher/`.
- 로그 디렉토리 (R-001 확정, 2026-05-23):
  - visor 자체: `~/v-publisher/log/YYYYMMDD` (testnet user=admin / mainnet user=ubuntu)
  - 3 child: **`/tmp/validator-publisher/{bridge-voter,reference-oracle-publisher,outcome-voter}/YYYYMMDD`** (`--log-dir` 영향 X — visor default 가 적용됨)
- 모니터링 레포(`validator/monitoring/`)는 PR merge → parser → ansible 로 Prometheus 서버에 룰/스크레이프 자동 배포 가능 상태.
- alertmanager 의 5종 alert_level 라우팅 (critical→PagerDuty+ddoa-critical / high→ddoa-high / medium/low→ddoa-low / disk→ddoa-disk) 은 그대로 사용.
- Slack bot token 은 publisher config 와 동일 토큰 재사용 가능 (env 로 별도 주입, config.json 직접 파싱 X).
- 메인넷 quorum 은 RPC 7개 중 4개 (가설). 실제 값은 가동 후 확정 (research.md R-005).
- 메인넷 binary URL 은 HF announce 시점에 환경변수로 주입 (R-006).
- monitoring 레포 외에 별도 ansible role 은 본 PR 범위 밖. 일단 빌드 산출물(바이너리 + systemd unit + env 템플릿) 만 제공, 배포는 후속 PR 또는 수동.
- **운영 발견 (2026-05-23 LSN-D13958 testnet 가동)**:
  - systemd dbus `MainPID`/`NRestarts` 는 `org.freedesktop.systemd1.Service` interface 에 있음 (Unit 아님). `GetUnitTypePropertiesContext(unit, "Service")` 분리 호출 필수.
  - systemd unit 의 `PrivateTmp=yes` 는 publisher 의 `/tmp/validator-publisher/` 격리. 반드시 `PrivateTmp=no` 사용 (publisher 의 `v-publisher.service.full` 도 동일).
  - critical 알람은 mainnet 한정 + testnet 은 `<Name>Testnet` (alertLevel=high) 로 분기 — PagerDuty noise 차단.
  - `VpubLogStale`/`Long` 의 `component` 라벨에서 `visor` 제외 — spawn manager 라 자체 로그 빈도 매우 낮아 false-positive.
  - `VpubBridgeStaleVote` mainnet 한정 — testnet 입금 트래픽 0건이라 영구 발화.
