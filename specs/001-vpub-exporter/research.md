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
- [ ] R-005 ⏸️ 메인넷 가동 후 (testnet votes=0). 임시 가설 `quorum=4`.
- [ ] R-006 ⏸️ HF 메인넷 binary URL announce 대기. 추정 `https://binaries.hyperliquid.xyz/validator-publisher/visor`.
- [x] R-007 ✅ 사용자 testnet 가동 시 channel ID 직접 주입 완료 (Slack 메시지 수신 확인)
- [x] R-008 ✅ 확정 (독립 module path `github.com/bharvest/vpub-exporter`)
- [x] R-009 ~ R-012 ✅ best practice 결정 완료
- [x] **추가 발견 (R-013, 2026-05-23 운영)** — systemd dbus `MainPID`/`NRestarts` 는 `Service` interface 분리 호출 필수
- [x] **추가 발견 (R-014)** — systemd unit `PrivateTmp=no` 필수 (publisher `/tmp/...` 격리 차단 X)
- [x] **추가 발견 (R-015)** — publisher visor 가 child kill 후 1-3초 즉시 재spawn → `VpubChildMissing` 안전망 룰로 재정의 (SC-002 보정)
- [x] **추가 발견 (R-016)** — `VpubLogStale` 에 `component!="visor"` 매처 (visor 자체 로그 빈도 낮음 — false-positive 차단)
- [x] **추가 발견 (R-017)** — `VpubBridgeStaleVote` mainnet only (testnet 입금 0건 자연 발화 차단)
- [x] **추가 발견 (R-018)** — critical 6 룰 mainnet 한정 + testnet `<Name>Testnet` (high) 복제 (PagerDuty noise 차단)
- [x] **추가 발견 (R-019, 2026-05-24)** — visor 가 child binary 3개를 자체 `/<child>/active` polling 으로 자동 동기화. visor 만 추적해선 child update/실패 불검출.
- [x] **추가 발견 (R-019b, 2026-05-24)** — mainnet 환경 차이 3건 식별 (사용자 보고): ubuntu homedir / 7 RPC / 로그량 ~10× testnet. 코드 변경 없이 systemd drop-in + env.mainnet.example + R-005 실측 절차 + 부하 측정 스크립트로 흡수.
- [x] **추가 발견 (R-017b, 2026-05-24)** — testnet `VpubBridgeStaleVoteLongTestnet` 임계 6h → 7d (`> 604800`). testnet 입금 트래픽 0건 영구 false-positive 차단. mainnet `VpubBridgeStaleVoteLong` (critical) 은 6h 그대로 — mainnet 가동 후 vote 빈도 실측 후 R-017c 로 재조정 예정.
- [ ] **R-017c PENDING** — mainnet 가동 후 24h~7d 동안 `vpub_bridge_last_vote_success_unix` 의 갱신 빈도 (inter-vote interval) 분포 측정. p99 의 2~3× 값을 `VpubBridgeStaleVoteLong` 임계로 확정. 측정 명령: `bash scripts/mainnet_burst_check.sh` 결과 + Prometheus query `histogram_quantile(0.99, rate(...))`.
- [x] **R-005c (2026-05-25, HF 답신)** — mainnet bridge voter quorum = **4/7 + ≤1 disagreement per vote**. 본 문서 § R-005c 참조. `VpubBridgeRpcMajorityDown` 정확. `VpubBridgeRpcDisagreement` 임계 완화 필요 — L1 upgrade 후 R-021 와 동시 적용.
- [ ] **R-003c PENDING** — `vpub_bridge_rpc_disagreement_total` 의 정확한 publisher 로그 라인 패턴 확정. 현재 `RPC failed` 임시는 호출 실패 카운터. L1 upgrade + 실제 disagreement 케이스 발생 시 로그 보고 정확한 정규식 백포트.
- [x] **R-022 (2026-05-25, mainnet 운영 발견)** — visor 가 child 를 5초 cycle 로 restart loop 도는 동안 우리 30s scrape sampling 이 child_count=3 만 잡아서 `VpubChildMissing` critical 알람 누락. 본 문서 § R-022 참조. 두 갈래 fix 적용:
  - R-022a: 룰만 정정 — `min_over_time(vpub_child_count[5m]) < 3` 으로 5분 window 안 dip 한 번이라도 잡음
  - R-022b: 신규 collector `visor_log` — `vpub_visor_child_restart_total{component=...}` + `vpub_visor_crit_total` counter. 새 룰 4건 (`VpubVisorChildRestartLoop{,Testnet}` + `VpubVisorCrit{,Testnet}`).
