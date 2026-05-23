# Phase 0 Research — vpub-exporter

본 문서는 spec.md 의 가정 / NEEDS CLARIFICATION 항목을 가동 첫날 (testnet) 에 실제로 확인하여 확정하기 위한 체크리스트다. 확정 결과는 본 문서를 갱신하고, 영향 받는 contracts/ 또는 plan.md 도 동기화한다.

## R-001 — 로그 디렉토리 실제 경로 ✅ 확정 (2026-05-23, testnet 9.7h)

**Gas pothesis 가 부분 틀렸음**.

**확정 결과**:
- visor 자체 로그: `~/v-publisher/log/YYYYMMDD` (service 의 `--log-dir log` 가 영향 — visor **자신만**)
- 3 child 컴포넌트 로그: **`/tmp/validator-publisher/{bridge-voter,reference-oracle-publisher,outcome-voter}/YYYYMMDD`** (visor default, `--log-dir` 영향 X)
- v-publisher.service.full 의 `PrivateTmp=false` 가 이 경로 의도. 정상.

**환경 차이**:
- testnet 머신 user = `admin`, WorkingDirectory = `/home/admin/v-publisher`
- mainnet 머신 user = `ubuntu`, WorkingDirectory = `/home/ubuntu/v-publisher`
- `/tmp/validator-publisher` 는 user 무관 — 양쪽 동일.

**영향 (백포트 완료)**:
- env.example: `VPUB_LOG_DIR` 한 변수 → **`VPUB_VISOR_LOG_DIR` + `VPUB_COMPONENT_LOG_DIR`** 둘로 분리
- config.go default: 동일하게 두 변수
- spec.md Assumptions §1 정정 필요

---

## R-002 — 컴포넌트별 로그 파일명 패턴 ✅ 확정

**확정 결과**: 모든 컴포넌트 디렉토리에서 **`YYYYMMDD`** 단일 파일 (확장자 없음). 하루 1 파일, 자정 UTC 회전.

**영향**: `logfs.LatestFile()` 의 단순 "최신 mtime" 로직으로 충분. 별도 glob 필터 불필요.

추가 발견 — `reference-oracle-publisher/<TOKEN>/YYYYMMDD` 가 따로 존재 (170 토큰, 각 가격 raw JSON 로그). **본 exporter 의 모니터링 대상 아님** (cardinality 폭증 위험 + 정보 가치 낮음).

---

## R-003 — Vote / Disagreement / Warn / Crit 로그 라인 패턴 ✅ 확정

**확정 결과** (9.7h testnet 로그):

### bridge-voter (`validator_publisher::bridge_voter::{rpc,runner}`)
- vote 카운터는 **라인 매칭이 아닌 숫자 추출**:
  ```
  2026-05-23T... INFO  validator_publisher::bridge_voter::runner: scanned from_block=N to_block=N
    candidates_seen=N tracked_candidates=N
    votes_sent=N votes_skipped=N votes_failed=N
    deposits=N advanced_to="N"
  ```
  Δ(votes_sent) → `vpub_bridge_vote_total{status="ok"}` 증가, Δ(votes_failed) → `{status="fail"}` 증가.
  9.7h testnet 동안 votes_sent=0, votes_failed=0 (입금 트래픽 0건 — 정상).

- RPC 헬스: `INFO  validator_publisher::bridge_voter::rpc: rpc {request|response} provider=X method="..." [status=200] ...`
- RPC 실패: `WARN  validator_publisher::bridge_voter::runner: RPC failed eth_getLogs provider="X" error=...`
  9.7h testnet 동안 15건, 전부 infura (`-32603: service temporarily unavailable`).
- **disagreement 별도 단어 없음** — testnet votes=0 라 합의 라인 미관찰. 메인넷 가동 후 재관찰 필요.

### reference-oracle-publisher (`validator_publisher::reference_oracle_publisher::{publisher,sources}`, `validator_publisher::hyperliquid::exchange_client`)
- vote ok: `INFO  validator_publisher::reference_oracle_publisher: oracle action sent`
  → 다음 라인: `INFO  validator_publisher::hyperliquid::exchange_client: hyperliquid response status=200 response={"status":"ok",...}`
  9.7h 동안 **8084건**, 평균 4.3초 간격, max 102초.
- vote fail (가설): `hyperliquid response status=[45]xx` 또는 `response={"status":"err"...}`. testnet 9.7h 동안 0건.
- WARN price drift (정상 동작): `WARN  validator_publisher::reference_oracle_publisher::publisher: non-trusted source price is (within|more than) 1% of trusted median source=X coin="Y" price=N median=N`
  → 대량 발생 (2647건/3000초). 알람 X. WARN 카운터에서 제외 권장 (`outcome` 전용으로 변경).
- ws 재연결 (정상): `WARN  validator_publisher::reference_oracle_publisher::sources: ws read error source="X"`

### outcome-voter (`validator_publisher::outcome_voter`)
- 정상: `INFO  validator_publisher::outcome_voter: spec check completed` (≈5초 주기, 9.7h 6181건)
- WARN: 0건 관찰
- CRIT: `CRIT  validator_publisher::outcome_voter: critical error failed to fetch outcome meta err=...` (9.7h 10건, 7:13~7:14 일시 502)

**영향 (백포트 완료)**:
- env.example 의 default 패턴 위 실제 문자열로 정정
- `VPUB_LOG_WARN_PATTERNS` / `VPUB_LOG_CRIT_PATTERNS` 는 **outcome-voter 모듈 한정** (oracle WARN 폭주 제외)
- `VPUB_VOTE_OK_PATTERNS` / `VPUB_VOTE_FAIL_PATTERNS` → bridge 의 votes_sent/votes_failed 정수 캡처 그룹 사용
- `VPUB_ORACLE_OK_PATTERNS` / `VPUB_ORACLE_FAIL_PATTERNS` 분리 (oracle 별도 — 코드 변경 검토)
- `VPUB_DISAGREEMENT_PATTERNS` → 임시로 "RPC failed" 매칭, 메인넷 후 재정정

---

## R-004 — Reference Oracle 게시 주기 ✅ 확정 (강력 조정 필요)

**확정 결과**: 평균 **4.3초**, max **102초** (testnet 9.7h, n=8084).

**알람 임계 재조정**:
- 현재 `VpubOracleStaleVote > 7200s` (2h) = **1675 사이클 미스** → **사실상 무용**
- 권장:
  - **>300s (5분, max=102s 의 3x 안전마진) → high**
  - **>1800s (30m) → critical**

**영향**: `monitoring/rules/hyperliquid_vpub_rule_tier1.yaml` 의 `VpubOracleStaleVote` 임계 정정 + 새 `VpubOracleStaleVoteLong` 추가.

---

## R-005 — 메인넷 RPC Quorum 정확값 ⏸️ 메인넷 가동 후 확정

**testnet 관찰**: votes_sent=0 (입금 트래픽 0건) — 합의 라인 미관찰.

**현재 임계**: `VpubBridgeRpcMajorityDown` `sum(vpub_bridge_rpc_up) < 4` (메인넷 7 → 4).

**testnet 한정 별도 룰 필요할 수 있음**: testnet 3 RPC → `< 2` 로 별도 룰. 또는 network 라벨로 분기.

**영향**: alerts.md 와 yaml 에 testnet/mainnet 룰 분리 필요. Phase N 시점에 처리.

---

## R-006 — 메인넷 Publisher 바이너리 URL

**질문**: HF 가 announce 할 메인넷 publisher 바이너리의 정확한 URL?

**Why it matters**: FR-013 (upgrade tracking) 의 데이터 소스. testnet 은 `https://binaries.hyperliquid-testnet.xyz/validator-publisher/visor` 확정.

**확정 방법**:
- HF announce 대기 (Discord / Telegram)
- 또는 `https://binaries.hyperliquid.xyz/validator-publisher/visor` 추정 후 가동 직전 확인

**확정 결과**: _(채움)_

**영향**: `VPUB_BINARY_URL` env, env.example.

---

## R-007 — Outcome Channel ID

**질문**: outcome_actions_channel 의 실제 Slack channel ID?

**Why it matters**: FR-010 (24h msg count) 의 호출 대상. 이름이 아닌 ID 필요 (channel 이름 변경에 강함).