- [x] **R-023 (2026-05-26, monitoring alarmer 운영 발견)** — vo_slack_bot 의 silence ("disabled until") 자동 만료 시 alarmer DB 의 `agent_mark` row 가 안 지워짐 → 같은 instance+alertEvent 의 후속 firing 이 silence 인식되어 슬랙 미dispatch. 본 문서 § R-023 참조.
- [ ] **R-021 PENDING (2026-05-25, HF 답신)** — bridge/oracle 가 L1 upgrade 후 fully automatic → jail 위험. oracle 임계 5m/30m → 1m/5m, bridge 임계 1h/6h → 30m/2h, disagreement 임계 [15m]/5 → [5m]/≥2. 본 문서 § R-021 참조. L1 upgrade trigger 시 R-020 복원과 한 사이클로 적용.
- [x] **R-020 (2026-05-24)** — HF announce: mainnet validator-publisher live. bridge_voter / reference_oracle_publisher 는 **다음 L1 upgrade 까지 disabled**. outcome_voter 만 가동. 영향: bridge/oracle 의존 5 룰 임계 일시 30d 로 silence + alertLevel 정책 변경 (testnet 7룰 high→low, mainnet critical 3룰→high). 복원 절차 본 문서 R-020 섹션 참조.

---

## R-020 — mainnet 일시 silence + alertLevel 정책 변경 (HF L1 upgrade 대기)

**Trigger**: HF Telegram announce (2026-05-24) — "The mainnet validator publisher is live. The bridge voter and reference oracle publisher are currently disabled pending the next L1 network upgrade. The outcome deploy news feed should be operational on mainnet."

**Decision A — 5 룰 임계 일시 silence**:

| 룰 | 원래 임계 | 새 임계 (30d) | 의미 |
|---|---|---|---|
| VpubBridgeStaleVote | `> 3600` (1h) | `> 2592000` | bridge_voter disabled — `last_vote_success_unix` 영원히 stale |
| VpubBridgeStaleVoteLong | `> 21600` (6h) | `> 2592000` | 동상 + alertLevel critical→high |
| VpubBridgeStateStuck | `delta(...[5m]) == 0` | `delta(...[30d]) == 0` | bridge state.json mtime 영구 정체 |
| VpubOracleStaleVote | `> 300` (5m) | `> 2592000` | reference_oracle disabled — `last_vote_success_unix` 영원히 stale |
| VpubOracleStaleVoteLong | `> 1800` (30m) | `> 2592000` | 동상 + alertLevel critical→high |

VpubBridgeAllFail (vote 시도 자체 없음 → counter 0 → 자동 no-fire) 와 VpubBridgeRpcMajorityDown (vpub-exporter 의 자체 RPC ping 은 publisher 와 무관 — 정상 동작) 은 변경 X.

**Decision B — alertLevel 정책 변경**:

mainnet critical 유지 (silent failure 3개만):
- `VpubServiceDown` — publisher 자체 다운
- `VpubChildMissing` — spawn manager 망가짐
- `VpubSlackTokenInvalid` — publisher 의 모든 슬랙 알람 누락 (가장 무서움)

mainnet critical → high (3개):
- `VpubLogStaleLong`
- `VpubBridgeStaleVoteLong` (이미 R-020 에서 silence 중이지만 어쨌든)
- `VpubOracleStaleVoteLong`

testnet 전부 high → low (7개):
- `VpubServiceDownTestnet` / `VpubChildMissingTestnet` / `VpubLogStaleLongTestnet`
- `VpubBridgeRpcMajorityDownTestnet` / `VpubBridgeStaleVoteLongTestnet`
- `VpubOracleStaleVoteLongTestnet` / `VpubSlackTokenInvalidTestnet`

**Rationale**:
- testnet 알람이 ddoa-high 까지 가서 운영자 주의력 분산 — testnet 은 ddoa-low 만 충분
- mainnet critical = PagerDuty 호출 = 실제 운영자 콜아웃. silent failure 3개 외에는 PagerDuty 가치 부족 (high 채널 + 운영자 자체 모니터링이면 충분)

**복원 절차 (L1 upgrade 후 bridge_voter / oracle 재가동 시)**:

```bash
cd /Users/ijeseon/hl-agent/validator/vpub-exporter

# 1) 임계 복원 (5 룰)
python3 <<'PY'
import yaml, re
f = "monitoring/rules/hyperliquid_vpub_rule_tier1.yaml"
restore = {
    "VpubBridgeStaleVote":       ("expr", lambda e: re.sub(r"> 2592000\b", "> 3600", e)),
    "VpubBridgeStaleVoteLong":   ("expr", lambda e: re.sub(r"> 2592000\b", "> 21600", e)),
    "VpubOracleStaleVote":       ("expr", lambda e: re.sub(r"> 2592000\b", "> 300", e)),
    "VpubOracleStaleVoteLong":   ("expr", lambda e: re.sub(r"> 2592000\b", "> 1800", e)),
    "VpubBridgeStateStuck":      ("expr", lambda e: e.replace("[30d])", "[5m])")),
}
doc = yaml.safe_load(open(f))
for r in doc["rules"]:
    if r["alert"] in restore:
        k, fn = restore[r["alert"]]
        r[k] = fn(r[k])
# alertLevel 정책은 사용자 합의 — 자동 복원 안 함. 필요 시 수동.
class D(yaml.SafeDumper):
    def increase_indent(self, flow=False, indentless=False): return super().increase_indent(flow, False)
def s(d, x): return d.represent_scalar("tag:yaml.org,2002:str", x, style="|") if "\n" in x else d.represent_scalar("tag:yaml.org,2002:str", x)
D.add_representer(str, s)
open(f, "w").write(open(f).readlines()[0:17].__iter__().__next__() if False else open(f).read().split("rules:")[0] + "rules:\n" + yaml.dump(doc, Dumper=D, sort_keys=False, indent=2, allow_unicode=True, width=2000).split("rules:\n",1)[1])
PY

# 2) 통합본 재생성 + monitoring sync
# (위에서 본 동일한 python 머지 스크립트)

# 3) make verify + git push
make verify
```

**Alternatives considered**:
- 룰을 `disable_alarm='true'` 라벨로 silencing → 우리 metric 라벨 인프라가 이를 지원하려면 별도 코드 변경 필요. 일시 silence 엔 임계 변경이 더 간단.
- 영향 룰 yaml 통째로 주석 처리 → 까먹기 쉬움. 임계 변경이 grep 으로 찾기 좋음.

---

## R-022 — visor restart loop 가 30s scrape 으로 missed (2026-05-25, mainnet `--tmp-dir` 사건)

**발견**: 2026-05-25 18:06 ~ , mainnet host. visor 새 build (0.1.0 / 2026-05-25T16:15) 가 child 에게 `--tmp-dir` flag 넘기는데 HF 의 child active heights (bridge=1, oracle=1, outcome=3) 가 옛 binary 가리킴 → child 가 unknown argument 로 exit 2 → systemd restart 5초 cycle 무한 loop. **`VpubChildMissing` (critical, mainnet) 알람이 firing 하지 않음** — `vpub_child_count` 메트릭이 5초 cycle 안에서 거의 항상 3 (spawn-up 상태) 으로 잡혀 30s scrape sampling 이 dip 을 놓침.

**Decision — 두 갈래 fix**:

### R-022a — 룰만 정정 (즉시 가시성)

```yaml
# 변경 전
- alert: VpubChildMissing
  expr: vpub_child_count{...} < 3
  for: 30s

# 변경 후
- alert: VpubChildMissing
  expr: min_over_time(vpub_child_count{...}[5m]) < 3
  for: 30s
```

5분 window 안 한 번이라도 dip 잡힘. 30s scrape × 10 sample = 약 65% catch rate.

### R-022b — 신규 visor_log collector (100% catch)

새 file `internal/collectors/visor_log.go`:
- tail `<VisorLogDir>/YYYYMMDD`
- 패턴 1: `INFO\s+visor: restarting process\s+binary_path="[^"]*/([^/"]+)"` → counter `vpub_visor_child_restart_total{component=<binary basename>}`
- 패턴 2: `(CRIT|ERROR)\s+visor:` (broad — managed process exited / visor run failed 등 future variants 도 흡수) → counter `vpub_visor_crit_total`

신규 알람 4건 (mainnet + testnet 각 2):
```yaml
- alert: VpubVisorChildRestartLoop
  expr: sum by (instance) (increase(vpub_visor_child_restart_total[5m])) >= 5
  for: 1m
  labels: { alertLevel: critical, network: mainnet }
  # testnet 복제는 alertLevel low

- alert: VpubVisorCrit
  expr: increase(vpub_visor_crit_total[5m]) > 0
  for: 1m
  labels: { alertLevel: critical, network: mainnet }
  # testnet 복제 low
```

**Rationale**:
- vpub_child_count 의 sampling miss 는 publisher 가 정상일 때도 발생 가능 (visor 의 normal restart 1회 = 사실 catch 못 함). 우리 알람은 그건 의도적으로 missed (false positive 회피).
- restart loop = visor 가 5분 안에 5+ 회 restart = 진짜 비정상. counter 누적 기반이라 1회 도 안 놓침.
- visor CRIT 라인은 `VpubServiceDown` 보다 빠름 (visor 가 죽기 전 CRIT 먼저 찍음) — early warning.

**Alternatives considered**:
- A 단독 (min_over_time 만) → 65% catch. 진짜 stuck loop 도 놓칠 가능성. 채택 X.
- B 단독 (counter 만) → 정상 restart 1회까지 잡음 (noise). 채택 X.
- A + B = 둘 다 적용. min_over_time 으로 빠른 60% catch + counter 로 5분 누적 정밀 catch.

**Test fixtures (production lines)**:
```
2026-05-25T18:25:29 INFO  visor: restarting process binary_path="/home/ubuntu/v-publisher/reference-oracle-publisher" height=1 n_restarts=14
2026-05-25T18:25:34 CRIT  visor: critical error managed process exited unexpectedly binary_name="reference-oracle-publisher" ... exit_status=exit status: 2
2026-05-25T07:18:32 CRIT  visor: critical error visor run failed error=Invalid cross-device link (os error 18)
```
모두 unit test `internal/collectors/visor_log_test.go` 의 fixture 로 인입됨.

---

## R-023 — vo_slack_bot silence agent_mark 자동 복귀 버그 (2026-05-26, monitoring alarmer)

**Source**: ddoa-critical 채널 Jinu Ahn (monitoring 운영자) thread (2026-05-26 09:04~09:18 KST). 사용자가 "분명 울려야 할 것들이 안 울리고 있다" 보고 → DB 진단.

**증상**:
- prom 에서 vpub 알람 firing 정상 (`/api/v1/alerts` 다수 firing 상태)
- 옛 alertEvent (`vpub:service:down`, `vpub:visor:child_missing`) 의 critical 만 ddoa-critical 도착
- 다른 alertEvent (`vpub:bridge:stale_vote`, `vpub:log:stale`, `vpub:oracle:stale_vote`, `vpub:binary:child:download_failed` 등) 의 high/low 가 슬랙 채널 미도착

**근본 원인** (Jinu 진단):

```
1. 운영자가 slack 채널에서 알람의 "Snooze" 또는 "disabled until <ts>" 버튼 누름
2. alarmer DB 의 alerts 테이블에 row 추가:
   - mark_end = <ts>  (silence 만료 시각)
   - agent_mark = 1   (이 알람은 silence 처리됨 플래그)
3. mark_end 시각 도달 → 알람 표시 :construction: → :large_green_circle: 변경
4. BUG: agent_mark row 는 자동 삭제 안 됨 → DB 에 stale 로 남음
5. 같은 instance + alertEvent 의 후속 firing 발생
   → alarmer 가 DB 에서 agent_mark=1 발견 → "silence 처리됨" 인식
   → slack dispatch 스킵
6. 결과: 운영자 입장에선 firing 인데 슬랙 안 옴
```

**해결**:
- (즉시) Jinu 가 DB row 수동 삭제 — `DELETE FROM ... WHERE agent_mark IS NOT NULL`
- (워크어라운드) 슬랙 메시지의 **"Start" 버튼** 직접 누르면 agent_mark 도 정상 삭제. 시간 기반 자동 만료 회피.
- (영구) monitoring 측 alarmer 코드 fix — 시간 만료 시 agent_mark 자동 삭제. Jinu / Sungjin 이 thread 에서 fix 의향 표명.

**우리 vpub 운영 SOP**:
1. silence 처리 시 **"Start" 버튼 우선 사용**, 시간 기반 ("disabled until ...") 회피
2. 시간 기반 silence 한 경우 — 만료 후 "Start" 버튼 한 번 더 눌러 agent_mark 강제 정리
3. 알람 firing 인데 슬랙 미도착 의심 시 즉시 prom `/api/v1/alerts` 직접 조회 — 알람 자체 firing 여부 확인. silence stuck 의심 시 운영자 (Jinu) ping