**확정 방법**: Slack 워크스페이스 admin 페이지 또는 `conversations.list` 호출.

**확정 결과**: _(채움)_

**영향**: `VPUB_OUTCOME_CHANNEL` env (시크릿은 아니지만 env 로 주입).

---

## R-008 — Go Module Path ✅ 확정 (2026-05-23)

**질문**: B-Harvest 내부 Go 컨벤션상 module path 는?

**확정 결과**: **`github.com/bharvest/vpub-exporter`**

**근거**: 사용자 결정 — 기존 `hlmon` 등의 코드 베이스와 **분리**해서 새로 작성. 기존 도구 go.mod 참조 안 함. 코드 컨벤션 / 라이브러리 / 구조 모두 본 spec-kit 산출물 기준으로 독립적으로 구축.

**참고**: 위 module 경로의 prefix (`github.com/bharvest`) 가 실제 B-Harvest GitHub organization 이름과 다르면 코드 작성 시 멈추고 질문 필요.

---

## R-009 — Best practice: Prometheus client_golang 표준 사용 패턴

**Decision**: 표준 `prometheus.NewRegistry()` + collector 별 `prometheus.Collector` 인터페이스 구현 + `promhttp.HandlerFor(registry, promhttp.HandlerOpts{})`.

**Rationale**: 가장 흔한 패턴. 코드 양 적음. promauto 는 전역 registry 의존성이라 테스트 어려움 → 명시 registry.

**Alternatives considered**: `promauto` (전역 — 거부), OpenMetrics 직접 텍스트 출력 (오버킬).

---

## R-010 — Best practice: systemd 상태 조회

**Decision**: `github.com/coreos/go-systemd/v22/dbus` 의 `Conn` 으로 `GetUnitProperties("v-publisher.service")` 호출. `ActiveState`, `MainPID`, `NRestarts` 추출.

**Rationale**: dbus 직접 호출이 가장 가볍고 신뢰성 높음. `exec systemctl is-active` 도 가능하지만 매 5s exec 는 자원 낭비.

**Alternatives considered**: `os/exec systemctl ...` (거부 — 오버헤드), `journalctl --since` 파싱 (거부 — 복잡).

---

## R-011 — Best practice: 로그 파일 tail (회전 인식)

**Decision**: 자체 구현 (Go std `os.Open` + `io.Reader` 위에 line-by-line). 매 30초마다 "현재 watched file == 최신 파일인지" 체크, 다르면 재오픈.

**Rationale**: `hpcloud/tail` 같은 라이브러리는 inotify 기반인데, publisher 로그는 일별 rotation (`YYYYMMDD` 새 파일) 이라 inotify 만으로 부족. 직접 polling + rename 감지가 더 robust.

**Alternatives considered**: `hpcloud/tail` (회전 인식 약함), `fsnotify` 직접 사용 (구현 복잡).

---

## R-012 — Best practice: Slack API client

**Decision**: 표준 `net/http` + JSON 직접 호출. `auth.test`, `conversations.history` 2개만 필요 → 라이브러리 의존 불필요.

**Rationale**: 의존성 최소화 (Constitution III + 기존 hl-exporter 컨벤션). 인증은 `Authorization: Bearer <token>` 헤더.

**Alternatives considered**: `slack-go/slack` (거부 — 오버킬).

---

## 완료 체크

- [x] R-001 ✅ 확정 (2026-05-23, testnet 9.7h) — visor + component log dirs 분리
- [x] R-002 ✅ 확정 — `YYYYMMDD` 단일 파일
- [x] R-003 ✅ 확정 — 실패턴 추출, env.example default 갱신
- [x] R-004 ✅ 확정 — 평균 4.3s, 임계 강력 조정
- [ ] R-005 ⏸️ 메인넷 가동 후 (testnet votes=0)
- [ ] R-006 ⏸️ HF 메인넷 binary URL announce 대기
- [ ] R-007 ⏸️ 사용자 Slack channel ID 채움
- [x] R-008 ✅ 확정 (독립 module path)
- [x] R-009 ~ R-012 ✅ best practice 결정 완료