**우리 측 작업 무관 영역 — 우리 vpub 룰 / 코드는 정상**:
- R-022 의 새 alertEvent (`vpub:visor:child_restart_loop`, `vpub:visor:crit`) 미dispatch 의문도 같은 원인 가능 — 옛 silence 의 agent_mark 가 새 alertEvent 까지 영향 줄 수 있음
- 다만 monitoring 측 alarmer 의 silence schema 가 (instance, alertEvent) 키 사용 → 새 alertEvent 는 영향 X 일 가능성. 그래도 운영 시 시간 기반 silence 회피가 안전.

---

## R-020b — bridge/oracle 가 sign 만 안 하는 게 아니라 log 도 안 찍음 (2026-05-24)

**발견**: R-020 적용 후에도 mainnet 에서 다음 알람 firing:
- `VpubLogStale` / `VpubLogStaleLong` — component=bridge-voter / reference-oracle-publisher
- `VpubChildBinaryDownloadFailed` — component=outcome-voter / reference-oracle-publisher

원인: testnet 에선 bridge/oracle 이 vote 안 해도 polling 라인 (`scanned from_block=...`) 등을 찍어서 log mtime 갱신됨. mainnet 은 **disabled 라 process 자체 아무 라인도 안 찍음** → 로그 디렉토리 mtime 영구 stale.

또 `VpubChildBinaryDownloadFailed` 의 outcome-voter false-positive 정밀 진단 필요 (사용자 관찰: "mtime 갱신된거같긴한데"). 가설: visor 가 `downloading new binary` 라인은 찍었으나 disabled child 의 실제 file write 가 skip 되어 mtime 안 갱신.

**Decision**:
1. `VpubLogStale{,Long,LongTestnet}` component 매처 확장: `component!="visor"` → `component!~"visor|bridge-voter|reference-oracle-publisher"`. 결과: outcome-voter 만 log freshness 추적. bridge/oracle 의 stall 은 `VpubBridgeStaleVote` / `VpubOracleStaleVote` 가 (현재 R-020 silence 중이지만) 의미 있는 시그널 — log mtime 으로 중복 추적 불필요.
2. `VpubChildBinaryDownloadFailed` expr 의 component 매처 추가: `=~"outcome-voter"`. visor 의 download log 가 bridge/oracle 에서 발생해도 알람 X.

**Rationale**: outcome-voter 만 mainnet 에서 operational. 알람 모두 outcome-voter 한정.

**복원 절차** (L1 upgrade 후):
```bash
# tier0.yaml — component 매처 좁혀서 원복
sed -i 's|component!~"visor|bridge-voter|reference-oracle-publisher"|component!="visor"|g' \
  monitoring/rules/hyperliquid_vpub_rule_tier0.yaml

# tier2.yaml — download_failed 룰의 component 매처 제거
python3 -c '
import yaml
f = "monitoring/rules/hyperliquid_vpub_rule_tier2.yaml"
d = yaml.safe_load(open(f))
for r in d["rules"]:
    if r["alert"] == "VpubChildBinaryDownloadFailed":
        r["expr"] = (
            "(vpub_binary_download_started_unix{disable_alarm!=\"true\"}\n"
            " - vpub_binary_local_mtime_unix) > 60"
        )
# ... yaml.dump 후 저장 (R-019 패턴)
'
# 통합본 재생성 + monitoring sync + make verify + push
```

**Pending 진단**: outcome-voter false-positive 가 진짜 visor 의 download log skip 인지, 아니면 다른 원인인지 확인 필요. 사용자 publisher 머신에서 `sudo grep "downloading new binary" /home/ubuntu/v-publisher/log/$(date -u +%Y%m%d)` 결과 + `curl localhost:8002/metrics | grep ^vpub_binary_` 비교.

---

## R-005c — RPC quorum 정확값 확정 (2026-05-25, HF 답신)

**Source**: HF 측 운영자 (Jeff/대리) Telegram 답:
> "it requires 4/7 and at most 1 disagreeing"

**확정**:
- mainnet bridge voter quorum: **min 4 RPCs live** out of 7
- vote 1회 당 disagreement: **최대 1개 허용** (2개부터 vote fail)

**기존 룰 평가**:

| 룰 | 현재 | HF 답신 기준 | 정정 필요? |
|---|---|---|---|
| `VpubBridgeRpcMajorityDown` (mainnet) | `sum(...) < 4` | `< 4` 맞음 | ❌ |
| `VpubBridgeRpcDisagreement` | `increase([15m]) > 5` | `< 2 per vote` | ✅ — 너무 느슨. `increase([5m]) >= 2` for 1m 권장 |
| `VpubBridgeRpcMajorityDownTestnet` | `sum(...) < 2` | testnet max 3 — `< 2` 그대로 | ❌ |

**Decision** (적용 시점 — L1 upgrade 후 R-020 복원과 동시):
- `VpubBridgeRpcDisagreement`: `increase(vpub_bridge_rpc_disagreement_total{disable_alarm!='true'}[5m]) >= 2` for 1m, alertLevel high

**Vote 성공 조건 (HF 의 algorithm)** — 둘 다 AND:
- **조건 A**: agreeing ≥ 4 (7 RPC 중 같은 답 4개 이상 = majority quorum)
- **조건 B**: disagreeing ≤ 1 (다른 답 보내는 RPC 1개까지만 허용)

**둘 중 하나라도 깨지면 vote skip**:
- 조건 A 깨짐 (majority 부족) — `VpubBridgeRpcMajorityDown` 이 추적. RPC 다수 down 시 발화. disagreement 와 무관.
- 조건 B 깨짐 (disagreement 2+) — `VpubBridgeRpcDisagreement` 가 추적. RPC 응답은 오지만 결과 다름 = Sybil / RPC 키 침해 / data corruption 의심.
- 두 룰이 서로 **다른 vote-fail 원인** 잡음. 동시 firing 도 가능 (둘 다 깨진 경우).

**왜 1 disagree 허용 / 2+ 거부**:
- 1 disagree: network glitch / Arbitrum indexer lag / reorg — transient noise. vote 보냄.
- 2+ disagree: systematic 한 의심. 잘못된 vote = 슬래싱 위험 → 안전하게 skip.

**케이스 표 (vote OK / skip)**:

| agreeing | disagreeing | down | A 만족 | B 만족 | 결과 |
|---:|---:|---:|:-:|:-:|---|
| 7 | 0 | 0 | ✅ | ✅ | vote OK |
| 6 | 1 | 0 | ✅ | ✅ | vote OK (1 noise 허용) |
| 5 | 2 | 0 | ✅ | ❌ | skip — Disagreement 알람 |
| 4 | 0 | 3 | ✅ | ✅ | vote OK (3 down 무관) |
| 3 | 1 | 3 | ❌ | ✅ | skip — MajorityDown 알람 (disagreement 1 은 무관) |
| 4 | 2 | 1 | ✅ | ❌ | skip — Disagreement 알람 |
| 0 | 0 | 7 | ❌ | ✅ | skip — MajorityDown 알람 |

**중요한 layer 구분 — 두 quorum 헷갈리지 말 것**:
- **RPC quorum** (이 룰의 영역): 한 publisher 안에서 7 RPC provider 의 majority. 우리 vpub-exporter 가 추적.
- **L1 validator quorum** (별개 영역): HyperCore consensus 의 stake-weighted validator vote. 우리 책임 밖 — HyperCore 자체가 처리.

**⚠️ Pending — R-003c — disagreement 패턴 정확값 미확정**:
- 현재 패턴 `WARN bridge_voter::runner: RPC failed` 는 **RPC 호출 실패** (timeout/401) 추적. HF 의 "disagreement" (다른 값 응답) 와 다른 시그널.
- testnet 9.7h 운영 + mainnet 가동 후 bridge disabled 라 진짜 disagreement 라인 본 적 없음.
- L1 upgrade 후 bridge_voter 가동 + 실제 disagreement 케이스 발생 시 publisher 로그에서 정확한 라인 패턴 확정 → R-003c 백포트.
- 우려: 현재 R-021 의 `>= 2 in 5m` 임계는 "RPC 실패 횟수" 기준. 진짜 disagreement counter 가 분리되면 임계 정밀화 가능.

---

## R-021 — Jail-safe 임계 강화 (2026-05-25, HF 답신 기반)

**Source**: HF 측 답:
> "once the bridge voter and reference oracle publisher are fully implemented, they should be fully automatic. So probably jailing for those components."

**문제**: 현재 임계는 R-003/R-004 의 "정상 ↔ 비정상" 구분용. **jail 발생 전 detect** 가 목표라면 더 보수적이어야 함. oracle 의 평균 vote interval = 4.3s 라 5m stale = 70+ vote miss = 사실상 망함. 운영자 인지 → 진단 → 수동 fix 의 골든타임 못 잡음.

**Decision** (적용 시점 — L1 upgrade 후 R-020 복원과 동시):

| 룰 | 현재 | jail-safe | 비고 |
|---|---|---|---|
| `VpubOracleStaleVote` | `> 300` (5m, high) | `> 60` (**1m**, high) | 평균 4.3s 기준 14 vote miss 시점 |
| `VpubOracleStaleVoteLong` | `> 1800` (30m, R-020 high) | `> 300` (**5m**, high) | critical 격하 (R-020 정책 그대로) — 5m = 70 vote miss = 마지막 경고 |
| `VpubBridgeStaleVote` | `> 3600` (1h) | `> 1800` (**30m**, high) | mainnet vote 빈도 R-017c 측정 후 정밀 조정 — 보수적 기본값 |
| `VpubBridgeStaleVoteLong` | `> 21600` (6h, R-020 high) | `> 7200` (**2h**, high) | 동상 |
| `VpubBridgeRpcDisagreement` | `[15m] > 5` | `[5m] >= 2` for 1m (R-005c) | HF "≤1 per vote" 반영 |

**Rationale**:
- jail = delegation 손실 + reputation 손상. 운영자 phone notification 부담 < jail 비용.
- oracle 1m alert: 4.3s × 14 = 60s 안에 14 vote miss. 운영자가 인지하고 ssh 들어가 진단 시작할 골든타임.
- oracle 5m critical: jail 직전 마지막 경고. 사용자 결정 (R-020 alertLevel 정책) 으로 high 유지 — silent failure 가 아니라 운영자가 채널 보면 즉시 인지 가능한 영역.
- bridge 30m/2h: 입금 트래픽이 sparse 할 가능성 — mainnet 실측 (R-017c) 후 더 짧게 조정 가능. 일단 보수적 기본.

**Trade-off**:
- oracle 1m 알람이 사실 false-positive 일 수도 (network glitch). `for: 1m` 으로 안정화 — 총 timeout 2m. 그래도 잡아낸 게 너무 많으면 R-021b 로 임계 완화.

**복원/적용 절차** (L1 upgrade trigger 시 한 사이클):

```bash
cd /Users/ijeseon/hl-agent/validator/vpub-exporter

# 1) R-020 임계 복원 (5룰) — research.md § R-020 스크립트
# 2) R-020b component matcher 복원 — research.md § R-020b 스크립트
# 3) R-005c + R-021 임계 강화 — 한 sed/python 으로 적용
python3 <<'PY'
import yaml
f = "monitoring/rules/hyperliquid_vpub_rule_tier1.yaml"
new_thresholds = {
    "VpubOracleStaleVote":          ("> 60", "1m"),
    "VpubOracleStaleVoteLong":      ("> 300", "5m"),
    "VpubBridgeStaleVote":          ("> 1800", "30m"),
    "VpubBridgeStaleVoteLong":      ("> 7200", "2h"),
    "VpubBridgeRpcDisagreement":    ("expr-replace", None),  # 별도
}
# ... yaml dump 후 저장
PY

# 4) 통합본 재생성 + monitoring sync + make verify + git push
```

**Pending — L1 upgrade 까지**:
- R-021 의 oracle 1m/5m 임계가 너무 빡센지 운영자 부담 평가는 가동 후 실측만 가능.
- R-017c 의 bridge mainnet 실측 vote 빈도가 R-021 의 30m/2h 와 align 되는지 확인.

---

---

## R-019 — child binary 자동 동기화 모델 (2026-05-24 운영 로그 분석)

**발견**: `/home/admin/v-publisher/log/YYYYMMDD` 의 visor 로그 분석 결과, visor 는 다음 3 child 를 HF announce URL 의 별도 path 에서 **자체 polling 으로 download** 한다:

- `https://binaries.hyperliquid-testnet.xyz/validator-publisher/bridge-voter/active`
- `https://binaries.hyperliquid-testnet.xyz/validator-publisher/outcome-voter/active`
- `https://binaries.hyperliquid-testnet.xyz/validator-publisher/reference-oracle-publisher/active`

`/active` 응답 본문 = 현재 active height (예: `5`), `Last-Modified` 헤더는 active alias 의 마지막 갱신 시각. visor 는 이 height 가 증가하면 `/<child>/<height>` URL 로 binary 를 download 한 뒤 spawn 교체.

```
2026-05-22T06:00:08 INFO visor: downloading new binary
  self.binary_name="outcome-voter"
  binary_url=".../validator-publisher/outcome-voter/1" height=1
2026-05-22T06:00:10 INFO visor: spawning new process
  binary_path="/home/admin/v-publisher/outcome-voter" height=1
```

**핵심 비대칭**:
- **visor**: HF 가 `/validator-publisher/visor` 에 publish → **사람이 wget/install 해야 적용**. 알람 발화 = "운영자가 액션할 시간".
- **child × 3**: HF 가 `/<child>/active` 갱신 → **visor 가 자동 download + spawn 교체**. 알람 발화 = "visor 의 maybe_download 가 망가졌으니 운영자가 visor 보러갈 시간".

**결정**: binary tracking 을 2-tier 로:
1. visor (manual upgrade): 기존 `remote_mtime - local_mtime > 60` expr 유지, `component="visor"` 라벨로 식별.
2. child × 3 (auto-sync fail): **visor 로그의 "downloading new binary" 라인 시각** (`vpub_binary_download_started_unix{component=<child>}`) 과 child file mtime 비교. `download_started_unix - local_mtime > 60` 이 1분 유지 = download 실패. 정상 시 mtime 이 거의 즉시 갱신되어 expr 음수 → 자동 resolve.

**왜 HTTP HEAD 가 아닌 로그 기반인가**: child URL HEAD 가 가능하긴 하지만 (200 응답 확인됨) 이는 "HF 가 publish 했다" 만 알려주고 **visor 가 download 시도했는지/실패했는지** 의 시그널은 안 줌. 로그 기반은 "visor 가 시도했는데 mtime 안 따라옴" 을 직접 잡아 진짜 실패 모드 검출. 또한 HEAD 추가 호출 (+3 req/min) 없이 기존 logtail 인프라 재사용.

**Rationale**: child auto-sync 실패는 일반적으로 maybe_download 의 retry (3s 간격) 가 충분히 견딘다. 60s 동안 mtime 미갱신이면 retry budget 초과 = 실제 실패. testnet 가동 로그 분석상 정상 download 완료까지 평균 2~3초.

**Alternatives considered**:
- 옵션 B (per-child HEAD polling, 3× /active gauge): HEAD 호출 4배 + 메트릭 4 시리즈 추가. 동일 결론 더 비싸게 도달.
- 옵션 C (logtail count only): retry 가 정상 self-heal 도 발화 → noise. 채택 X.

**임계**: download_started 후 60s 안에 mtime 미갱신 + `for: 1m` 추가 안정. 총 timeout ~120s.

---

## R-019b — mainnet 환경 차이 흡수 (2026-05-24 사용자 보고)

**Decision**: 코드는 그대로, **배포 산출물 (systemd drop-in + env example + 부하 측정 가이드) 로 환경 차이 흡수**. 메인넷 가동 시점 코드 빌드/배포 없이 cp/install + systemctl daemon-reload 만으로 완료.

| 메인넷 차이 | 흡수 방식 | 산출물 |
|---|---|---|
| User: admin → ubuntu | systemd drop-in `mainnet.conf` 에 `User=ubuntu`, `ReadOnlyPaths=/home/ubuntu/v-publisher` | `systemd/mainnet.conf` |
| RPC: 3 → 7 | bridge_rpc collector 가 list 기반이라 자동 확장. quorum 임계 (`< 4`) 는 R-005 미확정 — 가동 첫 vote 시점 실측 | `quickstart.md QS-2.1m` |
| 로그량 ~10× | logtail offset 기반 incremental read 라 자동 흡수. MemoryMax 200M → 400M drop-in. collection_duration_p95 가 SC-003 (< 1s) 안에 들어가는지 1h/6h/24h 측정 | `scripts/mainnet_burst_check.sh`, MemoryMax in drop-in |
| critical 6 룰 첫 발화 | PagerDuty dry-run 절차로 testalrt 채널 우회 검증 | `quickstart.md QS-2.1p` |

**Rationale**: R-019 (코드 변경) 와 다르게 R-019b 는 모두 **환경 변수 / unit drop-in / 운영 절차** 로 해결 가능 — 코드를 mainnet/testnet 분기로 만들면 복잡성 증가 + Constitution III (의존성 최소화) 위반.

**Alternatives considered**:
- 코드에 `network` 분기 + auto-detect (homedir / RPC count) → 거부. unit override + env 가 정답.
- mainnet 전용 binary (build tag) → 거부. 단일 binary 가 환경 차이 모두 흡수해야 운영 단순.

---
